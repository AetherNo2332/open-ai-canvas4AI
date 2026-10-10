package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"infinite-canvas/backend/internal/model"
	"time"
)

func (r *Repository) AgentWakeEvents(after int64, limit int) ([]model.AgentWakeEvent, error) {
	if limit < 1 || limit > 256 {
		limit = 128
	}
	var events []model.AgentWakeEvent
	err := r.db.Where("sequence > ?", after).Order("sequence").Limit(limit).Find(&events).Error
	return events, err
}

func (r *Repository) CloudAgentControlRow(userID, runID string) (*model.CloudAgentExecution, error) {
	var run model.CloudAgentExecution
	err := r.db.Where("id = ? AND user_id = ?", runID, userID).First(&run).Error
	return &run, err
}

func (r *Repository) CloudAgentPiLease(userID, conversationID string) (*model.CloudAgentPiSession, error) {
	var session model.CloudAgentPiSession
	err := r.db.Select("id", "user_id", "conversation_id", "active_run_id", "lease_owner", "lease_epoch", "lease_expires_at").Where("user_id = ? AND conversation_id = ?", userID, conversationID).First(&session).Error
	return &session, err
}

func (r *Repository) AgentCapacity(owner string, active, capacity int) error {
	return r.AgentCapacityReport(owner, model.AgentCapacityReport{Active: active, Capacity: capacity})
}

func (r *Repository) AgentRuntimeInstances() ([]model.AgentRuntimeInstance, error) {
	var rows []model.AgentRuntimeInstance
	err := r.db.Order("updated_at DESC,id").Find(&rows).Error
	return rows, err
}

func (r *Repository) AgentCapacityReport(owner string, p model.AgentCapacityReport) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.AgentRuntimeInstance{ID: owner, Active: p.Active, Capacity: p.Capacity, ClaimReservations: p.ClaimReservations, DispatchActive: p.DispatchActive, ReadyQueued: p.ReadyQueued, Draining: p.Draining, AppliedConfigRevision: p.AppliedConfigRevision, UpdatedAt: time.Now()}).Error; err != nil {
			return err
		}
		var available int64
		if err := tx.Model(&model.AgentRuntimeInstance{}).Where("updated_at > ? AND active + claim_reservations < capacity", time.Now().Add(-45*time.Second)).Count(&available).Error; err != nil {
			return err
		}
		phase, kind, reason := "waiting_resource", "capacity", "等待会话运行容量"
		if available > 0 {
			phase, kind, reason = "ready", "", ""
		}
		return tx.Model(&model.CloudAgentExecution{}).Where("engine = ? AND status = ? AND lease_expires_at IS NULL AND runtime_phase <> ? AND wait_kind <> ?", "pi", "queued", phase, "canvas_capacity").Updates(map[string]any{"runtime_phase": phase, "wait_kind": kind, "wait_id": "", "wait_reason": reason, "revision": gorm.Expr("revision + 1")}).Error
	})
}

func (r *Repository) SetAgentPhase(userID, runID, owner string, epoch int64, phase, kind, waitID, reason string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var session model.CloudAgentPiSession
		// 租约列由认领/续租路径以 UTC 写入，比较参数必须同为 UTC（见 ClaimPiAgentConfigured 注释）。
		if err := tx.Where("user_id = ? AND active_run_id = ? AND lease_owner = ? AND lease_epoch = ? AND lease_expires_at > ?", userID, runID, owner, epoch, time.Now().UTC()).First(&session).Error; err != nil {
			return ErrCreationConflict
		}
		updates := map[string]any{"runtime_phase": phase, "wait_kind": kind, "wait_id": waitID, "wait_reason": reason, "revision": gorm.Expr("revision + 1")}
		if phase == "waiting_tool" && kind == "tool" {
			// The async worker may commit a dependency wait before Node reports its
			// generic tool phase. Preserve the authoritative wait in the same write.
			for _, field := range []string{"runtime_phase", "wait_kind", "wait_id", "wait_reason"} {
				updates[field] = gorm.Expr("CASE WHEN wait_kind = 'subagents' THEN "+field+" ELSE ? END", updates[field])
			}
		}
		updated := tx.Model(&model.CloudAgentExecution{}).Where("id = ? AND user_id = ? AND lease_owner = ? AND lease_expires_at > ? AND status IN ?", runID, userID, owner, time.Now().UTC(), []string{"running", "waiting_approval"}).
			Where("EXISTS (SELECT 1 FROM cloud_agent_pi_sessions WHERE user_id = ? AND active_run_id = ? AND lease_owner = ? AND lease_epoch = ? AND lease_expires_at > ?)", userID, runID, owner, epoch, time.Now().UTC()).
			Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrCreationConflict
		}
		return nil
	})
}

func (r *Repository) PrepareAgentToolOperation(operation *model.AgentToolOperation) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// The operation key is a business identity, never a random retry identity.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(operation).Error; err != nil {
			return err
		}
		return tx.First(operation, "id = ?", operation.ID).Error
	})
}

func (r *Repository) AgentToolOperations(limit int) ([]model.AgentToolOperation, error) {
	var operations []model.AgentToolOperation
	err := r.db.Where("status IN ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)", []string{"queued", "pending", "running"}, time.Now()).Order("updated_at,id").Limit(limit).Find(&operations).Error
	return operations, err
}

func (r *Repository) ClaimAgentToolOperation(id, owner string) (bool, error) {
	result := r.db.Model(&model.AgentToolOperation{}).Where("id = ? AND status IN ? AND (lease_expires_at IS NULL OR lease_expires_at < ?)", id, []string{"queued", "pending", "running"}, time.Now()).Updates(map[string]any{"status": "running", "lease_owner": owner, "lease_expires_at": time.Now().Add(45 * time.Second)})
	return result.RowsAffected == 1, result.Error
}

func (r *Repository) RenewAgentToolOperation(id, owner string) error {
	result := r.db.Model(&model.AgentToolOperation{}).Where("id = ? AND lease_owner = ? AND lease_expires_at > ?", id, owner, time.Now()).Update("lease_expires_at", time.Now().Add(45*time.Second))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}

func (r *Repository) FinishAgentToolOperation(operation model.AgentToolOperation, owner, status, message string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// A pending tool is retried only by durable recovery, not by the Agent's HTTP waiter.
		expiry := any(nil)
		if status == "pending" {
			expiry = time.Now().Add(5 * time.Second)
		}
		result := tx.Model(&model.AgentToolOperation{}).Where("id = ? AND lease_owner = ? AND lease_expires_at > ?", operation.ID, owner, time.Now()).Updates(map[string]any{"status": status, "error": message, "lease_owner": "", "lease_expires_at": expiry})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrCreationConflict
		}
		return model.AppendAgentWake(tx, model.AgentWakeEvent{RunID: operation.RunID, UserID: operation.UserID, TaskID: operation.TaskID, Kind: "tool_changed"})
	})
}
