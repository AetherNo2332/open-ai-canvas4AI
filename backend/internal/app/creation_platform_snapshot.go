package app

import (
	"encoding/json"
	"strings"

	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// platformSnapshot 把任务行与费用订单投影为合同快照。执行、资源交付与费用分别取自
// tasks.status、media_stage/result_json 与 billing_orders.status，互不推导。
func platformSnapshot(submission model.PlatformSubmission, task model.Task, order *model.BillingOrder) cp.TaskSnapshot {
	var business cp.BusinessRef
	_ = json.Unmarshal([]byte(submission.BusinessJSON), &business)
	snapshot := cp.TaskSnapshot{
		ContractVersion: cp.ContractVersion, TaskID: task.ID, Capability: cp.Capability(submission.Capability),
		Execution: platformExecution(task), Stage: task.Stage, Progress: task.Progress,
		Billing: platformBilling(order, submission), Business: business,
		Actions: []cp.Action{}, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
	result := platformResult(task)
	snapshot.Resources = platformResources(task, snapshot.Execution, result)
	if snapshot.Execution == cp.ExecutionSucceeded {
		snapshot.Result = result
	}
	if task.Status == model.TaskStatusFailed || task.Status == model.TaskStatusCancelled {
		snapshot.Error = platformTaskError(task, snapshot)
	}
	return snapshot
}

func platformExecution(task model.Task) cp.ExecutionState {
	switch task.Status {
	case model.TaskStatusQueued:
		return cp.ExecutionQueued
	case model.TaskStatusRunning:
		// 上游已返回、正在转存作品的任务仍在 running；执行本身已成功，由资源状态表达交付进度。
		if taskMediaDelivering(task) {
			return cp.ExecutionSucceeded
		}
		return cp.ExecutionRunning
	case model.TaskStatusSucceeded:
		return cp.ExecutionSucceeded
	case model.TaskStatusCancelled:
		return cp.ExecutionCancelled
	case model.TaskStatusSubmissionUncertain:
		return cp.ExecutionUncertain
	case model.TaskStatusFailed:
		// 媒体恢复检查点只在上游成功后写入：任务失败是交付失败，不是生成失败。
		if taskMediaDelivering(task) {
			return cp.ExecutionSucceeded
		}
		return cp.ExecutionFailed
	}
	return cp.ExecutionRunning
}

// taskMediaDelivering：检查点在交付成功后仍保留在任务行上，media_stage=completed 才表示交付完成。
func taskMediaDelivering(task model.Task) bool {
	return task.MediaRecoveryJSON != "" && task.MediaStage != "completed"
}

func platformResources(task model.Task, execution cp.ExecutionState, result *cp.GenerationResult) cp.ResourceState {
	switch {
	case taskMediaDelivering(task) && task.Status == model.TaskStatusFailed:
		return cp.ResourcesFailed
	case taskMediaDelivering(task):
		return cp.ResourcesDelivering
	case execution == cp.ExecutionSucceeded && result != nil && len(result.Resources) > 0:
		return cp.ResourcesReady
	}
	return cp.ResourcesNone
}

func platformBilling(order *model.BillingOrder, submission model.PlatformSubmission) cp.BillingSummary {
	summary := cp.BillingSummary{Status: cp.BillingFree}
	if submission.MaxChargeSet {
		limit := submission.MaxChargeMicrocredits
		summary.AuthorizedMaxMicrocredits = &limit
	}
	if order == nil {
		return summary
	}
	summary.ReservedMicrocredits = order.AmountMicrocredits
	summary.RefundedMicrocredits = order.RefundedAmountMicrocredits
	switch order.Status {
	case model.BillingStatusReserved:
		summary.Status = cp.BillingReserved
	case model.BillingStatusRunning:
		summary.Status = cp.BillingRunning
	case model.BillingStatusSettled:
		summary.Status = cp.BillingSettled
		charged := order.ActualAmountMicrocredits
		summary.ChargedMicrocredits = &charged
	case model.BillingStatusRefunded:
		summary.Status = cp.BillingRefunded
	default:
		// uncertain 及未来的未知状态都按待核对处理：实收保持未知，不能填 0。
		summary.Status = cp.BillingPendingReview
	}
	return summary
}

// platformResult 只从 result_json 提取文本与稳定资源事实，不透出 URL、上游请求或受保护配置。
func platformResult(task model.Task) *cp.GenerationResult {
	if strings.TrimSpace(task.ResultJSON) == "" {
		return nil
	}
	var raw any
	if json.Unmarshal([]byte(task.ResultJSON), &raw) != nil {
		return nil
	}
	result := &cp.GenerationResult{Text: taskResultText(task.ResultJSON)}
	seen := map[string]bool{}
	var walk func(any)
	walk = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			if id := strings.TrimSpace(stringValue(item["resourceId"])); id != "" && !seen[id] {
				seen[id] = true
				mime := stringValue(item["mimeType"])
				result.Resources = append(result.Resources, cp.ResourceRef{
					ResourceID: id, Kind: normalizeResourceKind("", mime), MimeType: mime,
					Bytes: int64(intValue(item["bytes"])), Width: intValue(item["width"]), Height: intValue(item["height"]), DurationMs: int64(intValue(item["durationMs"])),
				})
			}
			for _, child := range item {
				walk(child)
			}
		case []any:
			for _, child := range item {
				walk(child)
			}
		}
	}
	walk(raw)
	if result.Text == "" && len(result.Resources) == 0 {
		return nil
	}
	return result
}

func platformTaskError(task model.Task, snapshot cp.TaskSnapshot) *cp.Error {
	code := cp.CodeInternal
	switch {
	case snapshot.Execution == cp.ExecutionCancelled:
		code = cp.CodeActionNotAllowed
	case snapshot.Resources == cp.ResourcesFailed:
		code = cp.CodeMediaDeliveryFailed
	case snapshot.Billing.Status == cp.BillingPendingReview:
		code = cp.CodeBillingPendingReview
	}
	err := cp.NewError(code, sanitizeUserVisibleError(task.Error))
	err.TaskID = task.ID
	if code == cp.CodeInternal {
		err.Retryable = false
	}
	return err
}
