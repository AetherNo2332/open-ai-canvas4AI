package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// executeGenericPiToolCall routes a non-canvas Pi call through the business
// neutral operation receipt. The adapter receives no authority from the model;
// the binding and Session scope come from the frozen run state.
func (s *Service) executeGenericPiToolCall(run *model.CloudAgentExecution, state *cloudAgentRuntime) error {
	if run == nil || state == nil || state.Execution == nil || state.CallIndex < 0 || state.CallIndex >= len(state.Calls) {
		return errors.New("generic Pi tool state is invalid")
	}
	call := state.Calls[state.CallIndex]
	if !genericPiToolFrozen(state.Execution.Tools, call.Function.Name) {
		return s.recordCloudAgentToolReceipt(run, state, call, nil, as.NewError(as.CodeForbidden, "工具不在本次 Pi 执行合同中"))
	}
	binding := state.Execution.Binding
	invocation := as.ToolInvocation{
		OwnerID: run.UserID, SessionID: state.Execution.SessionID, Scope: state.Execution.Scope,
		CallID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments),
		TurnMessageID: state.Execution.TriggerMessageID, Step: state.Step,
		OperationID: agentOperationKey(run.ID, state.Step, call.ID),
	}
	if binding != nil {
		invocation.WorkItemID, invocation.BudgetGrantID = binding.WorkItemID, binding.BudgetGrantID
		invocation.AssignmentID, invocation.AssignmentEpoch = binding.AssignmentID, binding.AssignmentEpoch
	}
	adapterInvocation := invocation
	adapterInvocation.OperationID = ""
	result, err := s.executeAgentOperation(context.Background(), invocation, func() (as.ToolResult, error) {
		session, sessionErr := s.agentSessionForOwner(run.UserID, state.Execution.SessionID)
		if sessionErr != nil {
			return as.ToolResult{}, sessionErr
		}
		if session.Role == as.RoleSupervisor {
			return s.executeSupervisorTool(context.Background(), session, adapterInvocation)
		}
		return s.executeBusinessTool(context.Background(), adapterInvocation)
	})
	if err != nil {
		if _, ok := as.AsError(err); ok {
			return s.recordCloudAgentToolReceipt(run, state, call, nil, err)
		}
		return err
	}
	return s.recordCloudAgentToolReceipt(run, state, call, result, nil)
}

func genericPiToolFrozen(tools []as.ToolSpec, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func (s *Service) completeGenericPiNoToolTurn(repo *repository.Repository, current *model.CloudAgentExecution, state *cloudAgentRuntime, taskID string, result piNoToolTurnOutput, owner string) error {
	if state.Execution == nil {
		return errors.New("generic Pi execution identity is missing")
	}
	leaseOwner, leaseEpoch, _, err := parsePiAgentLeaseOwner(owner)
	if err != nil {
		return err
	}
	actor := as.ActorExecutor
	if state.Execution.Role == as.RoleSupervisor {
		actor = as.ActorSupervisor
	}
	messageID := "pi:" + current.ID + ":" + taskID
	message := model.AgentSessionMessage{
		ID: agentSessionID(current.UserID, state.Execution.SessionID+"\x00"+messageID), MessageID: messageID,
		Kind: as.MessageKindMessage, Actor: actor, Role: "assistant", Body: result.Text, RunID: current.ID,
	}
	if err := repo.AppendAgentSessionMessageFromPi(current.UserID, state.Execution.SessionID,
		firstNonEmpty(current.ConversationID, current.ID), current.ID, leaseOwner, leaseEpoch, message, time.Now().UTC()); err != nil {
		return err
	}
	state.PiNoToolTaskID = taskID
	state.PiNoToolNudge = ""
	state.event(current.ID, "assistant_message", map[string]any{"messageId": messageID, "text": result.Text, "final": true})
	current.Status = "completed"
	return cloudAgentSave(current, state)
}
