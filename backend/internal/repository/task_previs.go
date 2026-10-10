package repository

import (
	"infinite-canvas/backend/internal/model"
	"time"
)

// Artifact facts commit before canvas writeback and remain recoverable on failure.
func (r *Repository) SavePrevisCheckpoint(task *model.Task, resultJSON string) error {
	updated := taskLeaseWriter(r.db.Model(&model.Task{}), task.LeaseOwner).Where("id = ? AND status = ? AND type = ?", task.ID, model.TaskStatusRunning, model.TaskTypePrevisRender).Updates(map[string]any{"result_json": resultJSON, "updated_at": time.Now()})
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrTaskStateConflict
	}
	return nil
}

func (r *Repository) PendingPrevisForRun(userID, runID string) ([]model.Task, error) {
	var tasks []model.Task
	err := r.db.Where("user_id = ? AND agent_run_id = ? AND type = ? AND status IN ?", userID, runID, model.TaskTypePrevisRender, []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusRunning}).Find(&tasks).Error
	return tasks, err
}
