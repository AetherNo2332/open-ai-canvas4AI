package repository

import (
	"encoding/json"

	"gorm.io/gorm"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

type ecommerceLegacyRound struct {
	draft  string
	images []model.EcommercePlanTask
}

// convertEcommerceLegacyRounds moves inline previousImages into round rows,
// grouped by their original draft snapshot. Repeated calls are no-ops because
// the stored plan no longer carries previousImages afterwards.
func convertEcommerceLegacyRounds(tx *gorm.DB, project *model.EcommerceProject) (int, error) {
	var plan model.EcommercePlan
	if json.Unmarshal([]byte(project.PlanJSON), &plan) != nil {
		plan = model.EcommercePlan{}
	}
	if len(plan.PreviousImages) == 0 {
		if project.ActiveRoundID == "" {
			project.ActiveRoundID = kernel.NewID()
		}
		return 0, nil
	}
	groups := groupEcommerceLegacyRounds(plan.PreviousImages)
	archivedTasks, err := ecommerceArchivedTasks(tx, project.ID)
	if err != nil {
		return 0, err
	}
	var ordinal int
	if err := tx.Model(&model.EcommerceRound{}).Where("project_id = ?", project.ID).Select("COALESCE(MAX(ordinal), 0)").Scan(&ordinal).Error; err != nil {
		return 0, err
	}
	created := 0
	for _, item := range groups {
		if ecommerceLegacyRoundReplayed(item, archivedTasks) {
			continue
		}
		dedup := ecommerceRoundKey(item.draft, item.images)
		var existing int64
		if err := tx.Model(&model.EcommerceRound{}).Where("project_id = ? AND dedup_key = ?", project.ID, dedup).Count(&existing).Error; err != nil {
			return 0, err
		}
		if existing > 0 {
			continue
		}
		ordinal++
		roundPlan, err := json.Marshal(model.EcommercePlan{Images: item.images})
		if err != nil {
			return 0, err
		}
		round := model.EcommerceRound{
			ID:         kernel.NewID(),
			UserID:     project.UserID,
			ProjectID:  project.ID,
			Ordinal:    ordinal,
			DedupKey:   dedup,
			DraftJSON:  item.draft,
			PlanJSON:   string(roundPlan),
			ImageCount: len(item.images),
			Version:    1,
		}
		if err := tx.Create(&round).Error; err != nil {
			return 0, err
		}
		for _, image := range item.images {
			if image.TaskID != "" {
				archivedTasks[image.TaskID] = struct{}{}
			}
		}
		created++
	}
	plan.PreviousImages = nil
	raw, err := json.Marshal(plan)
	if err != nil {
		return 0, err
	}
	project.PlanJSON = string(raw)
	if project.ActiveRoundID == "" {
		project.ActiveRoundID = kernel.NewID()
	}
	return created, nil
}

func groupEcommerceLegacyRounds(images []model.EcommercePlanTask) []ecommerceLegacyRound {
	// Only adjacent images sharing a draft form one round. Grouping by draft
	// globally would merge later rounds back into earlier chronological groups.
	groups := make([]ecommerceLegacyRound, 0, 4)
	for _, image := range images {
		draftJSON := "{}"
		if len(image.SourceDraft) > 0 && json.Valid(image.SourceDraft) {
			draftJSON = string(image.SourceDraft)
		}
		image.RoundID = ""
		if len(groups) == 0 || groups[len(groups)-1].draft != draftJSON {
			groups = append(groups, ecommerceLegacyRound{draft: draftJSON})
		}
		groups[len(groups)-1].images = append(groups[len(groups)-1].images, image)
	}
	return groups
}

func ecommerceArchivedTasks(tx *gorm.DB, projectID string) (map[string]struct{}, error) {
	var existingRounds []model.EcommerceRound
	if err := tx.Model(&model.EcommerceRound{}).Where("project_id = ?", projectID).Select("plan_json").Find(&existingRounds).Error; err != nil {
		return nil, err
	}
	archivedTasks := make(map[string]struct{})
	for _, round := range existingRounds {
		var archived model.EcommercePlan
		if json.Unmarshal([]byte(round.PlanJSON), &archived) != nil {
			continue
		}
		for _, image := range archived.Images {
			if image.TaskID != "" {
				archivedTasks[image.TaskID] = struct{}{}
			}
		}
	}
	return archivedTasks, nil
}

func ecommerceLegacyRoundReplayed(round ecommerceLegacyRound, archivedTasks map[string]struct{}) bool {
	// Legacy snapshots can replay artwork with edited drafts or removed images.
	// Any task overlap marks an already archived group, protecting edited history.
	for _, image := range round.images {
		if _, exists := archivedTasks[image.TaskID]; exists && image.TaskID != "" {
			return true
		}
	}
	return false
}
