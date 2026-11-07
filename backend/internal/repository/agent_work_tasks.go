package repository

import "yingce/backend/internal/model"

// UnfinishedWorkTasks reads every charged task under this exact work grant in
// the same transaction as the completion transition; no operation-list limit
// may silently drop a required task.
func (t *WorkTx) UnfinishedWorkTasks(ownerID, grantID string) (int64, error) {
	var count int64
	err := t.db.Model(&model.Task{}).Where("user_id = ? AND status <> ?", ownerID, model.TaskStatusSucceeded).
		Where("id IN (SELECT task_id FROM platform_budget_charges WHERE owner_id = ? AND grant_id = ?)", ownerID, grantID).Count(&count).Error
	return count, err
}
