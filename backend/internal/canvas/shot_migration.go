package canvas

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"yingce/backend/internal/canvas/legacymigrate"
	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

const shotMigrationSnapshotReason = "before_shot_migration"

// CanvasShotMigrationPreview combines the read-only S11 converter with the S12
// shot-row plan and the in-flight task gate. It never writes the document.
type CanvasShotMigrationPreview struct {
	Revision    int64                          `json:"revision"`
	Required    bool                           `json:"required"`
	Ready       bool                           `json:"ready"`
	Conflicts   []references.MigrationConflict `json:"conflicts"`
	ShotSubmaps int                            `json:"shotSubmaps"`
	Rows        int                            `json:"rows"`
	ActiveTasks []string                       `json:"activeTasks"`
	Blocking    []legacymigrate.Issue          `json:"blocking"`
	Warnings    []legacymigrate.Issue          `json:"warnings"`
}

type CanvasShotMigrationRequest struct {
	Revision *int64            `json:"revision"`
	Choices  map[string]string `json:"choices"`
}

type CanvasShotMigrationResult struct {
	Project         UserDataSummary `json:"project"`
	Submaps         int             `json:"submaps"`
	Rows            int             `json:"rows"`
	ExistingImages  int             `json:"existingImages"`
	ExistingVideos  int             `json:"existingVideos"`
	PreservedAssets int             `json:"preservedAssets"`
}

type CanvasShotMigrationRollbackRequest struct {
	Revision *int64 `json:"revision"`
	Strategy string `json:"strategy"`
}

type CanvasShotMigrationRollbackResult struct {
	Restored          bool   `json:"restored"`
	NewCanvasID       string `json:"newCanvasId,omitempty"`
	SnapshotID        string `json:"snapshotId"`
	MigrationRevision int64  `json:"migrationRevision"`
	Revision          int64  `json:"revision,omitempty"`
}

func (s *Service) PreviewCanvasShotMigration(userID, canvasID string) (CanvasShotMigrationPreview, error) {
	project, err := s.ownedCanvasProject(userID, canvasID)
	if err != nil {
		return CanvasShotMigrationPreview{}, err
	}
	preview, err := legacymigrate.PreviewPayload(project.PayloadJSON, legacymigrate.Options{Source: legacymigrate.SourceDatabase})
	if err != nil {
		return CanvasShotMigrationPreview{}, kernel.BadAuthRequest(err.Error())
	}
	active, err := s.activeShotMigrationTasks(userID, canvasID, project.PayloadJSON)
	if err != nil {
		return CanvasShotMigrationPreview{}, err
	}
	return CanvasShotMigrationPreview{
		Revision: project.Revision, Required: len(preview.ShotSubmaps) > 0,
		Ready: preview.Ready, Conflicts: preview.Conflicts, ShotSubmaps: len(preview.ShotSubmaps),
		Rows: countShotRows(preview.ShotSubmaps), ActiveTasks: active,
		Blocking: preview.Blocking, Warnings: preview.Warnings,
	}, nil
}

func (s *Service) MigrateCanvasShots(userID, canvasID string, req CanvasShotMigrationRequest) (CanvasShotMigrationResult, error) {
	if req.Revision == nil {
		return CanvasShotMigrationResult{}, kernel.NewAppError(http.StatusPreconditionRequired, "缺少画布版本，请重新加载画布")
	}
	if err := validateShotMigrationChoices(req.Choices); err != nil {
		return CanvasShotMigrationResult{}, err
	}
	project, err := s.ownedCanvasProject(userID, canvasID)
	if err != nil {
		return CanvasShotMigrationResult{}, err
	}
	if project.Revision != *req.Revision {
		return CanvasShotMigrationResult{}, canvasRevisionConflict()
	}
	active, err := s.activeShotMigrationTasks(userID, canvasID, project.PayloadJSON)
	if err != nil {
		return CanvasShotMigrationResult{}, err
	}
	if len(active) > 0 {
		return CanvasShotMigrationResult{}, canvasMutationError("shot_migration_tasks_active",
			"画布仍有在途生成任务，请等待任务结束或取消后再切换格式："+strings.Join(active, "、"))
	}
	doc, err := canvasReferenceDocument(project.PayloadJSON)
	if err != nil {
		return CanvasShotMigrationResult{}, err
	}
	if !references.Enabled(doc) {
		normalized, normalizeErr := references.NormalizeLegacy(doc, req.Choices, nil, nil)
		if normalizeErr != nil {
			if typed, ok := normalizeErr.(*references.Error); ok {
				appErr := kernel.BadAuthRequest(typed.Message)
				appErr.Reason = "shot_migration_invalid"
				return CanvasShotMigrationResult{}, appErr
			}
			return CanvasShotMigrationResult{}, normalizeErr
		}
		if len(normalized.Conflicts) > 0 {
			return CanvasShotMigrationResult{}, canvasMutationError("shot_migration_conflict",
				"仍有引用来源未选择，请为每个冲突选择以字段或连线为准")
		}
		doc = normalized.Document
	}
	counters := canvasMigrationCounters(doc)
	applied, err := legacymigrate.ApplyShotSubmaps(doc, func(prefix string) string {
		counters[prefix]++
		return fmt.Sprintf("%s-%d", prefix, counters[prefix])
	})
	if err != nil {
		return CanvasShotMigrationResult{}, kernel.BadAuthRequest(err.Error())
	}
	if applied.Rows == 0 {
		return CanvasShotMigrationResult{}, canvasMutationError("shot_migration_not_required",
			"画布没有待迁移的分镜行")
	}
	applied.Document["id"] = canvasID
	applied.Document["revision"] = project.Revision
	applied.Document["idCounters"] = canvasMigrationCounterValues(counters)
	raw, err := json.Marshal(applied.Document)
	if err != nil {
		return CanvasShotMigrationResult{}, err
	}
	summary, err := s.upsertUserCanvasProjectWithHistory(userID, raw, shotMigrationSnapshotReason)
	if err != nil {
		return CanvasShotMigrationResult{}, err
	}
	return CanvasShotMigrationResult{
		Project: summary, Submaps: applied.Submaps, Rows: applied.Rows,
		ExistingImages: applied.ExistingImages, ExistingVideos: applied.ExistingVideos,
		PreservedAssets: applied.PreservedAssets,
	}, nil
}

func (s *Service) RollbackCanvasShots(userID, canvasID string, req CanvasShotMigrationRollbackRequest) (CanvasShotMigrationRollbackResult, error) {
	if req.Revision == nil {
		return CanvasShotMigrationRollbackResult{}, kernel.NewAppError(http.StatusPreconditionRequired, "缺少画布版本，请重新加载画布")
	}
	strategy := strings.TrimSpace(req.Strategy)
	if strategy == "" {
		strategy = "restore"
	}
	if strategy != "restore" && strategy != "new_canvas" {
		return CanvasShotMigrationRollbackResult{}, kernel.BadAuthRequest("回退策略只能是 restore 或 new_canvas")
	}
	current, err := s.ownedCanvasProject(userID, canvasID)
	if err != nil {
		return CanvasShotMigrationRollbackResult{}, err
	}
	if current.Revision != *req.Revision {
		return CanvasShotMigrationRollbackResult{}, canvasRevisionConflict()
	}
	snapshot, err := s.repo.LatestCanvasSnapshotWithReason(userID, canvasID, shotMigrationSnapshotReason)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CanvasShotMigrationRollbackResult{}, canvasMutationError("shot_migration_rollback_missing",
			"找不到这次格式切换前的历史快照")
	}
	if err != nil {
		return CanvasShotMigrationRollbackResult{}, err
	}
	newer := current.Revision > snapshot.Revision+1
	if newer && strategy != "new_canvas" {
		return CanvasShotMigrationRollbackResult{}, canvasMutationError("shot_migration_has_newer_edits",
			"切换后已有新编辑，不能直接覆盖；请选择 new_canvas 保留当前画布并生成迁移前副本")
	}
	if strategy == "new_canvas" {
		newID, err := s.copyCanvasSnapshotToNewCanvas(userID, current.Title, snapshot)
		if err != nil {
			return CanvasShotMigrationRollbackResult{}, err
		}
		return CanvasShotMigrationRollbackResult{
			NewCanvasID: newID, SnapshotID: snapshot.ID, MigrationRevision: snapshot.Revision + 1,
		}, nil
	}
	summary, err := s.RestoreCanvasHistory(userID, canvasID, snapshot.ID, &current.Revision)
	if err != nil {
		return CanvasShotMigrationRollbackResult{}, err
	}
	return CanvasShotMigrationRollbackResult{
		Restored: true, SnapshotID: snapshot.ID, MigrationRevision: snapshot.Revision + 1, Revision: summary.Revision,
	}, nil
}

func (s *Service) copyCanvasSnapshotToNewCanvas(userID, title string, snapshot *model.CanvasSnapshot) (string, error) {
	doc, err := canvasReferenceDocument(snapshot.PayloadJSON)
	if err != nil {
		return "", err
	}
	newID := kernel.NewID()
	doc["id"] = newID
	doc["revision"] = 0
	doc["title"] = strings.TrimSpace(title) + "（迁移前副本）"
	delete(doc, "createdAt")
	delete(doc, "updatedAt")
	raw, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	if _, err := s.upsertUserCanvasProjectWithHistory(userID, raw, "initial"); err != nil {
		return "", err
	}
	return newID, nil
}

func (s *Service) activeShotMigrationTasks(userID, canvasID, payload string) ([]string, error) {
	tasks, err := s.repo.ActiveCanvasProjectTasks(userID, canvasID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, task := range tasks {
		if task.ID == "" || seen[task.ID] {
			continue
		}
		seen[task.ID] = true
		out = append(out, "task:"+task.ID)
	}
	doc, err := canvasReferenceDocument(payload)
	if err != nil {
		return nil, err
	}
	for _, target := range activeCanvasDocumentTargets(doc) {
		if seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out, nil
}

func activeCanvasDocumentTargets(doc map[string]any) []string {
	active := map[string]bool{
		"queued": true, "running": true, "loading": true, "pending": true,
		"submitting": true, "submission_uncertain": true,
	}
	out := []string{}
	for _, node := range canvasMaps(doc["nodes"]) {
		id := canvasText(node["id"])
		metadata := canvasObject(node["metadata"])
		if active[canvasText(metadata["status"])] || active[canvasText(metadata["taskStatus"])] {
			out = append(out, "node:"+id)
		}
		board := canvasObject(metadata["storyboard"])
		for _, row := range canvasMaps(board["rows"]) {
			if active[canvasText(row["status"])] || active[canvasText(row["taskStatus"])] {
				out = append(out, "row:"+id+":"+canvasText(row["id"]))
			}
		}
		table := canvasObject(metadata["batchTable"])
		for _, row := range canvasMaps(table["rows"]) {
			if active[canvasText(row["status"])] || active[canvasText(row["taskStatus"])] {
				out = append(out, "row:"+id+":"+canvasText(row["id"]))
			}
		}
	}
	return out
}

func countShotRows(plan []legacymigrate.ShotSubmap) int {
	rows := 0
	for _, shot := range plan {
		if shot.RowID != "" {
			rows++
		}
	}
	return rows
}

func validateShotMigrationChoices(choices map[string]string) error {
	for key, choice := range choices {
		if key == "" || len(key) > 400 || (choice != references.MigrationChoiceFields && choice != references.MigrationChoiceConnections) {
			return kernel.BadAuthRequest("引用来源只能选 fields 或 connections")
		}
	}
	return nil
}

func canvasMigrationCounters(doc map[string]any) map[string]int {
	out := map[string]int{}
	for key, value := range canvasObject(doc["idCounters"]) {
		switch number := value.(type) {
		case float64:
			out[key] = int(number)
		case int:
			out[key] = number
		case int64:
			out[key] = int(number)
		}
	}
	return out
}

func canvasMigrationCounterValues(counters map[string]int) map[string]any {
	out := make(map[string]any, len(counters))
	for key, value := range counters {
		out[key] = value
	}
	return out
}

func canvasMaps(value any) []map[string]any {
	out := []map[string]any{}
	if rows, ok := value.([]any); ok {
		for _, row := range rows {
			if item, ok := row.(map[string]any); ok {
				out = append(out, item)
			}
		}
		return out
	}
	if rows, ok := value.([]map[string]any); ok {
		return rows
	}
	return out
}

func canvasObject(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	return map[string]any{}
}

func canvasText(value any) string {
	if item, ok := value.(string); ok {
		return item
	}
	return ""
}
