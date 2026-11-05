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

// PlatformSubmissionByKey 按幂等作用域（用户 + application + 幂等键）读取提交记录。
func (r *Repository) PlatformSubmissionByKey(userID, application, key string) (*model.PlatformSubmission, error) {
	var item model.PlatformSubmission
	if err := r.db.First(&item, "user_id = ? AND application = ? AND idempotency_key = ?", userID, application, key).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// PlatformBillingTotals 是按业务归属汇总的费用；冻结、实收、退款与待核对分列。
type PlatformBillingTotals struct {
	TaskCount            int64
	ReservedMicrocredits int64
	ChargedMicrocredits  int64
	RefundedMicrocredits int64
	PendingReviewCount   int64
}

// PlatformBillingTotals 只汇总该用户经平台提交的任务；projectRef 为空时汇总整个 application。
func (r *Repository) PlatformBillingTotals(userID, application, projectRef string) (PlatformBillingTotals, error) {
	var totals PlatformBillingTotals
	query := r.db.Table("platform_submissions AS ps").
		Joins("JOIN tasks AS t ON t.id = ps.task_id").
		Joins("LEFT JOIN billing_orders AS bo ON bo.id = t.billing_order_id").
		Where("ps.user_id = ? AND ps.application = ?", userID, application)
	if projectRef != "" {
		query = query.Where("ps.project_ref = ?", projectRef)
	}
	err := query.Select(
		"COUNT(ps.id) AS task_count, "+
			"COALESCE(SUM(bo.amount_microcredits), 0) AS reserved_microcredits, "+
			"COALESCE(SUM(CASE WHEN bo.status = ? THEN bo.actual_amount_microcredits ELSE 0 END), 0) AS charged_microcredits, "+
			"COALESCE(SUM(bo.refunded_amount_microcredits), 0) AS refunded_microcredits, "+
			"COALESCE(SUM(CASE WHEN bo.status = ? THEN 1 ELSE 0 END), 0) AS pending_review_count",
		model.BillingStatusSettled, model.BillingStatusUncertain,
	).Scan(&totals).Error
	return totals, err
}
