package repository

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
	"sort"
	"time"
)

func (r *Repository) AgentSchedulerSetting(fallback model.AgentSchedulerSetting) (model.AgentSchedulerSetting, error) {
	if !r.db.Migrator().HasTable(&model.AgentSchedulerSetting{}) {
		return fallback, nil
	}
	var p model.AgentSchedulerSetting
	err := r.db.First(&p, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fallback, nil
	}
	return p, err
}

func (r *Repository) SaveAgentSchedulerSetting(p *model.AgentSchedulerSetting, expected int64, audit *model.AdminAuditEvent) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Configuration changes and admissions share one linearization point.
		counter := model.AgentAdmissionCounter{ID: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&counter).Error; err != nil {
			return err
		}
		if err := tx.Model(&counter).Where("id = ?", 1).UpdateColumn("value", gorm.Expr("value + 0")).Error; err != nil {
			return err
		}
		initial := model.DefaultAgentSchedulerSetting()
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&initial).Error; err != nil {
			return err
		}
		next := *p
		next.ID = 1
		next.Revision = expected + 1
		next.UpdatedAt = time.Now()
		result := tx.Model(&model.AgentSchedulerSetting{}).Where("id = ? AND revision = ?", 1, expected).Updates(map[string]any{"dispatch_concurrency": next.DispatchConcurrency, "max_resident_sessions": next.MaxResidentSessions, "max_resident_per_canvas": next.MaxResidentPerCanvas, "revision": next.Revision, "updated_by": next.UpdatedBy, "updated_at": next.UpdatedAt})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCreationConflict
		}
		if err := tx.Create(audit).Error; err != nil {
			return err
		}
		if err := model.AppendAgentWake(tx, model.AgentWakeEvent{Kind: "scheduler_config_changed", Revision: next.Revision}); err != nil {
			return err
		}
		*p = next
		return nil
	})
}

func admissionKey(run model.CloudAgentExecution) string {
	if run.CanvasID != "" {
		return run.UserID + ":" + run.CanvasID
	}
	return run.UserID + ":session:" + run.ConversationID + ":" + run.ID
}

func (r *Repository) ClaimPiAgentFair(owner string, until time.Time, fallback model.AgentSchedulerSetting) (*model.CloudAgentExecution, error) {
	var claimed *model.CloudAgentExecution
	err := r.db.Transaction(func(tx *gorm.DB) error {
		counter := model.AgentAdmissionCounter{ID: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&counter).Error; err != nil {
			return err
		}
		// The write holds the row until commit (also acquires SQLite's write lock).
		if err := tx.Model(&counter).Where("id = ?", 1).UpdateColumn("value", gorm.Expr("value + 0")).Error; err != nil {
			return err
		}
		if err := tx.First(&counter, 1).Error; err != nil {
			return err
		}
		policy, err := New(tx).AgentSchedulerSetting(fallback)
		if err != nil {
			return err
		}
		now := time.Now()
		var candidates []model.CloudAgentExecution
		if err := tx.Select("id", "user_id", "canvas_id", "conversation_id", "engine", "status", "created_at", "revision", "wait_kind").Where("engine = ? AND status IN ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)", "pi", []string{"queued", "running", "waiting_approval"}, now).Order("created_at,id").Find(&candidates).Error; err != nil {
			return err
		}
		var live []model.CloudAgentExecution
		if err := tx.Select("id", "user_id", "canvas_id", "conversation_id").Where("engine = ? AND status IN ? AND lease_expires_at > ?", "pi", []string{"running", "waiting_approval"}, now).Find(&live).Error; err != nil {
			return err
		}
		counts := map[string]int{}
		for _, run := range live {
			counts[admissionKey(run)]++
		}
		var states []model.AgentCanvasAdmission
		if err := tx.Find(&states).Error; err != nil {
			return err
		}
		sequences := map[string]int64{}
		for _, state := range states {
			sequences[state.Key] = state.LastAdmissionSequence
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			a, b := admissionKey(candidates[i]), admissionKey(candidates[j])
			if a == b {
				return false
			}
			if sequences[a] != sequences[b] {
				return sequences[a] < sequences[b]
			}
			return a < b
		})
		for _, candidate := range candidates {
			key := admissionKey(candidate)
			if counts[key] >= policy.MaxResidentPerCanvas {
				if candidate.Status == "queued" && candidate.WaitKind != "canvas_capacity" {
					if err := tx.Model(&model.CloudAgentExecution{}).Where("id = ? AND revision = ?", candidate.ID, candidate.Revision).Updates(map[string]any{"runtime_phase": "waiting_resource", "wait_kind": "canvas_capacity", "wait_reason": "等待画布会话容量", "revision": gorm.Expr("revision + 1")}).Error; err != nil {
						return err
					}
				}
				continue
			}
			conversation := candidate.ConversationID
			if conversation == "" {
				conversation = candidate.ID
			}
			var session model.CloudAgentPiSession
			err := tx.Where("conversation_id = ? AND user_id = ? AND active_run_id = ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)", conversation, candidate.UserID, candidate.ID, now).First(&session).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if err := New(tx).lockAgentCrewExecution(candidate.UserID, candidate.ID); err != nil {
				return err
			}
			result := tx.Model(&model.CloudAgentExecution{}).Where("id = ? AND revision = ? AND status IN ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)", candidate.ID, candidate.Revision, []string{"queued", "running", "waiting_approval"}, now).Updates(map[string]any{"lease_owner": owner, "lease_expires_at": until, "status": gorm.Expr("CASE WHEN status = 'queued' THEN 'running' ELSE status END"), "runtime_phase": "ready", "wait_kind": "", "wait_reason": "", "revision": gorm.Expr("revision + 1")})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				continue
			}
			result = tx.Model(&model.CloudAgentPiSession{}).Where("id = ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)", session.ID, now).Updates(map[string]any{"lease_owner": owner, "lease_expires_at": until, "lease_epoch": gorm.Expr("lease_epoch + 1"), "updated_at": now})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrCreationConflict
			}
			counter.Value++
			if err := tx.Model(&counter).UpdateColumn("value", counter.Value).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.AgentCanvasAdmission{Key: key, LastAdmissionSequence: counter.Value}).Error; err != nil {
				return err
			}
			claimed = &candidate
			current, err := New(tx).CloudAgent(candidate.UserID, candidate.ID)
			if err != nil {
				return err
			}
			if err := New(tx).projectAgentCrewExecution(current); err != nil {
				return err
			}
			return nil
		}
		return nil
	})
	if err != nil || claimed == nil {
		return nil, err
	}
	return r.CloudAgent(claimed.UserID, claimed.ID)
}
