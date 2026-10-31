package repository

import (
	"encoding/json"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
	"yingce/backend/internal/model"
)

func (r *Repository) ActiveCanvasTaskBindings(owner, canvas, epoch string) ([]model.CanvasTaskBinding, error) {
	rows := []model.CanvasTaskBinding{}
	err := r.db.Model(&model.CanvasTaskBinding{}).Select("canvas_task_bindings.*").Joins("JOIN tasks ON tasks.id = canvas_task_bindings.task_id AND tasks.user_id = canvas_task_bindings.owner_id").Where("canvas_task_bindings.owner_id = ? AND canvas_task_bindings.canvas_id = ? AND canvas_task_bindings.epoch = ? AND tasks.status IN ?", owner, canvas, epoch, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning, model.TaskStatusSubmissionUncertain}).Find(&rows).Error
	return rows, err
}

func (r *Repository) SaveCanvasTaskResource(task, owner, resource string) error {
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.CanvasTaskResource{TaskID: task, OwnerID: owner, ResourceID: resource}).Error
}

// Caller supplies the same command transaction that records owner acknowledgement.
func (r *Repository) CloseCanvasSubmissionUncertainty(owner, id string) error {
	result := r.db.Model(&model.Task{}).Where("id = ? AND user_id = ? AND status = ?", id, owner, model.TaskStatusSubmissionUncertain).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "submission_uncertainty_acknowledged", "stage": "主机已核对上游并结束本地等待", "updated_at": time.Now()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrTaskStateConflict
	}
	if err := r.db.Model(&model.CanvasTaskBinding{}).Where("task_id = ? AND owner_id = ?", id, owner).Update("resolution_version", 1).Error; err != nil {
		return err
	}
	if err := r.EnqueueCanvasMaterialization(id); err != nil {
		return err
	}
	return r.db.Model(&model.CanvasMaterializationJob{}).Where("task_id = ?", id).Updates(map[string]any{"state": "pending", "outcome": "", "next_attempt_at": nil, "last_error": "", "updated_at": time.Now()}).Error
}

func (r *Repository) canvasTerminalStatus(task *model.Task) (model.TaskStatus, error) {
	if task.Status != model.TaskStatusFailed || !r.db.Migrator().HasTable(&model.CanvasTaskBinding{}) {
		return task.Status, nil
	}
	_, err := r.CanvasTaskBinding(task.ID)
	if r.IsMissing(err) {
		return task.Status, nil
	}
	if err != nil {
		return task.Status, err
	}
	var diagnostic model.TaskExecutionDiagnostic
	if json.Unmarshal([]byte(task.ExecutionDiagnosticJSON), &diagnostic) == nil && diagnostic.SubmissionOutcome == "unknown" {
		return model.TaskStatusSubmissionUncertain, nil
	}
	return task.Status, nil
}

func (r *Repository) InsertPreparedCanvasTask(task *model.Task) error { return r.db.Create(task).Error }
func (r *Repository) InsertCanvasTaskBinding(b *model.CanvasTaskBinding) error {
	return r.db.Create(b).Error
}
func (r *Repository) CanvasTaskBinding(taskID string) (*model.CanvasTaskBinding, error) {
	var b model.CanvasTaskBinding
	err := r.db.Where("task_id = ?", taskID).First(&b).Error
	return &b, err
}

func (r *Repository) UpdateCanvasTaskBindingResultContext(taskID, owner, value string) error {
	return r.db.Model(&model.CanvasTaskBinding{}).Where("task_id = ? AND owner_id = ?", taskID, owner).Update("result_context_json", value).Error
}
func (r *Repository) CanvasTaskBindingForOperation(owner, canvas, epoch, actor, client, op string) (*model.CanvasTaskBinding, error) {
	var b model.CanvasTaskBinding
	err := r.db.Where("owner_id = ? AND canvas_id = ? AND epoch = ? AND actor_id = ? AND client_id = ? AND operation_id = ?", owner, canvas, epoch, actor, client, op).First(&b).Error
	return &b, err
}

// This composes with the caller's task terminal transaction. No task binding
// means ordinary non-canvas tasks continue without a materialization job.
func (r *Repository) EnqueueCanvasMaterialization(taskID string) error {
	if !r.db.Migrator().HasTable(&model.CanvasTaskBinding{}) {
		return nil
	}
	b, err := r.CanvasTaskBinding(taskID)
	if r.IsMissing(err) {
		return nil
	}
	if err != nil {
		return err
	}
	now := time.Now()
	job := model.CanvasMaterializationJob{TaskID: taskID, OwnerID: b.OwnerID, State: "pending", CreatedAt: now, UpdatedAt: now}
	if err := r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
		return err
	}
	// A recovered media-save failure reuses its paid task, but needs a fresh
	// materialization receipt. Ordinary duplicate delivery remains a no-op.
	result := r.db.Model(&model.CanvasMaterializationJob{}).Where("task_id = ? AND state = ? AND outcome = ? AND EXISTS (SELECT 1 FROM tasks WHERE tasks.id = ? AND tasks.status = ?)", taskID, "done", "media_save_pending", taskID, model.TaskStatusSucceeded).Updates(map[string]any{"state": "pending", "outcome": "", "next_attempt_at": nil, "last_error": "", "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return r.db.Model(&model.CanvasTaskBinding{}).Where("task_id = ?", taskID).UpdateColumn("resolution_version", gorm.Expr("resolution_version + 1")).Error
	}
	return nil
}

// Reconciles older cancellation/lease-cleanup paths at startup. Successful task
// completion and diagnostic failure already enqueue atomically in their writer.
func (r *Repository) RecoverCanvasMaterializationJobs() error {
	var ids []string
	err := r.db.Table("tasks").Select("tasks.id").Joins("JOIN canvas_task_bindings b ON b.task_id = tasks.id").Joins("LEFT JOIN canvas_materialization_jobs j ON j.task_id = tasks.id").Where("j.task_id IS NULL AND tasks.status IN ?", []model.TaskStatus{model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusCancelled, model.TaskStatusSubmissionUncertain}).Scan(&ids).Error
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = r.EnqueueCanvasMaterialization(id); err != nil {
			return err
		}
	}
	return nil
}
func (r *Repository) PendingCanvasMaterializations(limit int) ([]model.CanvasMaterializationJob, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows := []model.CanvasMaterializationJob{}
	err := r.db.Where("state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)", "pending", time.Now()).Order("created_at").Limit(limit).Find(&rows).Error
	return rows, err
}
func (r *Repository) CanvasMaterialization(taskID string) (*model.CanvasMaterializationJob, error) {
	var j model.CanvasMaterializationJob
	err := r.db.Where("task_id = ?", taskID).First(&j).Error
	return &j, err
}
func (r *Repository) FinishCanvasMaterialization(taskID, outcome string, resolutionVersion int) error {
	return r.db.Model(&model.CanvasMaterializationJob{}).Where("task_id = ? AND state = ? AND EXISTS (SELECT 1 FROM canvas_task_bindings b WHERE b.task_id = ? AND b.resolution_version = ?)", taskID, "pending", taskID, resolutionVersion).Updates(map[string]any{"state": "done", "outcome": outcome, "last_error": "", "next_attempt_at": nil, "updated_at": time.Now()}).Error
}
func (r *Repository) RecordCanvasMaterializationFailure(taskID string) error {
	j, err := r.CanvasMaterialization(taskID)
	if err != nil {
		return err
	}
	// Retry the committed artifact only; capped backoff avoids polling a broken
	// storage/quota binding twice a second. Never persist raw provider/SQL errors.
	shift := j.Attempts
	if shift > 8 {
		shift = 8
	}
	next := time.Now().Add(time.Duration(1<<shift) * time.Second)
	return r.db.Model(&model.CanvasMaterializationJob{}).Where("task_id = ? AND state = ?", taskID, "pending").Updates(map[string]any{"attempts": j.Attempts + 1, "last_error": "作品已生成，写回画布暂未完成；请检查存储限制或重试写回", "next_attempt_at": next, "updated_at": time.Now()}).Error
}
func (r *Repository) CanvasTaskOutputs(taskID string) ([]model.CanvasTaskOutput, error) {
	rows := []model.CanvasTaskOutput{}
	err := r.db.Where("task_id = ?", taskID).Order("output_index").Find(&rows).Error
	return rows, err
}
func (r *Repository) InsertCanvasTaskOutput(o *model.CanvasTaskOutput) error {
	return r.db.Create(o).Error
}
func (r *Repository) InsertCanvasOutputAsset(a *model.Asset) error { return r.db.Create(a).Error }

// Used only inside a command transaction; rechecks limits and actual model
// availability without opening a nested transaction/savepoint.
func (r *Repository) CommitPreparedTask(task *model.Task, order *model.BillingOrder, activeLimit int) error {
	if err := r.requireActiveLogicalModelForTask(r.db, task); err != nil {
		return err
	}
	if err := enforceActiveTaskLimit(r.db, task.UserID, activeLimit); err != nil {
		return err
	}
	if order != nil {
		if err := reserveBillingOrder(r.db, order); err != nil {
			return err
		}
	}
	return r.db.Create(task).Error
}
