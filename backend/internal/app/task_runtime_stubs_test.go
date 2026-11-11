package app

import (
	"yingce/backend/internal/model"
	"yingce/backend/internal/taskruntime"
)

// 供 app 侧组合测试复用的计费/终态 stub；行为断言本体已迁 internal/taskruntime，
// 这里只覆盖 app 编排与 taskruntime 协调器的组合路径。

type taskBillingRepositoryStub struct {
	runningCalls      []string
	settleCalls       []string
	restoreCalls      []string
	refundCalls       []string
	uncertainCalls    []string
	order             *model.BillingOrder
	orderErr          error
	hasSuccessfulCall bool
	callErr           error
}

func (r *taskBillingRepositoryStub) MarkBillingRunning(orderID string) error {
	r.runningCalls = append(r.runningCalls, orderID)
	return nil
}

func (r *taskBillingRepositoryStub) SettleBillingOrder(orderID string, providerRequestID string) error {
	r.settleCalls = append(r.settleCalls, orderID+":"+providerRequestID)
	return nil
}

func (r *taskBillingRepositoryStub) RestoreRefundedBillingOrder(orderID string, providerRequestID string) error {
	r.restoreCalls = append(r.restoreCalls, orderID+":"+providerRequestID)
	return nil
}

func (r *taskBillingRepositoryStub) RefundBillingOrder(orderID string, errorText string) error {
	r.refundCalls = append(r.refundCalls, orderID+":"+errorText)
	return nil
}

func (r *taskBillingRepositoryStub) MarkBillingUncertain(orderID string, errorText string) error {
	r.uncertainCalls = append(r.uncertainCalls, orderID+":"+errorText)
	return nil
}

func (r *taskBillingRepositoryStub) BillingOrder(string) (*model.BillingOrder, error) {
	return r.order, r.orderErr
}

func (r *taskBillingRepositoryStub) TaskHasSuccessfulBillableCall(string) (bool, error) {
	return r.hasSuccessfulCall, r.callErr
}

type taskTerminalBillingStub struct {
	uncertain      []string
	refund         []string
	settleCalls    int
	review         bool
	settleError    error
	uncertainError error
	refundError    error
}

func (b *taskTerminalBillingStub) MarkBillingUncertain(_ string, reason string) error {
	b.uncertain = append(b.uncertain, reason)
	return b.uncertainError
}

func (b *taskTerminalBillingStub) RefundBilling(_ string, reason string) error {
	b.refund = append(b.refund, reason)
	return b.refundError
}

func (b *taskTerminalBillingStub) BillingFailureRequiresReview(string, string, error) bool {
	return b.review
}

func (b *taskTerminalBillingStub) SettleBilling(string, string) error {
	b.settleCalls++
	return b.settleError
}

type taskTerminalReplayStub struct {
	statuses []model.TaskStatus
}

func (r *taskTerminalReplayStub) FinalizeTaskTextReplay(_ string, status model.TaskStatus) error {
	r.statuses = append(r.statuses, status)
	return nil
}

type taskTerminalLoggerStub struct {
	messages []string
}

func (l *taskTerminalLoggerStub) Log(_ string, _ string, _ string, message string, _ string) error {
	l.messages = append(l.messages, message)
	return nil
}

// billingFailureUncertain 转发 taskruntime 的分类归属；连接中断类错误必须
// 进入待核对而不是退款。
func billingFailureUncertain(err error) bool {
	return taskruntime.BillingFailureUncertain(err)
}

type taskTerminalOutputStub struct {
	calls int
	err   error
}

func (o *taskTerminalOutputStub) RegisterTaskOutputFromTask(model.Task) error {
	o.calls++
	return o.err
}

// newTaskTerminalCoordinatorForTest 组合 app 编排使用的终态协调器；与生产路径
// 相同的 adapter 接线，只是注入 stub 依赖。
func newTaskTerminalCoordinatorForTest(repo taskTerminalRepository, billing *taskTerminalBillingStub, replay *taskTerminalReplayStub, logger *taskTerminalLoggerStub, outputs *taskTerminalOutputStub) *taskTerminalCoordinator {
	return &taskTerminalCoordinator{runtime: taskruntime.NewTerminalCoordinator(
		repo, billing, replay, logger, outputs,
		func(err error) string { return "public: " + err.Error() }, nil, nil, nil,
	)}
}
