package repository

import (
	"errors"
	"gorm.io/gorm"
	"strings"
	"time"
	"yingce/backend/internal/model"
)

func (r *Repository) BillingOrder(id string) (*model.BillingOrder, error) {
	var order model.BillingOrder
	if err := r.db.First(&order, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

func (r *Repository) BillingOrdersByIDs(ids []string) (map[string]model.BillingOrder, error) {
	result := make(map[string]model.BillingOrder, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	var orders []model.BillingOrder
	if err := r.db.Where("id IN ?", ids).Find(&orders).Error; err != nil {
		return nil, err
	}
	for _, order := range orders {
		result[order.ID] = order
	}
	return result, nil
}

func (r *Repository) BillingOrdersByTaskIDs(userID string, taskIDs []string) (map[string]model.BillingOrder, error) {
	result := make(map[string]model.BillingOrder, len(taskIDs))
	if len(taskIDs) == 0 {
		return result, nil
	}
	var orders []model.BillingOrder
	if err := r.db.Where("user_id = ? AND task_id IN ?", userID, taskIDs).Find(&orders).Error; err != nil {
		return nil, err
	}
	for _, order := range orders {
		if order.TaskID != "" {
			result[order.TaskID] = order
		}
	}
	return result, nil
}

type BillingAutoRefundCursor struct {
	CreatedAt time.Time
	ID        string
}

// AutoRefundableBillingOrders 只自动退回通用任务已失败或取消、且没有作品交付证据的订单。
// 无通用任务的订单可能属于电商等独立运行域，不能把缺少 tasks 行当作停止执行的证据。
func (r *Repository) AutoRefundableBillingOrders(limit int, before *BillingAutoRefundCursor) ([]BillingAutoRefundCursor, error) {
	var items []BillingAutoRefundCursor
	query := autoRefundableBillingQuery(r.db)
	if before != nil {
		query = query.Where("billing_orders.created_at < ? OR (billing_orders.created_at = ? AND billing_orders.id < ?)", before.CreatedAt, before.CreatedAt, before.ID)
	}
	// 扫描只读取定位字段；退款事务重新检查资格并读取完整订单。
	err := query.Select("billing_orders.id", "billing_orders.created_at").
		Order("billing_orders.created_at desc, billing_orders.id desc").Limit(limit).Find(&items).Error
	return items, err
}

func autoRefundableBillingQuery(db *gorm.DB) *gorm.DB {
	return db.Model(&model.BillingOrder{}).
		Joins("JOIN tasks ON tasks.id = billing_orders.task_id").
		Where("billing_orders.status IN ? OR (billing_orders.status = ? AND billing_orders.updated_at < ?)",
			[]model.BillingStatus{model.BillingStatusUncertain, model.BillingStatusReserved}, model.BillingStatusRunning, time.Now().Add(-40*time.Minute)).
		Where("tasks.status IN ?", []model.TaskStatus{model.TaskStatusFailed, model.TaskStatusCancelled}).
		Where("COALESCE(tasks.media_recovery_json, '') = '' AND COALESCE(tasks.execution_diagnostic_json, '') NOT LIKE ?", `%"code":"result_persistence_failed"%`)
}

func (r *Repository) AdminBillingOrders(status string, keyword string, limit int, offset int) ([]model.BillingOrder, int64, error) {
	var items []model.BillingOrder
	var total int64
	query := r.db.Model(&model.BillingOrder{})
	if status == "review" {
		query = query.Joins("LEFT JOIN tasks ON tasks.id = billing_orders.task_id").Where(
			"billing_orders.status = ? OR (billing_orders.status = ? AND billing_orders.updated_at < ?) OR (billing_orders.status = ? AND tasks.status IN ?)",
			model.BillingStatusUncertain, model.BillingStatusRunning, time.Now().Add(-40*time.Minute), model.BillingStatusReserved,
			[]model.TaskStatus{model.TaskStatusFailed, model.TaskStatusCancelled},
		)
	} else if status != "" && status != "all" {
		query = query.Where("billing_orders.status = ?", status)
	}
	if value := strings.TrimSpace(keyword); value != "" {
		pattern := "%" + strings.ToLower(value) + "%"
		query = query.Joins("LEFT JOIN users ON users.id = billing_orders.user_id").Where(
			"lower(billing_orders.model) LIKE ? OR lower(billing_orders.scene) LIKE ? OR lower(billing_orders.provider_request_id) LIKE ? OR lower(users.username) LIKE ? OR lower(users.display_name) LIKE ?",
			pattern, pattern, pattern, pattern, pattern,
		)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Select("billing_orders.*").Order("billing_orders.created_at desc").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// BillingOrderHasTerminalFailure 只读取该次订单的明确上游失败证据，不借用重试后的任务状态。
func (r *Repository) BillingOrderHasTerminalFailure(orderID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.ApiCallLog{}).
		Where("billing_order_id = ? AND billable = ? AND request_kind = ? AND status = ? AND error_code IN ?", orderID, true, "create", model.ApiCallStatusFailed, []string{"task_failed", "task_cancelled"}).
		Count(&count).Error
	return count > 0, err
}

func (r *Repository) BillingOrderHasSuccessfulBillableCall(orderID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.ApiCallLog{}).
		Where("billing_order_id = ? AND billable = ? AND status = ?", orderID, true, model.ApiCallStatusSucceeded).
		Count(&count).Error
	return count > 0, err
}

func (r *Repository) TaskHasSuccessfulBillableCall(taskID string) (bool, error) {
	var count int64
	err := r.db.Model(&model.ApiCallLog{}).
		Where("task_id = ? AND billable = ? AND status = ?", taskID, true, model.ApiCallStatusSucceeded).
		Count(&count).Error
	return count > 0, err
}

type BillingUsage struct {
	InputTokens  int64
	OutputTokens int64
	CachedTokens int64
	// CacheWriteTokens 已计入 InputTokens。
	CacheWriteTokens int64
}

func (r *Repository) BillingUsage(orderID string) (*BillingUsage, error) {
	return billingUsage(r.db, orderID)
}

func billingUsage(db *gorm.DB, orderID string) (*BillingUsage, error) {
	var log model.ApiCallLog
	// 异步视频的真实 usage 由非计费的轮询请求返回；订单本身已经限定了归属，
	// 结算应读取同订单最新的成功 usage，而不是只看创建请求。
	err := db.Where("billing_order_id = ? AND status = ? AND usage_available = ?", orderID, model.ApiCallStatusSucceeded, true).
		Order("created_at desc").First(&log).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrBillingUsageUnavailable
	}
	if err != nil {
		return nil, err
	}
	return &BillingUsage{InputTokens: log.InputTokens, OutputTokens: log.OutputTokens, CachedTokens: log.CachedTokens, CacheWriteTokens: log.CacheWriteTokens}, nil
}

const (
	billingUsageSourceProvider     = "provider"
	billingUsageSourceVideoFormula = "video_formula"
)

func tokenSettlementUsage(db *gorm.DB, order model.BillingOrder) (*BillingUsage, string, error) {
	// 参考视频不计费的视频订单：上游 usage 含参考视频用量，不能直接计费；
	// 改用提交时只按生成时长固化的公式快照结算。
	if order.Capability == "video" && order.ReferenceVideoCount > 0 && !order.ReferenceVideoBilled && order.VideoFormulaTokens > 0 {
		return &BillingUsage{OutputTokens: order.VideoFormulaTokens}, billingUsageSourceVideoFormula, nil
	}
	usage, err := billingUsage(db, order.ID)
	if err != nil && !errors.Is(err, ErrBillingUsageUnavailable) {
		return nil, "", err
	}
	if order.Capability != "video" {
		return usage, billingUsageSourceProvider, err
	}
	if err == nil && usage.OutputTokens > 0 && usage.InputTokens >= 0 && usage.CachedTokens >= 0 {
		return &BillingUsage{OutputTokens: usage.OutputTokens}, billingUsageSourceProvider, nil
	}
	// 只有提交时明确记录的公式用量可以结算，预授权 Quantity 含余量，不能用作最终用量。
	if order.VideoFormulaTokens > 0 {
		return &BillingUsage{OutputTokens: order.VideoFormulaTokens}, billingUsageSourceVideoFormula, nil
	}
	return usage, billingUsageSourceProvider, err
}
