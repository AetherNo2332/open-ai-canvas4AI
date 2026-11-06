package agentservice

import "errors"

// ErrorCode is the stable machine-readable Agent error. It shares the envelope
// shape of the platform contract but is a separate list: Agent codes do not
// widen the creation platform contract.
type ErrorCode string

const (
	CodeInvalidRequest      ErrorCode = "invalid_request"
	CodeUnauthorized        ErrorCode = "unauthorized"
	CodeForbidden           ErrorCode = "forbidden"
	CodeNotFound            ErrorCode = "not_found"
	CodeScopeMismatch       ErrorCode = "scope_mismatch"
	CodeSessionConflict     ErrorCode = "session_conflict"
	CodeStaleLease          ErrorCode = "stale_lease"
	CodeIdempotencyConflict ErrorCode = "idempotency_conflict"
	CodeWorkStopped         ErrorCode = "work_stopped"
	CodeStaleAssignment     ErrorCode = "stale_assignment"
	CodeBudgetExceeded      ErrorCode = "budget_exceeded"
	CodeInternal            ErrorCode = "internal"
)

type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

var errorStatus = map[ErrorCode]int{
	CodeInvalidRequest: 400, CodeUnauthorized: 401, CodeForbidden: 403, CodeNotFound: 404,
	CodeScopeMismatch: 403, CodeSessionConflict: 409, CodeStaleLease: 409, CodeIdempotencyConflict: 409,
	CodeWorkStopped: 409, CodeStaleAssignment: 409, CodeBudgetExceeded: 403, CodeInternal: 500,
}

func NewError(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message, Retryable: code == CodeSessionConflict || code == CodeInternal}
}

// HTTPStatus is used by the HTTP adapter.
func (e *Error) HTTPStatus() int {
	if status, ok := errorStatus[e.Code]; ok {
		return status
	}
	return 500
}

func AsError(err error) (*Error, bool) {
	var agentErr *Error
	if errors.As(err, &agentErr) {
		return agentErr, true
	}
	return nil, false
}

func IsCode(err error, code ErrorCode) bool {
	agentErr, ok := AsError(err)
	return ok && agentErr.Code == code
}
