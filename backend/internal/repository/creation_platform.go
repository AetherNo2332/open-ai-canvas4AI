package repository

import (
	"encoding/json"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
	if err := r.db.Create(submission).Error; err != nil {
		return err
	}
	return insertPlatformSubmittedEvent(r.db, submission, taskID)
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
		Joins("LEFT JOIN billing_orders AS bo ON bo.task_id = t.id").
		Where("ps.user_id = ? AND ps.application = ?", userID, application)
	if projectRef != "" {
		query = query.Where("ps.project_ref = ?", projectRef)
	}
	err := query.Select(
		"COUNT(DISTINCT ps.id) AS task_count, "+
			"COALESCE(SUM(CASE WHEN bo.id = t.billing_order_id AND bo.status NOT IN (?, ?) THEN bo.amount_microcredits ELSE 0 END), 0) AS reserved_microcredits, "+
			"COALESCE(SUM(CASE WHEN bo.status = ? THEN bo.actual_amount_microcredits ELSE 0 END), 0) AS charged_microcredits, "+
			"COALESCE(SUM(bo.refunded_amount_microcredits), 0) AS refunded_microcredits, "+
			"COALESCE(SUM(CASE WHEN bo.status = ? THEN 1 ELSE 0 END), 0) AS pending_review_count",
		model.BillingStatusSettled, model.BillingStatusRefunded, model.BillingStatusSettled, model.BillingStatusUncertain,
	).Scan(&totals).Error
	return totals, err
}

// InsertPlatformSubmission 供已在事务中的调用方（画布命令提交）登记平台记录与提交事件。
func (r *Repository) InsertPlatformSubmission(submission *model.PlatformSubmission) error {
	return insertPlatformSubmission(r, submission, submission.TaskID)
}

// PlatformSettledSubmissionCount 统计某业务操作前缀下、任务已到终态的平台提交数。
// 短剧用它作为作业的提交序号：在途任务不计数（崩溃后重放同一序号），终态任务计数（重试与续作得到新序号）。
func (r *Repository) PlatformSettledSubmissionCount(userID, application, operationPrefix string) (int64, error) {
	var count int64
	err := r.db.Table("platform_submissions AS ps").Joins("JOIN tasks AS t ON t.id = ps.task_id").
		Where("ps.user_id = ? AND ps.application = ? AND ps.operation_id LIKE ? AND t.status IN ?", userID, application, operationPrefix+"%",
			[]model.TaskStatus{model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusCancelled}).
		Count(&count).Error
	return count, err
}

// PlatformPriceSignature 是平台报价签名。系统渠道模型沿用 CreationPriceSignature；逻辑模型对整个逻辑模型签名
// （当前修订、全部启用线路及其渠道模型、渠道与价格档、计费设置），不包含本次随机选中的线路，
// 因此同价多线路下报价与提交选到不同线路不会误判过期，而任一线路的价格或配置变化仍会让签名改变。
func (r *Repository) PlatformPriceSignature(task *model.Task, channelID, modelKey string) (string, error) {
	if task.LogicalModelID == "" {
		return r.CreationPriceSignature(task, channelID, modelKey)
	}
	db := r.db.Clauses(clause.Locking{Strength: "SHARE"}).Session(&gorm.Session{})
	var logical model.LogicalModel
	if err := db.First(&logical, "id = ?", task.LogicalModelID).Error; err != nil {
		return "", err
	}
	var routes []model.LogicalModelRoute
	if err := db.Where("logical_model_revision_id = ? AND enabled = ?", logical.ActiveRevisionID, true).Order("id").Find(&routes).Error; err != nil {
		return "", err
	}
	values := map[string]any{"logical": logical}
	var revision model.LogicalModelRevision
	if err := db.First(&revision, "id = ?", logical.ActiveRevisionID).Error; err != nil {
		return "", err
	}
	values["revision"] = revision
	lines := make([]map[string]any, 0, len(routes))
	for _, route := range routes {
		var cm model.ChannelModel
		if err := db.First(&cm, "id = ?", route.ChannelModelID).Error; err != nil {
			return "", err
		}
		var channel struct {
			ID        string
			Enabled   bool
			UpdatedAt time.Time
		}
		if err := db.Model(&model.ModelChannel{}).Select("id, enabled, updated_at").First(&channel, "id = ?", cm.ChannelID).Error; err != nil {
			return "", err
		}
		var tiers []model.ChannelModelPriceTier
		if err := db.Where("channel_model_id = ?", cm.ID).Order("id").Find(&tiers).Error; err != nil {
			return "", err
		}
		lines = append(lines, map[string]any{"route": route, "channelModel": cm, "channel": channel, "tiers": tiers})
	}
	values["routes"] = lines
	var settings []model.SystemSetting
	if err := db.Where("key IN ?", []string{"credit_policy", "feature_availability"}).Order("key").Find(&settings).Error; err != nil {
		return "", err
	}
	values["pricingSettings"] = settings
	encoded, err := json.Marshal(values)
	return string(encoded), err
}
