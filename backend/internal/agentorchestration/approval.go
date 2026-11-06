package agentorchestration

const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
	ApprovalConsumed = "consumed"
	ApprovalExpired  = "expired"
)

// Approval is the state an approval decision is checked against. It binds the
// work authorization version, the quoted cost and the target object version
// at the time it was requested. Operation is the server identity of the one
// execution the approval belongs to.
type Approval struct {
	Status               string
	Operation            string
	AuthorizationVersion int64
	QuoteMicrocredits    int64
	TargetVersion        string
}

// DecideApproval returns the next status. A decision is final: repeating it
// is a no-op, reversing it is a conflict. If the caller decided on an old
// view, the work moved on or stopped, the approval expires instead.
func DecideApproval(current Approval, decision string, callerVersion, workVersion int64, workStatus string) (string, error) {
	if decision != ApprovalApproved && decision != ApprovalRejected {
		return current.Status, NewError(CodeInvalidRequest, "审批决定无效")
	}
	switch current.Status {
	case ApprovalPending:
	case ApprovalConsumed:
		if decision == ApprovalApproved {
			return ApprovalConsumed, nil
		}
		return current.Status, NewError(CodeVersionConflict, "审批已执行，不能改为拒绝")
	case decision:
		return current.Status, nil
	default:
		return current.Status, NewError(CodeVersionConflict, "审批已有不同的决定")
	}
	if WorkTerminal(workStatus) {
		return ApprovalExpired, NewError(CodeWorkStopped, "工作已停止，审批失效")
	}
	if callerVersion != current.AuthorizationVersion || workVersion != current.AuthorizationVersion {
		return ApprovalExpired, NewError(CodeVersionConflict, "工作授权已变化，审批失效，请重新发起")
	}
	return decision, nil
}

// ConsumeApproval lets exactly one execution use an approved approval, and
// only while the quote, target object and work version still match. The
// consuming execution identifies itself by operation: a retry of the same
// operation after a crash consumes idempotently, a different operation never
// touches the approval.
func ConsumeApproval(current Approval, operation string, quote int64, targetVersion string, workVersion int64, workStatus string) (string, error) {
	if WorkTerminal(workStatus) {
		return ApprovalExpired, NewError(CodeWorkStopped, "工作已停止，审批失效")
	}
	if quote != current.QuoteMicrocredits || targetVersion != current.TargetVersion || workVersion != current.AuthorizationVersion {
		return ApprovalExpired, NewError(CodeVersionConflict, "报价、目标或授权已变化，审批失效")
	}
	switch current.Status {
	case ApprovalApproved:
	case ApprovalConsumed:
		if operation != "" && operation == current.Operation {
			return ApprovalConsumed, nil
		}
		return current.Status, NewError(CodeVersionConflict, "审批已执行过一次")
	default:
		return current.Status, NewError(CodeForbidden, "审批未通过")
	}
	return ApprovalConsumed, nil
}
