package app

import (
	"encoding/json"
	"strings"
	"yingce/backend/internal/canvas/mediaversions"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// cloudAgentTaskFacts is the model-facing projection of durable task state.
// Keep this typed: canvas reads, task_get and the fresh context frame must not
// independently invent string-key contracts for the same execution facts.
type cloudAgentTaskFacts struct {
	TaskID             string                       `json:"taskId"`
	TaskStatus         model.TaskStatus             `json:"taskStatus"`
	TaskType           string                       `json:"taskType,omitempty"`
	Operation          string                       `json:"operation,omitempty"`
	NodeID             string                       `json:"nodeId,omitempty"`
	TaskSubmitted      bool                         `json:"taskSubmitted"`
	SubmissionOutcome  string                       `json:"submissionOutcome"`
	Phase              string                       `json:"phase,omitempty"`
	DiagnosticCode     string                       `json:"diagnosticCode,omitempty"`
	FieldPath          string                       `json:"fieldPath,omitempty"`
	RetryClass         string                       `json:"retryClass,omitempty"`
	Error              string                       `json:"error,omitempty"`
	WritebackOutcome   string                       `json:"writebackOutcome,omitempty"`
	WritebackReason    string                       `json:"writebackReason,omitempty"`
	WritebackNodeID    string                       `json:"writebackNodeId,omitempty"`
	WritebackError     string                       `json:"writebackError,omitempty"`
	CancellationSource string                       `json:"cancellationSource,omitempty"`
	Billing            *cloudAgentTaskBillingFacts  `json:"billing,omitempty"`
}

// cloudAgentTaskBillingFacts 只给模型可读的积分数与结论；订单号、微积分整数和
// 授权上限属于账务内部字段，模型不会把它们传回任何工具，因此不进回执。
type cloudAgentTaskBillingFacts struct {
	Status          model.BillingStatus `json:"status,omitempty"`
	ReservedCredits float64             `json:"reservedCredits"`
	ActualCredits   float64             `json:"actualCredits"`
	RefundedCredits float64             `json:"refundedCredits"`
	ChargeState     string              `json:"chargeState"`
	ChargeSummary   string              `json:"chargeSummary"`
	StateAvailable  bool                `json:"stateAvailable"`
}

func cloudAgentChargeState(status model.BillingStatus) (string, string) {
	switch status {
	case model.BillingStatusSettled:
		return "settled", "已结算，实际扣费以 actualCredits 为准"
	case model.BillingStatusRefunded:
		return "refunded", "已退款，本次不计入已消费"
	case model.BillingStatusReserved, model.BillingStatusRunning:
		return "pending", "仅预授权/执行中，尚未确认实际扣费"
	case model.BillingStatusUncertain:
		return "uncertain", "扣费状态待确认，不得称为已扣费"
	default:
		return "unknown", "扣费状态未知，不得称为已扣费"
	}
}

func cloudAgentTaskDiagnostic(repo *repository.Repository, task *model.Task) map[string]any {
	if task == nil {
		return map[string]any{"taskStatus": "unavailable", "taskSubmitted": false, "submissionOutcome": "unavailable"}
	}
	facts := cloudAgentTaskFacts{
		TaskID: task.ID, TaskStatus: task.Status, TaskType: task.Type, Operation: task.Operation,
		TaskSubmitted: true, SubmissionOutcome: cloudAgentTaskSubmissionOutcome(task),
		CancellationSource: task.CancellationSource,
	}
	if context := taskClientContext(task.InputJSON); context != nil {
		facts.NodeID = context.NodeID
	}
	if diagnostic := taskExecutionDiagnostic(task); diagnostic != nil {
		facts.TaskSubmitted = diagnostic.TaskSubmitted
		facts.SubmissionOutcome = diagnostic.SubmissionOutcome
		facts.Phase = diagnostic.Phase
		facts.DiagnosticCode = diagnostic.Code
		facts.FieldPath = diagnostic.FieldPath
		facts.RetryClass = diagnostic.RetryClass
		facts.Error = diagnostic.SafeMessage
		facts.WritebackOutcome = diagnostic.WritebackOutcome
		facts.WritebackReason = diagnostic.WritebackReason
		facts.WritebackNodeID = diagnostic.WritebackNodeID
		facts.WritebackError = diagnostic.WritebackMessage
	} else if task.Status == model.TaskStatusFailed || task.Status == model.TaskStatusCancelled {
		facts.Error = cloudAgentSafeMediaTaskError(task)
	}
	if task.BillingOrderID != "" {
		facts.Billing = &cloudAgentTaskBillingFacts{}
		if repo != nil {
			if order, err := repo.BillingOrder(task.BillingOrderID); err == nil && order.UserID == task.UserID && (order.TaskID == "" || order.TaskID == task.ID) {
				facts.Billing.Status = order.Status
				facts.Billing.ReservedCredits = float64(order.ReservedAmountMicrocredits) / float64(CreditScale)
				facts.Billing.ActualCredits = float64(order.ActualAmountMicrocredits) / float64(CreditScale)
				facts.Billing.RefundedCredits = float64(order.RefundedAmountMicrocredits) / float64(CreditScale)
				facts.Billing.ChargeState, facts.Billing.ChargeSummary = cloudAgentChargeState(order.Status)
				facts.Billing.StateAvailable = true
			}
		}
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return map[string]any{"taskId": task.ID, "taskStatus": task.Status, "taskSubmitted": true, "submissionOutcome": "unknown"}
	}
	result := map[string]any{}
	if json.Unmarshal(raw, &result) != nil {
		return map[string]any{"taskId": task.ID, "taskStatus": task.Status, "taskSubmitted": true, "submissionOutcome": "unknown"}
	}
	if repo != nil {
		// 节点版本任务：写回后回执给出新版本 ID 与是否已自动采用（决定 3）。
		if binding, err := repo.CanvasTaskBinding(task.ID); err == nil && binding.OwnerID == task.UserID {
			if base, versioned, err := mediaversions.BindingBase(binding); err == nil && versioned {
				result["targetNodeId"] = base.NodeID
				if base.Settled {
					if base.ResultVersionID != "" {
						result["versionId"] = base.ResultVersionID
					}
					result["adopted"] = base.Adopted
				}
				if canvas, err := repo.CanvasProjectForUser(task.UserID, binding.CanvasID); err == nil {
					if doc, err := creationDocument(canvas.PayloadJSON); err == nil {
						nodes, _ := creationObjects(doc["nodes"])
						if current := mediaversions.Current(nodes[base.NodeID]); current != nil {
							result["currentMediaVersionId"] = current["id"]
						}
					}
				}
			}
		}
	}
	return result
}

// cloudAgentTaskSubmissionOutcome 描述"这一步到底有没有送到上游"：
// 有上游任务 ID 才算 accepted；已经跑完的任务必然提交过，只是上游没回传 ID，报 completed；
// 还在排队/执行的按本地状态说是 queued/submitted，其余只能说 unknown。
func cloudAgentTaskSubmissionOutcome(task *model.Task) string {
	if task == nil {
		return "unavailable"
	}
	if strings.TrimSpace(task.ProviderRequestID) != "" {
		return "accepted"
	}
	switch task.Status {
	case model.TaskStatusQueued:
		return "queued"
	case model.TaskStatusRunning:
		return "submitted"
	case model.TaskStatusSucceeded, model.TaskStatusFailed, model.TaskStatusCancelled:
		return "completed"
	default:
		return "unknown"
	}
}
