package taskruntime

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

// SafeTaskDiagnosticMessage 清洗进入任务诊断的用户可见消息：截断并拦截地址、
// 凭据与内部路径痕迹。原始错误仍保留在服务端日志与请求明细里。
func SafeTaskDiagnosticMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" || !utf8.ValidString(message) || strings.ContainsAny(message, "\r\n\x00") {
		return "任务未完成，请在任务中心查看诊断"
	}
	lower := strings.ToLower(message)
	for _, marker := range []string{"http://", "https://", "file://", "ftp://", "authorization", "cookie", "secret", "token", "api_key", "apikey", "x-api-key", "/var/", "/tmp/", "\\", "stack trace", "traceback", "sql:", "sqlite", "postgres"} {
		if strings.Contains(lower, marker) {
			return "任务未完成，请在任务中心查看诊断"
		}
	}
	return kernel.TruncateRunes(message, 240)
}

// RecordTaskDiagnostic 把一次执行结果写进任务诊断；业务错误码取自 kernel.Reason。
func RecordTaskDiagnostic(task *model.Task, phase, code, outcome, retryClass string, cause error) {
	var appErr *kernel.AppError
	if errors.As(cause, &appErr) && appErr.Reason != "" {
		code = string(appErr.Reason)
	}
	diagnostic := &model.TaskExecutionDiagnostic{
		Code: code, Phase: phase, TaskSubmitted: outcome == "accepted" || outcome == "submitted", SubmissionOutcome: outcome,
		RetryClass: retryClass, SafeMessage: SafeTaskDiagnosticMessage(task.Error), DiagnosticID: task.ID,
	}
	raw, _ := json.Marshal(diagnostic)
	task.ExecutionDiagnosticJSON, task.Diagnostic = string(raw), diagnostic
}

// TaskSubmissionOutcome 归类提交结果：未发出、已被接受或接单状态未知。
func TaskSubmissionOutcome(task *model.Task, notSent bool) string {
	if notSent {
		return "not_submitted"
	}
	if task.ProviderRequestID != "" {
		return "accepted"
	}
	return "unknown"
}
