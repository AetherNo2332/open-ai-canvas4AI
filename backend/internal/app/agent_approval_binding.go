package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"gorm.io/gorm"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
)

// agentApprovalThresholdMicrocredits is the product policy confirmed for B1:
// one charged operation at or above this quote needs the user's approval
// before it executes; below it, the operation runs inside the granted budget.
const agentApprovalThresholdMicrocredits int64 = 100_000

// agentApprovalCallIdentity binds an approval to exactly one execution: the
// server call identity of the operation, or a hash of the direct call when no
// operation context exists.
func agentApprovalCallIdentity(call as.ToolInvocation) string {
	if call.OperationID != "" {
		return serverAgentCallID(call.OperationID)
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{call.OwnerID, call.SessionID, call.CallID, string(call.Arguments)}, "\x00")))
	return "call_" + hex.EncodeToString(sum[:24])
}

// gateAgentToolApproval runs the approval gate of one charged tool execution.
// A quote below the work's threshold passes; at or above it, the gate creates
// (or finds) the approval bound to this operation, its arguments, quote,
// target version and the work's authorization version, and only lets the
// execution continue once that approval is approved and consumed once.
func (s *Service) gateAgentToolApproval(call as.ToolInvocation, quote int64, targetVersion string) error {
	if call.WorkItemID == "" || quote <= 0 {
		return nil
	}
	work, err := s.repo.AgentWorkForOwner(call.OwnerID, call.WorkItemID)
	if err != nil {
		return workNotFound(err)
	}
	auth, err := ao.DecodeAuthorization(work.AuthorizationJSON)
	if err != nil {
		return err
	}
	if auth.ApprovalThresholdMicrocredits <= 0 || quote < auth.ApprovalThresholdMicrocredits {
		return nil
	}
	identity := agentApprovalCallIdentity(call)
	view, err := s.RequestApproval(call.OwnerID, ApprovalRequest{WorkItemID: work.ID, SessionID: call.SessionID, CallID: identity,
		QuoteMicrocredits: quote, TargetVersion: targetVersion, Summary: "单次支出达到审批阈值", ArgumentsHash: agentArgumentsHash(call.Arguments)})
	if err != nil {
		return err
	}
	if err := s.ConsumeApproval(call.OwnerID, view.ID, quote, targetVersion, identity); err != nil {
		if ao.IsCode(err, ao.CodeForbidden) || ao.IsCode(err, ao.CodeVersionConflict) {
			// Not approved yet, or bound to a moved target: the model sees the
			// refusal and the operation stays pending for a later retry.
			if view.Status == ao.ApprovalRejected {
				return as.NewError(as.CodeForbidden, "该操作已被用户拒绝，请调整方案")
			}
			return as.NewError(as.CodeForbidden, "该操作报价达到审批阈值，等待用户批准后继续")
		}
		return err
	}
	return nil
}

// agentWorkAuthorizationLive reports whether a work tree's budget grant is
// still open and its work still live. The drama scheduler's bound dispatch
// checks it before every charged submission; a missing record counts as
// stopped so nothing new is charged.
func (s *Service) agentWorkAuthorizationLive(ownerID, grantID string) (bool, error) {
	grant, err := budgetGrantHost{s: s}.grant(ownerID, strings.TrimSpace(grantID))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || cp.IsCode(err, cp.CodeNotFound) {
			return false, nil
		}
		return false, err
	}
	if grant.Status != cp.BudgetStatusOpen {
		return false, nil
	}
	work, err := s.repo.AgentWorkForOwner(ownerID, grant.ObjectID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return !ao.WorkTerminal(work.Status), nil
}

