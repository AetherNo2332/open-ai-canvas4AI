package app

import (
	"yingce/backend/internal/model"
	"yingce/backend/internal/taskruntime"
)

// 任务计费的状态迁移与核对策略归 internal/taskruntime 所有；这里保留既有
// 调用点的方法名，把动作转发给 taskruntime.BillingCoordinator。

type taskBillingRepository = taskruntime.BillingRepository

var (
	// errTaskBillingReview 表示订单状态已经不能由重试流程安全推断，必须先完成费用核对。
	errTaskBillingReview = taskruntime.ErrBillingReview
)

type taskBillingCoordinator struct {
	runtime *taskruntime.BillingCoordinator
}

func newTaskBillingCoordinator(repo taskBillingRepository) *taskBillingCoordinator {
	return &taskBillingCoordinator{runtime: taskruntime.NewBillingCoordinator(repo)}
}

func (c *taskBillingCoordinator) MarkBillingRunning(orderID string) error {
	return c.runtime.MarkBillingRunning(orderID)
}

func (c *taskBillingCoordinator) SettleBilling(orderID string, providerRequestID string) error {
	return c.runtime.SettleBilling(orderID, providerRequestID)
}

func (c *taskBillingCoordinator) BillingOrder(orderID string) (*model.BillingOrder, error) {
	return c.runtime.BillingOrder(orderID)
}

func (c *taskBillingCoordinator) SettleBillingWithAudioDuration(orderID string, providerRequestID string, durationMs int64) error {
	return c.runtime.SettleBillingWithAudioDuration(orderID, providerRequestID, durationMs)
}

func (c *taskBillingCoordinator) RestoreRefundedBilling(orderID string, providerRequestID string) error {
	return c.runtime.RestoreRefundedBilling(orderID, providerRequestID)
}

func (c *taskBillingCoordinator) RefundBilling(orderID string, errorText string) error {
	return c.runtime.RefundBilling(orderID, errorText)
}

func (c *taskBillingCoordinator) MarkBillingUncertain(orderID string, errorText string) error {
	return c.runtime.MarkBillingUncertain(orderID, errorText)
}

func (c *taskBillingCoordinator) CheckRetryEligibility(orderID string) error {
	return c.runtime.CheckRetryEligibility(orderID)
}

func (c *taskBillingCoordinator) BillingFailureRequiresReview(orderID string, taskID string, err error) bool {
	return c.runtime.BillingFailureRequiresReview(orderID, taskID, err)
}
