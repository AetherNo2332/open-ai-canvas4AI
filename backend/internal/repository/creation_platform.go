package repository

import "yingce/backend/internal/model"

// PlatformSubmissionForTask 只返回属于该用户的平台提交记录。
func (r *Repository) PlatformSubmissionForTask(userID, taskID string) (*model.PlatformSubmission, error) {
	var item model.PlatformSubmission
	if err := r.db.First(&item, "user_id = ? AND task_id = ?", userID, taskID).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// insertPlatformSubmission 在任务写入的同一事务里登记平台提交；唯一约束冲突整体回滚。
func insertPlatformSubmission(r *Repository, submission *model.PlatformSubmission, taskID string) error {
	if submission == nil {
		return nil
	}
	submission.TaskID = taskID
	if submission.ID == "" {
		submission.ID = newRepositoryID()
	}
	return r.db.Create(submission).Error
}
