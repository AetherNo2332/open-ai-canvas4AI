package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Agent tool execution statuses. Only succeeded receipts are replayable; a
// pending row marks an attempt whose outcome is unknown, and refused or
// uncertain rows are audit records that a retry re-evaluates.
const (
	agentOperationPending   = "pending"
	agentOperationSucceeded = "succeeded"
	agentOperationRefused   = "refused"
	agentOperationUncertain = "uncertain"
)

// agentOperationKey is the durable identity of one tool execution: the turn,
// the step and the provider call ID. Provider call IDs may repeat across
// turns; this key never does.
func agentOperationKey(turnKey string, step int, callID string) string {
	return turnKey + ":step:" + itoaStep(step) + ":" + callID
}

// agentToolReceiptMessageID is the Session history ID of a tool receipt. The
// operation key can exceed the history ID limit, so the receipt carries its
// hash; the durable key lives in the operation row.
func agentToolReceiptMessageID(operationKey string) string {
	sum := sha256.Sum256([]byte(operationKey))
	return "tool:" + hex.EncodeToString(sum[:16])
}

// serverAgentCallID is the tool call identity the model conversation sees for
// one execution. Provider call IDs repeat across turns; the conversation
// carries this server identity instead, so history mapping and receipts stay
// unambiguous inside one structured request.
func serverAgentCallID(operationKey string) string {
	sum := sha256.Sum256([]byte(operationKey))
	return "op_" + hex.EncodeToString(sum[:24])
}

func agentArgumentsHash(arguments json.RawMessage) string {
	sum := sha256.Sum256(arguments)
	return hex.EncodeToString(sum[:])
}

// fenceAgentTurn refuses to advance a turn whose work tree has stopped.
// StopWork cancels the queued turns; in-flight ones fail here, and the model
// step already paid for stays an audit fact.
func (s *Service) fenceAgentTurn(session *model.AgentSession) error {
	if session.WorkItemID == "" {
		return nil
	}
	work, err := s.repo.AgentWorkForOwner(session.OwnerID, session.WorkItemID)
	if err != nil {
		return workNotFound(err)
	}
	if ao.WorkTerminal(work.Status) {
		return as.NewError(as.CodeWorkStopped, "工作已停止，轮次不再推进")
	}
	return nil
}

// fenceAgentToolExecution re-checks, for this call, that the work is still
// live and that the executing assignment is still the work's live one. A
// stopped tree or a replaced assignment invalidates the caller's credentials.
func (s *Service) fenceAgentToolExecution(call as.ToolInvocation) error {
	if call.WorkItemID == "" {
		return nil
	}
	work, err := s.repo.AgentWorkForOwner(call.OwnerID, call.WorkItemID)
	if err != nil {
		return workNotFound(err)
	}
	if ao.WorkTerminal(work.Status) {
		return as.NewError(as.CodeWorkStopped, "工作已停止，工具执行被拒绝")
	}
	if call.AssignmentID == "" {
		return nil
	}
	assignment, err := s.repo.AgentAssignmentForOwner(call.OwnerID, call.AssignmentID)
	if err != nil {
		return workNotFound(err)
	}
	if assignment.WorkItemID != work.ID || work.ActiveAssignmentID != assignment.ID || assignment.Status != ao.AssignmentRunning || assignment.Epoch != call.AssignmentEpoch {
		return as.NewError(as.CodeStaleAssignment, "派工已更换，旧执行凭据失效")
	}
	return nil
}

// isAgentToolRefusal reports whether an execution error is a refusal the
// model should see as its tool result; anything else is an infrastructure
// failure that must fail the turn instead of pretending the tool ran.
func isAgentToolRefusal(err error) bool {
	_, ok := as.AsError(err)
	return ok
}

// executeAgentOperation runs one identified tool call with a durable
// receipt: the pending row is written before the adapter runs, the outcome
// afterwards, and a succeeded receipt short-circuits every replay.
func (s *Service) executeAgentOperation(ctx context.Context, call as.ToolInvocation, execute func() (as.ToolResult, error)) (as.ToolResult, error) {
	argumentsHash := agentArgumentsHash(call.Arguments)
	now := time.Now()
	row := &model.AgentOperation{ID: newID(), OwnerID: call.OwnerID, OperationKey: call.OperationID, SessionID: call.SessionID,
		WorkItemID: call.WorkItemID, AssignmentID: call.AssignmentID, AssignmentEpoch: call.AssignmentEpoch,
		TurnMessageID: call.TurnMessageID, Step: call.Step, CallID: call.CallID, Name: call.Name,
		ArgumentsHash: argumentsHash, Status: agentOperationPending, CreatedAt: now, UpdatedAt: now}
	inserted, err := s.repo.InsertAgentOperation(row)
	if err != nil {
		return as.ToolResult{}, err
	}
	if !inserted {
		stored, storedErr := s.repo.AgentOperation(call.OwnerID, call.OperationID)
		if storedErr != nil {
			return as.ToolResult{}, storedErr
		}
		if stored.Status == agentOperationSucceeded {
			return storedAgentToolResult(stored)
		}
		if stored.ArgumentsHash != argumentsHash {
			return as.ToolResult{}, as.NewError(as.CodeIdempotencyConflict, "同一操作标识被用于不同参数")
		}
	}
	result, execErr := execute()
	status, payload := agentOperationOutcome(result, execErr)
	if refErr := s.repo.FinishAgentOperation(call.OwnerID, call.OperationID, status, payload, result.Operation, time.Now()); refErr != nil && inserted && !errors.Is(refErr, repository.ErrAgentOperationFinished) {
		return as.ToolResult{}, refErr
	}
	if execErr != nil && status == agentOperationUncertain {
		// The adapter failed before it could commit or deny: the outcome is
		// unknown, the pending row keeps the attempt on record for audit.
		return as.ToolResult{}, execErr
	}
	return result, execErr
}

func agentOperationOutcome(result as.ToolResult, err error) (string, string) {
	if err == nil {
		payload, _ := json.Marshal(result)
		return agentOperationSucceeded, string(payload)
	}
	if isAgentToolRefusal(err) {
		return agentOperationRefused, ""
	}
	return agentOperationUncertain, ""
}

func storedAgentToolResult(row *model.AgentOperation) (as.ToolResult, error) {
	result := as.ToolResult{}
	if row.ResultJSON == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(row.ResultJSON), &result); err != nil {
		return as.ToolResult{}, as.NewError(as.CodeInternal, "工具回执损坏")
	}
	return result, nil
}

// agentTurnTools returns the tool schemas of one turn. The first model step of
// the turn freezes them together with the definition version; every later step
// and every resume of the same turn key reuses that frozen contract, so an
// instance that redeployed with wider tools cannot change a running turn.
func (s *Service) agentTurnTools(ownerID, turnKey string, session *model.AgentSession) ([]cp.ToolDefinition, error) {
	contract, err := s.repo.AgentTurnContract(ownerID, turnKey)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if err == nil {
		if tools, ok := decodeAgentTurnTools(contract.ToolsJSON); ok {
			return tools, nil
		}
	}
	tools := s.businessToolDefinitions(session.Application)
	if len(tools) == 0 {
		return tools, nil
	}
	raw, err := json.Marshal(tools)
	if err != nil {
		return nil, err
	}
	inserted, err := s.repo.InsertAgentTurnContract(&model.AgentTurnContract{ID: newID(), OwnerID: ownerID, TurnKey: turnKey, SessionID: session.ID,
		DefinitionID: session.DefinitionID, DefinitionVersion: session.DefinitionVersion, ToolsJSON: string(raw), CreatedAt: time.Now()})
	if err != nil {
		return nil, err
	}
	if inserted {
		return tools, nil
	}
	stored, err := s.repo.AgentTurnContract(ownerID, turnKey)
	if err != nil {
		return nil, err
	}
	if tools, ok := decodeAgentTurnTools(stored.ToolsJSON); ok {
		return tools, nil
	}
	return tools, nil
}

func decodeAgentTurnTools(raw string) ([]cp.ToolDefinition, bool) {
	tools := []cp.ToolDefinition{}
	if json.Unmarshal([]byte(raw), &tools) != nil || len(tools) == 0 {
		return nil, false
	}
	return tools, true
}
