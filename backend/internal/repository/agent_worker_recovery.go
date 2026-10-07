package repository

import (
	"fmt"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
	"time"
)

// MutateCloudAgentControl can terminate a run even if its transcript is damaged.
// Only fresh append-only events are accepted; existing messages remain untouched.
func (r *Repository) MutateCloudAgentControl(userID, id string, revision int64, fn func(*model.CloudAgentExecution, *Repository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		update := tx.Model(&model.CloudAgentExecution{}).Where("id = ? AND user_id = ? AND revision = ?", id, userID, revision).
			UpdateColumn("revision", gorm.Expr("revision + 1"))
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrCreationConflict
		}
		repo := New(tx)
		run, err := repo.CloudAgentControlRow(userID, id)
		if err != nil {
			return err
		}
		events := run.EventCount
		if err = fn(run, repo); err != nil {
			return err
		}
		if len(run.Transcript) != 0 || run.EventCount != events+len(run.Journal) {
			return fmt.Errorf("invalid control event append")
		}
		for index, event := range run.Journal {
			if event.RunID != id || event.UserID != userID || event.Sequence != events+index+1 {
				return fmt.Errorf("invalid control event identity")
			}
		}
		if isTerminalCloudAgentRunStatus(run.Status) {
			run.RuntimePhase = "terminal"
			run.WaitKind = ""
			run.WaitID = ""
			run.WaitReason = ""
		}
		if err = tx.Omit("Journal", "Transcript").Save(run).Error; err != nil {
			return err
		}
		for _, event := range run.Journal {
			if err = tx.Create(&event).Error; err != nil {
				return err
			}
		}
		if isTerminalCloudAgentRunStatus(run.Status) {
			return tx.Model(&model.CloudAgentPiSession{}).Where("user_id = ? AND conversation_id = ? AND active_run_id = ?", userID, run.ConversationID, id).
				Updates(map[string]any{"active_run_id": "", "lease_owner": "", "lease_expires_at": nil, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now()}).Error
		}
		return nil
	})
}

func (r *Repository) SetPiRecoveryLease(run *model.CloudAgentExecution, owner string, epoch int64, expires time.Time) error {
	conversation := run.ConversationID
	if conversation == "" {
		conversation = run.ID
	}
	update := r.db.Model(&model.CloudAgentPiSession{}).Where("user_id = ? AND conversation_id = ? AND active_run_id = ? AND lease_owner = ? AND lease_epoch = ?",
		run.UserID, conversation, run.ID, owner, epoch).Updates(map[string]any{"lease_expires_at": expires, "updated_at": time.Now()})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}

func (r *Repository) WorkerRecoveryRuns(after string, limit int) ([]model.CloudAgentExecution, error) {
	var runs []model.CloudAgentExecution
	err := r.db.Where("engine = ? AND status IN ? AND recovery_status IN ?", "pi", []string{"running", "queued"}, []string{"scheduled", "reconciling"}).
		Where("id > ?", after).Order("id").Limit(limit).Find(&runs).Error
	return runs, err
}

// A takeover without progress spends a persisted attempt even when the worker
// died before sending an error report. Explicit release and user waits do not.
func (r *Repository) WorkerRecoveryProtectedWait(run model.CloudAgentExecution) (bool, error) {
	if run.Status == "waiting_approval" || run.WaitKind == "subagents" {
		return true, nil
	}
	resourceTask := run.RuntimePhase == "waiting_resource" && (run.WaitKind == "model" || run.WaitKind == "tool" || run.WaitKind == "compaction")
	if !resourceTask && run.RuntimePhase != "waiting_model" && run.RuntimePhase != "waiting_tool" && run.RuntimePhase != "waiting_compaction" {
		return false, nil
	}
	var count int64
	err := r.db.Model(&model.Task{}).Where("user_id = ? AND id IN ? AND status IN ?", run.UserID, []string{run.ActiveTaskID, run.MediaTaskID, run.WaitID}, []model.TaskStatus{model.TaskStatusRunning, model.TaskStatusQueued}).Count(&count).Error
	return count > 0, err
}

func workerRecoveryClaimUpdates(run model.CloudAgentExecution, now time.Time, protected bool) map[string]any {
	updates := map[string]any{"claim_progress_version": run.ProgressVersion, "next_recovery_at": nil}
	if protected {
		updates["runtime_phase"] = run.RuntimePhase
		updates["wait_kind"] = run.WaitKind
		updates["wait_reason"] = run.WaitReason
		return updates
	}
	if run.RecoveryStatus == "scheduled" {
		updates["recovery_status"] = "reconciling"
		return updates
	}
	if run.LeaseOwner != "" && run.LeaseExpiresAt != nil && !run.LeaseExpiresAt.After(now) &&
		run.ProgressVersion == run.ClaimProgressVersion && run.Status != "waiting_approval" && run.WaitKind != "subagents" {
		updates["recovery_attempts"] = run.RecoveryAttempts + 1
		updates["recovery_status"] = "reconciling"
		updates["recovery_class"] = "worker_lost"
		updates["last_error_reason"] = "worker_lost"
		if run.RecoveryStartedAt == nil {
			updates["recovery_started_at"] = now
		}
	} else if run.RecoveryStatus != "" {
		updates["recovery_status"] = run.RecoveryStatus
	}
	return updates
}
