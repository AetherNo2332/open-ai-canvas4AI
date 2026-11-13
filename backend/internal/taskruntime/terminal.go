package taskruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// TerminalRepository 是终态协调器所需的窄持久化合同。
type TerminalRepository interface {
	Task(id string) (*model.Task, error)
	UpdateTaskTerminalDiagnostic(task *model.Task, completedAt time.Time) (bool, error)
}

// BillingLifecycle 是终态收尾依赖的计费动作。
type BillingLifecycle interface {
	MarkBillingUncertain(orderID string, errorText string) error
	RefundBilling(orderID string, errorText string) error
	BillingFailureRequiresReview(orderID string, taskID string, err error) bool
	SettleBilling(orderID string, providerRequestID string) error
}

// ReplayLifecycle 收尾文本回放草稿；app 通过显式 adapter 实现。
type ReplayLifecycle interface {
	FinalizeTaskTextReplay(taskID string, status model.TaskStatus) error
}

// LifecycleLogger 写任务日志；app 通过显式 adapter 实现。
type LifecycleLogger interface {
	Log(userID string, taskID string, level string, message string, payload string) error
}

// OutputLifecycle 登记任务产物。
type OutputLifecycle interface {
	RegisterTaskOutputFromTask(task model.Task) error
}

// AudioDurationBilling 支持按输出时长结算的计费实现（可选探测）。
type AudioDurationBilling interface {
	SettleBillingWithAudioDuration(orderID string, providerRequestID string, durationMs int64) error
}

// BillingOrderReader 读取计费订单（可选探测）。
type BillingOrderReader interface {
	BillingOrder(orderID string) (*model.BillingOrder, error)
}

// AudioDurationOutput 读取音频产物时长（可选探测）。
type AudioDurationOutput interface {
	AudioOutputDurationMs(task model.Task) (int64, error)
}

// VideoProcessingSettlement 处理视频处理类订单的按秒结算（可选探测）。
type VideoProcessingSettlement interface {
	SettleVideoProcessingBilling(task model.Task, providerRequestID string) (bool, error)
}

// LeaseReleaser 释放任务资源租约（可选探测；stub repository 可以不实现）。
type LeaseReleaser interface {
	ReleaseCloudAgentResourceLeases(userID string, scope string) error
}

// TerminalCoordinator 收敛任务进入终态后的业务策略。
//
// 任务执行本身仍由 Service 编排，但失败、取消和成功收尾必须保持一致，
// 不能散落在 provider/worker 分支中。文本回放、日志、产物登记与能力归类
// 通过构造参数注入；app 组合根用显式 adapter 接入。
type TerminalCoordinator struct {
	repo              TerminalRepository
	billing           BillingLifecycle
	replay            ReplayLifecycle
	logger            LifecycleLogger
	outputs           OutputLifecycle
	userFacingMessage func(error) string
	logFailedAttempt  func(model.Task, error)
	capabilityOf      func(taskType string) string
	rejectedSubmiss   func(error) bool
}

// NewTerminalCoordinator 组合终态协调器；capabilityOf 与 rejectedSubmission
// 分别注入能力归类和上游拒收判断（依赖调用方的稳定错误事实）。
func NewTerminalCoordinator(repo TerminalRepository, billing BillingLifecycle, replay ReplayLifecycle, logger LifecycleLogger, outputs OutputLifecycle, userFacingMessage func(error) string, logFailedAttempt func(model.Task, error), capabilityOf func(string) string, rejectedSubmission func(error) bool) *TerminalCoordinator {
	return &TerminalCoordinator{repo: repo, billing: billing, replay: replay, logger: logger, outputs: outputs, userFacingMessage: userFacingMessage, logFailedAttempt: logFailedAttempt, capabilityOf: capabilityOf, rejectedSubmiss: rejectedSubmission}
}

func (c *TerminalCoordinator) capability(taskType string) string {
	if c.capabilityOf == nil {
		return ""
	}
	return c.capabilityOf(taskType)
}

func (c *TerminalCoordinator) rejected(err error) bool {
	if c.rejectedSubmiss == nil {
		return false
	}
	return c.rejectedSubmiss(err)
}

// MarkPreparationFailure 收尾准备阶段失败：写终态与诊断，并按费用状态退款或进入待核对。
func (c *TerminalCoordinator) MarkPreparationFailure(task *model.Task, stage string, err error, billingUncertain bool, refundReason string) error {
	c.ensureFailedAttemptLogged(task, err)
	task.Status = model.TaskStatusFailed
	task.Stage = stage
	task.Error = c.userFacingMessage(err)
	outcome, retryClass := "not_submitted", "correct_input"
	if billingUncertain {
		outcome, retryClass = "unknown", "reconcile_submission"
	}
	RecordTaskDiagnostic(task, "preparation", "preparation_failed", outcome, retryClass, err)
	if terminalErr := c.markTerminalState(task); terminalErr != nil {
		return errors.Join(err, terminalErr)
	}
	var billingErr error
	if billingUncertain {
		billingErr = c.billing.MarkBillingUncertain(task.BillingOrderID, task.Error)
	} else {
		billingErr = c.billing.RefundBilling(task.BillingOrderID, refundReason)
	}
	if billingErr != nil {
		return errors.Join(err, fmt.Errorf("任务计费收尾失败：%w", billingErr))
	}
	return err
}

// HandleExecutionFailure 返回 nil 仅表示取消已被正常收尾；普通失败仍返回原始错误，
// 让 worker 保留重试/监控所需的失败语义。
func (c *TerminalCoordinator) HandleExecutionFailure(task *model.Task, err error, providerSucceeded bool, channelSlotFailedBeforeRequest bool) error {
	if errors.Is(err, context.Canceled) {
		// 用户取消会先把数据库任务置为 cancelled，再停止 worker context。
		// 此时不再重复退款/核对，只补齐 worker 侧的回放和日志收尾。
		if latest, latestErr := c.repo.Task(task.ID); latestErr == nil && latest.Status == model.TaskStatusCancelled {
			return c.handleAlreadyCancelled(*latest)
		}
		task.Status = model.TaskStatusCancelled
		task.Stage = "任务已取消"
		task.Error = "任务已取消"
		now := time.Now()
		task.CancellationSource, task.CancellationRequestedAt = model.TaskCancellationWorkerContext, &now
		RecordTaskDiagnostic(task, "cancellation", "task_cancelled", TaskSubmissionOutcome(task, channelSlotFailedBeforeRequest), "reconcile_cancellation", err)
		if terminalErr := c.markTerminalState(task); terminalErr != nil {
			return terminalErr
		}
		var billingErr error
		if channelSlotFailedBeforeRequest {
			billingErr = c.billing.RefundBilling(task.BillingOrderID, "等待渠道槽位期间取消，上游请求未发出")
		} else {
			billingErr = c.billing.MarkBillingUncertain(task.BillingOrderID, "任务取消时上游费用状态不明确")
		}
		c.finalizeReplay(task, model.TaskStatusCancelled, "文本回放草稿归并失败")
		_ = c.logger.Log(task.UserID, task.ID, "warn", "任务已取消", "")
		var resultErr error
		if billingErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("任务取消后的计费收尾失败：%w", billingErr))
		}
		return resultErr
	}

	task.Status = model.TaskStatusFailed
	c.ensureFailedAttemptLogged(task, err)
	task.Stage = "任务失败"
	task.Error = c.userFacingMessage(err)
	outcome := TaskSubmissionOutcome(task, channelSlotFailedBeforeRequest)
	if providerSucceeded {
		outcome = "accepted"
	} else if outcome == "unknown" && c.rejected(err) {
		// 上游明确拒绝（参数、鉴权、余额、模型不存在等 4xx）：请求已被读取并拒收，没有建立任务，
		// 不是“接单状态未知”。按未接单处理，不把分镜行或节点长期锁在待核对上。
		outcome = "rejected"
	}
	retryClass := "new_generation_authorization"
	if outcome == "unknown" {
		retryClass = "reconcile_submission"
	}
	RecordTaskDiagnostic(task, "execution", "provider_execution_failed", outcome, retryClass, err)
	if terminalErr := c.markTerminalState(task); terminalErr != nil {
		return errors.Join(err, terminalErr)
	}
	c.finalizeReplay(task, model.TaskStatusFailed, "文本回放草稿归并失败")
	var billingErr error
	if providerSucceeded || (!channelSlotFailedBeforeRequest && c.billing.BillingFailureRequiresReview(task.BillingOrderID, task.ID, err)) {
		billingErr = c.billing.MarkBillingUncertain(task.BillingOrderID, task.Error)
	} else {
		billingErr = c.billing.RefundBilling(task.BillingOrderID, task.Error)
	}
	_ = c.logger.Log(task.UserID, task.ID, "error", "任务处理失败", task.Error)
	resultErr := err
	if billingErr != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("任务计费收尾失败：%w", billingErr))
	}
	return resultErr
}

func (c *TerminalCoordinator) handleAlreadyCancelled(task model.Task) error {
	c.finalizeReplay(&task, model.TaskStatusCancelled, "文本回放草稿归并失败")
	_ = c.logger.Log(task.UserID, task.ID, "warn", "任务已取消，worker 已停止执行", "")
	return nil
}

// HandleAlreadyCancelled 收尾已被并发取消的任务：只补回放与日志，不再触碰计费。
func (c *TerminalCoordinator) HandleAlreadyCancelled(task model.Task) error {
	return c.handleAlreadyCancelled(task)
}

// HandleCancelledResult 丢弃已生成的结果并进入待核对。
func (c *TerminalCoordinator) HandleCancelledResult(task model.Task) error {
	c.finalizeReplay(&task, model.TaskStatusCancelled, "文本回放草稿归并失败")
	billingErr := c.billing.MarkBillingUncertain(task.BillingOrderID, "上游已返回结果，但任务被取消")
	_ = c.logger.Log(task.UserID, task.ID, "warn", "任务已取消，丢弃生成结果", "")
	var resultErr error
	if billingErr != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("丢弃已生成结果后的计费收尾失败：%w", billingErr))
	}
	return resultErr
}

// HandleResultPersistenceFailure 处理“上游成功但本地结果保存失败”的收尾。
// 返回 handled=true 表示并发取消已被识别并正常收尾，调用方不应再返回保存错误。
func (c *TerminalCoordinator) HandleResultPersistenceFailure(task *model.Task, saveErr error) (handled bool, err error) {
	if errors.Is(saveErr, repository.ErrTaskStateConflict) {
		latest, latestErr := c.repo.Task(task.ID)
		if latestErr == nil && latest.Status == model.TaskStatusCancelled {
			if billingErr := c.HandleCancelledResult(*latest); billingErr != nil {
				return true, billingErr
			}
			return true, nil
		}
	}

	task.Status = model.TaskStatusFailed
	task.Stage = "任务结果保存失败"
	task.Error = c.userFacingMessage(saveErr)
	RecordTaskDiagnostic(task, "result_persistence", "result_persistence_failed", "accepted", "recover_result", saveErr)
	if terminalErr := c.markTerminalState(task); terminalErr != nil {
		return false, errors.Join(saveErr, terminalErr)
	}
	c.finalizeReplay(task, model.TaskStatusFailed, "文本回放草稿归并失败")
	billingErr := c.billing.MarkBillingUncertain(task.BillingOrderID, "上游已成功但任务结果未保存："+task.Error)
	_ = c.logger.Log(task.UserID, task.ID, "error", "任务结果保存失败", task.Error)
	resultErr := saveErr
	if billingErr != nil {
		resultErr = errors.Join(resultErr, fmt.Errorf("任务结果保存失败后的计费收尾失败：%w", billingErr))
	}
	return false, resultErr
}

func (c *TerminalCoordinator) ensureFailedAttemptLogged(task *model.Task, err error) {
	if c.logFailedAttempt == nil || task == nil || err == nil {
		return
	}
	c.logFailedAttempt(*task, err)
}

// HandleSuccess 收尾成功任务：登记产物并按常规路径结算。
func (c *TerminalCoordinator) HandleSuccess(task *model.Task) error {
	return c.HandleSuccessWithSettlement(task, nil)
}

// HandleSuccessWithSettlement 与 HandleSuccess 相同；settle 非 nil 时替换常规结算，
// 用于订单已退款后再交付作品的场景。
func (c *TerminalCoordinator) HandleSuccessWithSettlement(task *model.Task, settle func() error) error {
	c.finalizeReplay(task, model.TaskStatusSucceeded, "文本回放窗口更新失败")
	var completionErr error
	completedTask, fetchErr := c.repo.Task(task.ID)
	if fetchErr != nil {
		completionErr = fmt.Errorf("任务成功后读取任务产物失败：%w", fetchErr)
		_ = c.logger.Log(task.UserID, task.ID, "error", "任务成功但读取任务产物失败", fetchErr.Error())
	} else if completedTask.MediaRecoveryJSON == "" {
		if registerErr := c.outputs.RegisterTaskOutputFromTask(*completedTask); registerErr != nil {
			// 任务成功与产物登记分开记账；登记失败保持步骤异常，允许项目页幂等补登记。
			_ = c.logger.Log(task.UserID, task.ID, "error", "任务成功但项目产物登记失败", registerErr.Error())
			completionErr = fmt.Errorf("任务成功后的项目产物登记失败：%w", registerErr)
		}
	}
	settleBilling := func() error {
		// 视频处理（画质增强等）按秒订单按实际输出时长结算；拿不到时长时返回错误进入待核对。
		if processing, ok := c.outputs.(VideoProcessingSettlement); ok && completedTask != nil && c.capability(task.Type) == "video" {
			if handled, err := processing.SettleVideoProcessingBilling(*completedTask, ""); handled || err != nil {
				return err
			}
		}
		if audioBilling, ok := c.billing.(AudioDurationBilling); ok && c.capability(task.Type) == "audio" {
			orderReader, readerOK := c.billing.(BillingOrderReader)
			if readerOK {
				order, err := orderReader.BillingOrder(task.BillingOrderID)
				if err != nil {
					return err
				}
				// Only audio orders priced per second need measured output duration.
				// Fixed-request audio must retain the ordinary settlement path even
				// when the stored resource has no duration metadata.
				if order == nil || order.Capability != "audio" || order.BillingMode != "per_second" {
					return c.billing.SettleBilling(task.BillingOrderID, "")
				}
			}
			output, outputOK := c.outputs.(AudioDurationOutput)
			if outputOK {
				if completedTask == nil {
					return errors.New("任务成功后无法读取音频产物")
				}
				durationMs, err := output.AudioOutputDurationMs(*completedTask)
				if err != nil {
					return err
				}
				return audioBilling.SettleBillingWithAudioDuration(task.BillingOrderID, "", durationMs)
			}
		}
		return c.billing.SettleBilling(task.BillingOrderID, "")
	}
	if settle != nil {
		settleBilling = settle
	}
	if err := settleBilling(); err != nil {
		uncertainErr := c.billing.MarkBillingUncertain(task.BillingOrderID, "生成成功但积分结算失败："+err.Error())
		_ = c.logger.Log(task.UserID, task.ID, "error", "积分结算失败，已进入待核对", err.Error())
		completionErr = errors.Join(completionErr, fmt.Errorf("积分结算失败：%w", err))
		if uncertainErr != nil {
			completionErr = errors.Join(completionErr, fmt.Errorf("记录计费待核对状态失败：%w", uncertainErr))
		}
	}
	_ = c.logger.Log(task.UserID, task.ID, "info", "任务完成，结果已持久化", "")
	return completionErr
}

func (c *TerminalCoordinator) markTerminalState(task *model.Task) error {
	completedAt := time.Now()
	task.CompletedAt = &completedAt
	updated, err := c.repo.UpdateTaskTerminalDiagnostic(task, completedAt)
	if err != nil {
		return fmt.Errorf("写入任务终态失败：%w", err)
	}
	if !updated {
		return repository.ErrTaskStateConflict
	}
	if leases, ok := c.repo.(LeaseReleaser); ok && task.ID != "" {
		if err := leases.ReleaseCloudAgentResourceLeases(task.UserID, "task:"+task.ID); err != nil {
			return fmt.Errorf("释放任务资源租约失败：%w", err)
		}
	}
	return nil
}

func (c *TerminalCoordinator) finalizeReplay(task *model.Task, status model.TaskStatus, message string) {
	if err := c.replay.FinalizeTaskTextReplay(task.ID, status); err != nil {
		_ = c.logger.Log(task.UserID, task.ID, "error", message, err.Error())
	}
}
