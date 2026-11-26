package repository

import (
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"yingce/backend/internal/model"
)

var (
	ErrAgentGenerationBatchClosed = errors.New("generation batch is no longer collecting")
	ErrAgentGenerationBatchEmpty  = errors.New("generation batch has no registered tasks")
	ErrAgentGenerationBatchState  = errors.New("generation batch state conflict")
)

func (r *Repository) AgentGenerationBatch(userID, id string) (*model.AgentGenerationBatch, error) {
	var row model.AgentGenerationBatch
	err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&row).Error
	return &row, err
}

func (r *Repository) AgentGenerationBatchBySourceRun(userID, sourceRunID string) (*model.AgentGenerationBatch, error) {
	var row model.AgentGenerationBatch
	err := r.db.Where("user_id = ? AND source_run_id = ?", userID, sourceRunID).First(&row).Error
	return &row, err
}

func (r *Repository) AgentGenerationBatchTasks(userID, batchID string) ([]model.AgentGenerationBatchTask, error) {
	rows := []model.AgentGenerationBatchTask{}
	err := r.db.Where("user_id = ? AND batch_id = ?", userID, batchID).Order("created_at, task_id").Find(&rows).Error
	return rows, err
}

func (r *Repository) ReadyAgentGenerationBatches(limit int) ([]model.AgentGenerationBatch, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows := []model.AgentGenerationBatch{}
	err := r.db.Where("status = ?", model.AgentGenerationBatchReady).Order("updated_at, id").Limit(limit).Find(&rows).Error
	return rows, err
}

// EnsureAgentGenerationBatchTask registers exactly the tasks the source run
// actually submitted. The barrier size is derived from inserted rows, never a
// fixed threshold.
func (r *Repository) EnsureAgentGenerationBatchTask(seed model.AgentGenerationBatch, task model.AgentGenerationBatchTask) (*model.AgentGenerationBatch, bool, error) {
	batch, err := r.AgentGenerationBatchBySourceRun(seed.UserID, seed.SourceRunID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		seed.Status = model.AgentGenerationBatchCollecting
		seed.Revision = 1
		seed.ExpectedTasks, seed.TerminalTasks = 0, 0
		seed.CreatedAt, seed.UpdatedAt = time.Now().UTC(), time.Now().UTC()
		if err = r.db.Create(&seed).Error; err != nil {
			return nil, false, err
		}
		batch = &seed
	} else if err != nil {
		return nil, false, err
	}
	if batch.Status != model.AgentGenerationBatchCollecting {
		return batch, false, ErrAgentGenerationBatchClosed
	}
	task.BatchID = batch.ID
	if task.UserID == "" {
		task.UserID = batch.UserID
	}
	task.Status = model.AgentGenerationBatchTaskPending
	task.CreatedAt, task.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	insert := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&task)
	if insert.Error != nil {
		return batch, false, insert.Error
	}
	inserted := insert.RowsAffected == 1
	if !inserted {
		// Re-registration is idempotent and keeps the barrier unchanged.
		return batch, false, nil
	}
	update := r.db.Model(&model.AgentGenerationBatch{}).
		Where("id = ? AND user_id = ? AND revision = ?", batch.ID, batch.UserID, batch.Revision).
		Updates(map[string]any{
			"expected_tasks": gorm.Expr("expected_tasks + 1"),
			"revision":       gorm.Expr("revision + 1"),
			"updated_at":     time.Now().UTC(),
		})
	if update.Error != nil {
		return batch, false, update.Error
	}
	if update.RowsAffected != 1 {
		return batch, false, ErrCreationConflict
	}
	reloaded, err := r.AgentGenerationBatch(batch.UserID, batch.ID)
	return reloaded, true, err
}

func (r *Repository) ArmAgentGenerationBatch(userID, batchID, when, instruction, requestJSON string) (*model.AgentGenerationBatch, error) {
	batch, err := r.AgentGenerationBatch(userID, batchID)
	if err != nil {
		return nil, err
	}
	switch batch.Status {
	case model.AgentGenerationBatchArmed, model.AgentGenerationBatchReady,
		model.AgentGenerationBatchQueued, model.AgentGenerationBatchConsumed:
		return batch, nil
	case model.AgentGenerationBatchDetached:
		return batch, ErrAgentGenerationBatchClosed
	case model.AgentGenerationBatchCollecting:
	default:
		return batch, ErrAgentGenerationBatchState
	}
	if batch.ExpectedTasks == 0 {
		return batch, ErrAgentGenerationBatchEmpty
	}
	status := model.AgentGenerationBatchArmed
	if batch.TerminalTasks == batch.ExpectedTasks {
		status = model.AgentGenerationBatchReady
	}
	update := r.db.Model(&model.AgentGenerationBatch{}).
		Where("id = ? AND user_id = ? AND revision = ? AND status = ?", batch.ID, userID, batch.Revision, model.AgentGenerationBatchCollecting).
		Updates(map[string]any{
			"status": status, "trigger_when": when, "instruction": instruction, "request_json": requestJSON,
			"revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC(),
		})
	if update.Error != nil {
		return nil, update.Error
	}
	if update.RowsAffected != 1 {
		return nil, ErrCreationConflict
	}
	return r.AgentGenerationBatch(userID, batch.ID)
}

func (r *Repository) DetachAgentGenerationBatch(userID, batchID string) error {
	result := r.db.Model(&model.AgentGenerationBatch{}).
		Where("id = ? AND user_id = ? AND status = ?", batchID, userID, model.AgentGenerationBatchCollecting).
		Updates(map[string]any{"status": model.AgentGenerationBatchDetached, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	return result.Error
}

// DetachAgentGenerationBatchBySourceRun closes a still-collecting batch when its
// source run reached a terminal state without registering a continuation. It is
// idempotent and never touches armed/ready/queued/consumed batches, so existing
// results and billing rows stay untouched.
func (r *Repository) DetachAgentGenerationBatchBySourceRun(userID, sourceRunID string) error {
	if sourceRunID == "" {
		return nil
	}
	result := r.db.Model(&model.AgentGenerationBatch{}).
		Where("user_id = ? AND source_run_id = ? AND status = ?", userID, sourceRunID, model.AgentGenerationBatchCollecting).
		Updates(map[string]any{"status": model.AgentGenerationBatchDetached, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	return result.Error
}

// DetachOrphanAgentGenerationBatches repairs batches whose source run is already
// terminal but still collecting. It covers crash windows and the failure,
// cancel and stop paths that finish a run outside finish_run.
func (r *Repository) DetachOrphanAgentGenerationBatches() (int64, error) {
	terminal := []string{"completed", "failed", "cancelled", "rejected"}
	result := r.db.Model(&model.AgentGenerationBatch{}).
		Where("status = ? AND source_run_id IN (?)", model.AgentGenerationBatchCollecting,
			r.db.Model(&model.CloudAgentExecution{}).Select("id").Where("status IN ?", terminal)).
		Updates(map[string]any{"status": model.AgentGenerationBatchDetached, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	return result.RowsAffected, result.Error
}

func (r *Repository) SettleAgentGenerationBatchTask(userID, batchID, taskID string, outcome any) (*model.AgentGenerationBatch, bool, error) {
	batch, err := r.AgentGenerationBatch(userID, batchID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var task model.AgentGenerationBatchTask
	if err = r.db.Where("user_id = ? AND batch_id = ? AND task_id = ?", userID, batchID, taskID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return batch, false, nil
		}
		return nil, false, err
	}
	if task.Status == model.AgentGenerationBatchTaskTerminal {
		return batch, false, nil
	}
	raw, err := json.Marshal(outcome)
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	settleTask := r.db.Model(&model.AgentGenerationBatchTask{}).
		Where("user_id = ? AND batch_id = ? AND task_id = ? AND status = ?", userID, batchID, taskID, model.AgentGenerationBatchTaskPending).
		Updates(map[string]any{"status": model.AgentGenerationBatchTaskTerminal, "outcome_json": string(raw), "updated_at": now})
	if settleTask.Error != nil {
		return nil, false, settleTask.Error
	}
	if settleTask.RowsAffected != 1 {
		current, loadErr := r.AgentGenerationBatch(userID, batchID)
		return current, false, loadErr
	}
	results := map[string]json.RawMessage{}
	if batch.ResultsJSON != "" {
		_ = json.Unmarshal([]byte(batch.ResultsJSON), &results)
	}
	results[taskID] = raw
	encoded, err := json.Marshal(results)
	if err != nil {
		return nil, false, err
	}
	status := batch.Status
	if status == model.AgentGenerationBatchArmed && batch.TerminalTasks+1 >= batch.ExpectedTasks {
		status = model.AgentGenerationBatchReady
	}
	updateBatch := r.db.Model(&model.AgentGenerationBatch{}).
		Where("id = ? AND user_id = ? AND revision = ?", batch.ID, userID, batch.Revision).
		Updates(map[string]any{
			"terminal_tasks": gorm.Expr("terminal_tasks + 1"), "results_json": string(encoded), "status": status,
			"revision": gorm.Expr("revision + 1"), "updated_at": now,
		})
	if updateBatch.Error != nil {
		return nil, false, updateBatch.Error
	}
	if updateBatch.RowsAffected != 1 {
		return nil, false, ErrCreationConflict
	}
	updated, err := r.AgentGenerationBatch(userID, batchID)
	return updated, err == nil, err
}

func (r *Repository) QueueReadyAgentGenerationBatch(userID, batchID, messageID string) (*model.AgentGenerationBatch, error) {
	batch, err := r.AgentGenerationBatch(userID, batchID)
	if err != nil {
		return nil, err
	}
	if batch.Status != model.AgentGenerationBatchReady {
		return batch, nil
	}
	result := r.db.Model(&model.AgentGenerationBatch{}).
		Where("id = ? AND user_id = ? AND revision = ? AND status = ?", batch.ID, userID, batch.Revision, model.AgentGenerationBatchReady).
		Updates(map[string]any{"status": model.AgentGenerationBatchQueued, "queued_message_id": messageID, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return r.AgentGenerationBatch(userID, batchID)
	}
	return r.AgentGenerationBatch(userID, batchID)
}

func (r *Repository) ConsumeAgentGenerationBatchByMessage(userID, messageID, runID string) error {
	var batch model.AgentGenerationBatch
	err := r.db.Where("user_id = ? AND queued_message_id = ? AND status = ?", userID, messageID, model.AgentGenerationBatchQueued).First(&batch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	result := r.db.Model(&model.AgentGenerationBatch{}).
		Where("id = ? AND user_id = ? AND revision = ? AND status = ?", batch.ID, userID, batch.Revision, model.AgentGenerationBatchQueued).
		Updates(map[string]any{"status": model.AgentGenerationBatchConsumed, "continuation_run_id": runID, "revision": gorm.Expr("revision + 1"), "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrCreationConflict
	}
	return nil
}
