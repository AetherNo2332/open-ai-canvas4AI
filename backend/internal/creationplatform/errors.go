package creationplatform

import "errors"

// ErrorCode 是稳定的机器可读平台错误；调用方据此分支，展示文案只给用户看。
type ErrorCode string

const (
	CodeInvalidRequest       ErrorCode = "invalid_request"
	CodeUnauthorized         ErrorCode = "unauthorized"
	CodeForbidden            ErrorCode = "forbidden"
	CodeNotFound             ErrorCode = "not_found"
	CodeModelUnavailable     ErrorCode = "model_unavailable"
	CodeModelNotSupported    ErrorCode = "model_not_supported"
	CodeReferenceUnavailable ErrorCode = "reference_unavailable"
	CodeInsufficientCredits  ErrorCode = "insufficient_credits"
	CodeCostLimitExceeded    ErrorCode = "cost_limit_exceeded"
	CodeQuoteExpired         ErrorCode = "quote_expired"
	CodeIdempotencyConflict  ErrorCode = "idempotency_conflict"
	CodeActiveTaskLimit      ErrorCode = "active_task_limit"
	CodeActionNotAllowed     ErrorCode = "action_not_allowed"
	CodeBillingPendingReview ErrorCode = "billing_pending_review"
	CodeMediaDeliveryFailed  ErrorCode = "media_delivery_failed"
	CodeUnavailable          ErrorCode = "unavailable"
	CodeInternal             ErrorCode = "internal"
	// 预算授权（BudgetGrant）错误：额度不足与授权已关闭分开，调用方据此决定申请增额还是停止。
	CodeBudgetExceeded ErrorCode = "budget_exceeded"
	CodeBudgetClosed   ErrorCode = "budget_closed"
)

// Error 是平台对外的统一错误。Reason 对齐后端信封的 reason 取值，便于 HTTP 适配复用。
type Error struct {
	Code      ErrorCode `json:"code"`
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
	TaskID    string    `json:"taskId,omitempty"`
	RequestID string    `json:"requestId,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

var errorDefaults = map[ErrorCode]struct {
	reason    string
	status    int
	retryable bool
}{
	CodeInvalidRequest:       {"invalid_argument", 400, false},
	CodeUnauthorized:         {"unauthorized", 401, false},
	CodeForbidden:            {"forbidden", 403, false},
	CodeNotFound:             {"not_found", 404, false},
	CodeModelUnavailable:     {"invalid_model_selection", 400, false},
	CodeModelNotSupported:    {"model_capability_not_supported", 400, false},
	CodeReferenceUnavailable: {"failed_precondition", 400, false},
	CodeInsufficientCredits:  {"quota_exceeded", 403, false},
	CodeCostLimitExceeded:    {"failed_precondition", 400, false},
	CodeQuoteExpired:         {"conflict", 409, false},
	CodeIdempotencyConflict:  {"conflict", 409, false},
	CodeActiveTaskLimit:      {"rate_limited", 429, true},
	CodeActionNotAllowed:     {"failed_precondition", 409, false},
	CodeBillingPendingReview: {"failed_precondition", 409, false},
	CodeMediaDeliveryFailed:  {"failed_precondition", 409, true},
	CodeUnavailable:          {"unavailable", 503, true},
	CodeInternal:             {"internal", 500, true},
	CodeBudgetExceeded:       {"quota_exceeded", 403, false},
	CodeBudgetClosed:         {"failed_precondition", 409, false},
}

// NewError 用默认 reason 与可重试标记构造错误。
func NewError(code ErrorCode, message string) *Error {
	defaults, ok := errorDefaults[code]
	if !ok {
		defaults = errorDefaults[CodeInternal]
	}
	return &Error{Code: code, Reason: defaults.reason, Message: message, Retryable: defaults.retryable}
}

// HTTPStatus 是 HTTP 适配层使用的状态码。
func (e *Error) HTTPStatus() int {
	if defaults, ok := errorDefaults[e.Code]; ok {
		return defaults.status
	}
	return 500
}

// AsError 判断任意错误是否已经是平台错误。
func AsError(err error) (*Error, bool) {
	var platformErr *Error
	if errors.As(err, &platformErr) {
		return platformErr, true
	}
	return nil, false
}

// IsCode reports whether err is a platform error with the given code.
func IsCode(err error, code ErrorCode) bool {
	platformErr, ok := AsError(err)
	return ok && platformErr.Code == code
}
