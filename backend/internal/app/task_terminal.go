package app

import (
	"yingce/backend/internal/model"
	"yingce/backend/internal/taskruntime"
)

// 任务终态的业务策略归 internal/taskruntime 所有；这里保留既有调用点的
// 方法名，用显式 adapter 把 Service 的文本回放、日志、产物登记与能力归类
// 接入 taskruntime.TerminalCoordinator，协调器不感知完整 Service。

type taskTerminalRepository = taskruntime.TerminalRepository
type taskBillingLifecycle = taskruntime.BillingLifecycle
type taskReplayLifecycle = taskruntime.ReplayLifecycle
type taskLifecycleLogger = taskruntime.LifecycleLogger
type taskOutputLifecycle = taskruntime.OutputLifecycle

type audioDurationTaskBilling = taskruntime.AudioDurationBilling
type billingOrderReader = taskruntime.BillingOrderReader
type audioDurationTaskOutput = taskruntime.AudioDurationOutput
type videoProcessingTaskSettlement = taskruntime.VideoProcessingSettlement

type taskTerminalCoordinator struct {
	runtime *taskruntime.TerminalCoordinator
}

// serviceRuntimeAdapter 把 Service 的私有回放/日志/产物能力接到 taskruntime 的导出接口。
type serviceRuntimeAdapter struct{ s *Service }

func (a serviceRuntimeAdapter) FinalizeTaskTextReplay(taskID string, status model.TaskStatus) error {
	return a.s.finalizeTaskTextReplay(taskID, status)
}

func (a serviceRuntimeAdapter) Log(userID string, taskID string, level string, message string, payload string) error {
	return a.s.log(userID, taskID, level, message, payload)
}

func (a serviceRuntimeAdapter) RegisterTaskOutputFromTask(task model.Task) error {
	return a.s.RegisterTaskOutputFromTask(task)
}

func (a serviceRuntimeAdapter) AudioOutputDurationMs(task model.Task) (int64, error) {
	return a.s.AudioOutputDurationMs(task)
}

func (a serviceRuntimeAdapter) SettleVideoProcessingBilling(task model.Task, providerRequestID string) (bool, error) {
	return a.s.settleVideoProcessingBilling(task, providerRequestID)
}

func newTaskTerminalCoordinator(s *Service) *taskTerminalCoordinator {
	adapter := serviceRuntimeAdapter{s: s}
	return &taskTerminalCoordinator{runtime: taskruntime.NewTerminalCoordinator(
		s.repo, s.taskBilling(), adapter, adapter, adapter,
		s.UserFacingErrorMessage, s.ensureFailedProviderAttemptLogged, capabilityFromTaskType, providerRejectedSubmission,
	)}
}

func (s *Service) terminalCoordinator() *taskTerminalCoordinator {
	if s.taskTerminalCoordinator != nil {
		return s.taskTerminalCoordinator
	}
	// 部分单元测试直接构造 Service 字面量；延迟创建保持这些测试和内部工具兼容。
	return newTaskTerminalCoordinator(s)
}

func (c *taskTerminalCoordinator) markPreparationFailure(task *model.Task, stage string, err error, billingUncertain bool, refundReason string) error {
	return c.runtime.MarkPreparationFailure(task, stage, err, billingUncertain, refundReason)
}

func (c *taskTerminalCoordinator) handleExecutionFailure(task *model.Task, err error, providerSucceeded bool, channelSlotFailedBeforeRequest bool) error {
	return c.runtime.HandleExecutionFailure(task, err, providerSucceeded, channelSlotFailedBeforeRequest)
}

func (c *taskTerminalCoordinator) handleCancelledResult(task model.Task) error {
	return c.runtime.HandleCancelledResult(task)
}

func (c *taskTerminalCoordinator) handleResultPersistenceFailure(task *model.Task, saveErr error) (bool, error) {
	return c.runtime.HandleResultPersistenceFailure(task, saveErr)
}

func (c *taskTerminalCoordinator) handleSuccess(task *model.Task) error {
	return c.runtime.HandleSuccess(task)
}

func (c *taskTerminalCoordinator) handleSuccessWithSettlement(task *model.Task, settle func() error) error {
	return c.runtime.HandleSuccessWithSettlement(task, settle)
}

func (c *taskTerminalCoordinator) handleAlreadyCancelled(task model.Task) error {
	return c.runtime.HandleAlreadyCancelled(task)
}
