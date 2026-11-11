package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"yingce/backend/internal/generation"
	"yingce/backend/internal/model"
	"yingce/backend/internal/taskruntime"
)

func taskHasAgentOrigin(raw string) bool {
	var input struct {
		Metadata struct {
			Source string `json:"source"`
		} `json:"metadata"`
	}
	return json.Unmarshal([]byte(raw), &input) == nil && input.Metadata.Source == "cloud_agent"
}

// 诊断清洗与记录的归属在 internal/taskruntime；writeback 展示与读取仍留 app。
func safeTaskDiagnosticMessage(message string) string {
	return taskruntime.SafeTaskDiagnosticMessage(message)
}

func recordTaskDiagnostic(task *model.Task, phase, code, outcome, retryClass string, cause error) {
	taskruntime.RecordTaskDiagnostic(task, phase, code, outcome, retryClass, cause)
}

func recordTaskWritebackDiagnostic(task *model.Task, nodeID, outcome, reason string, cause error) {
	if task == nil {
		return
	}
	diagnostic := taskExecutionDiagnostic(task)
	if diagnostic == nil {
		diagnostic = &model.TaskExecutionDiagnostic{
			Code:              "task_terminal",
			Phase:             "completion",
			TaskSubmitted:     true,
			SubmissionOutcome: cloudAgentTaskSubmissionOutcome(task),
			RetryClass:        "none",
			DiagnosticID:      task.ID,
		}
		if task.Status == model.TaskStatusFailed || task.Status == model.TaskStatusCancelled {
			diagnostic.SafeMessage = cloudAgentSafeMediaTaskError(task)
		}
	}
	diagnostic.WritebackOutcome = outcome
	diagnostic.WritebackReason = strings.TrimSpace(reason)
	diagnostic.WritebackNodeID = strings.TrimSpace(nodeID)
	diagnostic.WritebackMessage = ""
	if cause != nil {
		diagnostic.WritebackMessage = safeTaskDiagnosticMessage(cloudAgentSafeToolError(cause))
	}
	raw, _ := json.Marshal(diagnostic)
	task.ExecutionDiagnosticJSON, task.Diagnostic = string(raw), diagnostic
}

// taskExecutionDiagnostic exposes stored execution facts; it never derives a
// failure category from a node's presentation state or the model's explanation.
func taskExecutionDiagnostic(task *model.Task) *model.TaskExecutionDiagnostic {
	if task == nil || task.ExecutionDiagnosticJSON == "" {
		return nil
	}
	var diagnostic model.TaskExecutionDiagnostic
	if json.Unmarshal([]byte(task.ExecutionDiagnosticJSON), &diagnostic) != nil {
		return nil
	}
	// Execution success and writeback failure are separate facts. A successful
	// task has no execution error; writeback errors remain in WritebackMessage.
	if task.Status == model.TaskStatusSucceeded {
		diagnostic.SafeMessage = ""
	} else if diagnostic.SafeMessage != "" || task.Status == model.TaskStatusFailed || task.Status == model.TaskStatusCancelled {
		diagnostic.SafeMessage = safeTaskDiagnosticMessage(diagnostic.SafeMessage)
	}
	return &diagnostic
}

// providerRejectedSubmission 判断上游是否明确拒收了这次提交。408/425/429 与 5xx、524
// 不算：前者可能在排队或已部分处理，后者网关超时时上游可能仍在执行并计费。
func providerRejectedSubmission(err error) bool {
	status := 0
	var upstream providerHTTPError
	var protocol generation.HTTPError
	switch {
	case errors.As(err, &upstream):
		status = upstream.StatusCode
	case errors.As(err, &protocol):
		status = protocol.StatusCode
	default:
		return false
	}
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
		return false
	}
	return status >= 400 && status < 500
}

func taskSubmissionOutcome(task *model.Task, notSent bool) string {
	return taskruntime.TaskSubmissionOutcome(task, notSent)
}
