// Package agentorchestration holds the deterministic rules of supervised work:
// work states, delegation guards, and how a report changes the current work.
// Models never write work state; the supervisor only calls these commands.
package agentorchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

const (
	WorkPending     = "pending"
	WorkRunning     = "running"
	WorkWaitingUser = "waiting_user"
	WorkPaused      = "paused"
	WorkCompleted   = "completed"
	WorkFailed      = "failed"
	WorkCancelled   = "cancelled"

	AssignmentPending = "pending"
	AssignmentRunning = "running"
	AssignmentEnded   = "ended"

	ReportProgress  = "progress"
	ReportNeedsUser = "needs_user"
	ReportCompleted = "completed"
	ReportFailed    = "failed"
)

type ErrorCode string

const (
	CodeInvalidRequest  ErrorCode = "invalid_request"
	CodeNotFound        ErrorCode = "not_found"
	CodeForbidden       ErrorCode = "forbidden"
	CodeWorkStopped     ErrorCode = "work_stopped"
	CodeStaleAssignment ErrorCode = "stale_assignment"
	CodeVersionConflict ErrorCode = "version_conflict"
	CodeBudgetExceeded  ErrorCode = "budget_exceeded"
)

type Error struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func NewError(code ErrorCode, message string) *Error { return &Error{Code: code, Message: message} }

func IsCode(err error, code ErrorCode) bool {
	var workErr *Error
	return errors.As(err, &workErr) && workErr.Code == code
}

var workTransitions = map[string][]string{
	WorkPending:     {WorkRunning, WorkPaused, WorkCancelled},
	WorkRunning:     {WorkWaitingUser, WorkPaused, WorkCompleted, WorkFailed, WorkCancelled},
	WorkWaitingUser: {WorkRunning, WorkCancelled},
	WorkPaused:      {WorkRunning, WorkCancelled},
}

// WorkTerminal reports whether work can never change again.
func WorkTerminal(status string) bool {
	return status == WorkCompleted || status == WorkFailed || status == WorkCancelled
}

// CheckWorkTransition allows only the listed moves; terminal work has none.
func CheckWorkTransition(from, to string) error {
	for _, next := range workTransitions[from] {
		if next == to {
			return nil
		}
	}
	if WorkTerminal(from) {
		return NewError(CodeWorkStopped, "工作已结束，不能再改变状态")
	}
	return NewError(CodeInvalidRequest, "工作状态不允许该变化")
}

// CheckDelegation allows new assignments only on live, unpaused work: a stop
// or pause made by the user fences every queued or replayed delegation.
func CheckDelegation(status string) error {
	if WorkTerminal(status) || status == WorkPaused {
		return NewError(CodeWorkStopped, "工作已停止或暂停，不能再派工")
	}
	return nil
}

// DelegationKey identifies one delegation: owner + source message + action
// key. A replay of the same supervisor step maps to the same assignment.
func DelegationKey(ownerID, sourceMessageID, actionKey string) string {
	sum := sha256.Sum256([]byte(ownerID + "\x00" + sourceMessageID + "\x00" + actionKey))
	return hex.EncodeToString(sum[:16])
}

// Assignment is the part of an assignment a report is checked against.
type Assignment struct {
	ID     string
	Epoch  int64
	Status string
}

// Report is what an executor submits.
type Report struct {
	AssignmentID string
	LeaseEpoch   int64
	WorkVersion  int64
	Status       string
}

// ReportEffect is how an accepted report changes the work.
type ReportEffect struct {
	WorkStatus    string // empty: unchanged
	EndAssignment bool
	EndReason     string
}

// ApplyReport accepts a report only from the current assignment at its current
// epoch for the current work version. Anything older is stale: it may be
// stored for audit but never changes the work.
func ApplyReport(current Assignment, workVersion int64, report Report) (ReportEffect, error) {
	if report.AssignmentID != current.ID || report.LeaseEpoch != current.Epoch || report.WorkVersion != workVersion || current.Status == AssignmentEnded {
		return ReportEffect{}, NewError(CodeStaleAssignment, "报告来自已被替代的派工或旧版本工作")
	}
	switch report.Status {
	case ReportProgress:
		return ReportEffect{}, nil
	case ReportNeedsUser:
		return ReportEffect{WorkStatus: WorkWaitingUser}, nil
	case ReportCompleted:
		return ReportEffect{WorkStatus: WorkCompleted, EndAssignment: true, EndReason: "succeeded"}, nil
	case ReportFailed:
		return ReportEffect{WorkStatus: WorkFailed, EndAssignment: true, EndReason: "failed"}, nil
	}
	return ReportEffect{}, NewError(CodeInvalidRequest, "报告状态无效")
}
