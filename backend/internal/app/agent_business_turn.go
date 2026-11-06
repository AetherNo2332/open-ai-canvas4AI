package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"gorm.io/gorm"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// businessTurnMaxSteps bounds the model/tool loop of one turn.
const businessTurnMaxSteps = 6

// businessTurn is one user (or supervisor) turn on an executor Session.
type businessTurn struct {
	Assignment AssignmentView
	MessageID  string
	Text       string
	Model      cp.ModelSelection
}

type businessCallResult struct {
	Call   cp.ToolCall
	Result as.ToolResult
}

type businessTurnResult struct {
	Text  string
	Calls []businessCallResult
}

// advanceBusinessTurn moves a turn forward as far as it can without waiting:
// it records the input, submits the next model step, and when a step has
// finished, executes its tool calls and records their results. Every step key
// is the turn plus the step number and every tool result is keyed by its call
// ID, so calling it again after a crash resumes without a second model call,
// tool execution or charge. done is true once the model answered with text.
func (s *Service) advanceBusinessTurn(ownerID string, turn businessTurn) (businessTurnResult, bool, error) {
	session, err := s.agentSessionForOwner(ownerID, turn.Assignment.ExecutorSessionID)
	if err != nil {
		return businessTurnResult{}, false, err
	}
	if err := s.fenceAgentTurn(session); err != nil {
		return businessTurnResult{}, false, err
	}
	turnKey := "turn:" + session.ID + ":" + strings.TrimSpace(turn.MessageID)
	lease, err := s.acquireAgentTurnLease(ownerID, session.ID, agentSessionID(ownerID, turnKey))
	if err != nil {
		return businessTurnResult{}, false, err
	}
	run := businessTurnRun{s: s, ownerID: ownerID, session: session, turn: turn, turnKey: turnKey, lease: lease}
	result, done, err := run.advance()
	if done || err != nil {
		_ = s.releaseAgentSessionWriter(ownerID, session.ID, lease)
	}
	return result, done, err
}

type businessTurnRun struct {
	s       *Service
	ownerID string
	session *model.AgentSession
	turn    businessTurn
	turnKey string
	lease   as.Lease
	result  businessTurnResult
}

func (r *businessTurnRun) append(messages ...as.HistoryMessage) error {
	current, err := r.s.agentSessionForOwner(r.ownerID, r.session.ID)
	if err != nil {
		return err
	}
	_, err = r.s.appendAgentSessionMessages(r.ownerID, r.session.ID, r.lease, current.Revision, messages)
	return err
}

func (r *businessTurnRun) advance() (businessTurnResult, bool, error) {
	if err := r.append(as.HistoryMessage{ID: r.turn.MessageID, Kind: as.MessageKindMessage, Actor: as.ActorUser, Role: cp.RoleUser, Text: r.turn.Text}); err != nil {
		return r.result, false, err
	}
	for step := 1; step <= businessTurnMaxSteps; step++ {
		stepKey := r.turnKey + ":step:" + itoaStep(step)
		finished, calls, text, err := r.modelStep(step, stepKey)
		if err != nil || !finished {
			return r.result, false, err
		}
		if len(calls) == 0 {
			r.result.Text = text
			if err := r.append(as.HistoryMessage{ID: stepKey + ":reply", Kind: as.MessageKindMessage, Actor: as.ActorExecutor, Role: cp.RoleAssistant, Text: text}); err != nil {
				return r.result, false, err
			}
			return r.result, true, nil
		}
		if err := r.runTools(step, stepKey, calls); err != nil {
			return r.result, false, err
		}
	}
	return r.result, true, as.NewError(as.CodeSessionConflict, "本轮工具调用次数已达上限")
}

// modelStep submits the step once and afterwards reads the task it created.
// The step's input is frozen at its first submit: later resumes never rebuild
// it, so they cannot submit different content under the same key. The tools
// come from the turn's frozen contract, not from the live registry.
func (r *businessTurnRun) modelStep(step int, stepKey string) (bool, []cp.ToolCall, string, error) {
	taskID, err := r.s.structuredStepTask(r.ownerID, agentBillingApplication(r.session.Application), stepKey)
	if err != nil {
		return false, nil, "", err
	}
	if taskID == "" {
		snapshot, err := r.s.buildAgentSessionContext(r.ownerID, r.session.ID)
		if err != nil {
			return false, nil, "", err
		}
		tools, err := r.s.agentTurnTools(r.ownerID, r.turnKey, r.session)
		if err != nil {
			return false, nil, "", err
		}
		req := agentStepRequest(snapshot, r.turn.Model, stepKey)
		req.ToolChoice, req.Tools, req.Messages = cp.ToolChoiceAuto, tools, businessStepMessages(snapshot)
		receipt, err := r.s.SubmitStructuredText(context.Background(), cp.Caller{UserID: r.ownerID}, req, r.turn.Assignment.BudgetGrantID)
		if err != nil {
			return false, nil, "", err
		}
		taskID = receipt.Snapshot.TaskID
	}
	result, state, err := r.s.StructuredResult(context.Background(), cp.Caller{UserID: r.ownerID}, taskID)
	if err != nil {
		return false, nil, "", err
	}
	switch state.Execution {
	case cp.ExecutionSucceeded:
		return true, result.ToolCalls, result.Text, nil
	case cp.ExecutionFailed, cp.ExecutionCancelled:
		return false, nil, "", as.NewError(as.CodeInternal, "模型步失败")
	}
	return false, nil, "", nil
}

// runTools records the calls, executes each one under its operation receipt,
// and records the result keyed by the operation, so a provider call ID reused
// in a later turn or step cannot alias an earlier result. The conversation
// blocks carry the server call identity, not the provider's.
func (r *businessTurnRun) runTools(step int, stepKey string, calls []cp.ToolCall) error {
	serverCalls := make([]cp.ToolCall, len(calls))
	for index, call := range calls {
		serverCalls[index] = call
		serverCalls[index].ID = serverAgentCallID(agentOperationKey(r.turnKey, step, call.ID))
	}
	encoded, _ := json.Marshal(serverCalls)
	if err := r.append(as.HistoryMessage{ID: stepKey + ":calls", Kind: as.MessageKindToolCall, Actor: as.ActorExecutor, Role: cp.RoleAssistant, Text: string(encoded)}); err != nil {
		return err
	}
	for index, call := range calls {
		operationKey := agentOperationKey(r.turnKey, step, call.ID)
		result, err := r.s.executeBusinessTool(context.Background(), as.ToolInvocation{OwnerID: r.ownerID, SessionID: r.session.ID,
			Scope:      as.Scope{Application: r.session.Application, SpaceType: r.session.SpaceType, SpaceID: r.session.SpaceID},
			WorkItemID: r.session.WorkItemID, BudgetGrantID: r.turn.Assignment.BudgetGrantID, CallID: call.ID, Name: call.Name, Arguments: call.Arguments,
			TurnMessageID: r.turn.MessageID, Step: step, OperationID: operationKey,
			AssignmentID: r.turn.Assignment.ID, AssignmentEpoch: r.turn.Assignment.Epoch})
		if err != nil {
			if !isAgentToolRefusal(err) {
				// An infrastructure failure is not a refusal; the turn fails and
				// the pending receipt keeps the attempt on record.
				return err
			}
			// The model sees the refusal as the tool result; nothing was executed.
			result = as.ToolResult{Text: "工具调用被拒绝：" + err.Error()}
		}
		body, _ := json.Marshal(map[string]any{"toolCallId": serverCalls[index].ID, "result": result})
		if err := r.append(as.HistoryMessage{ID: agentToolReceiptMessageID(operationKey), Kind: as.MessageKindToolResult, Actor: as.ActorSystem, Role: cp.RoleTool, Text: string(body)}); err != nil {
			return err
		}
		r.result.Calls = append(r.result.Calls, businessCallResult{Call: call, Result: result})
	}
	return nil
}

// businessStepMessages turns the snapshot's history into structured messages,
// keeping tool calls and tool results as such.
func businessStepMessages(snapshot as.ContextSnapshot) []cp.StructuredMessage {
	messages := []cp.StructuredMessage{}
	for _, block := range snapshot.Blocks {
		switch block.Kind {
		case as.BlockHistory, as.BlockSummary:
			role := cp.RoleUser
			if block.Role == cp.RoleAssistant {
				role = cp.RoleAssistant
			}
			messages = append(messages, cp.StructuredMessage{Role: role, Text: block.Text})
		case as.BlockToolCall:
			var calls []cp.ToolCall
			if json.Unmarshal([]byte(block.Text), &calls) == nil && len(calls) > 0 {
				messages = append(messages, cp.StructuredMessage{Role: cp.RoleAssistant, ToolCalls: calls})
			}
		case as.BlockToolResult:
			var body struct {
				ToolCallID string        `json:"toolCallId"`
				Result     as.ToolResult `json:"result"`
			}
			if json.Unmarshal([]byte(block.Text), &body) == nil {
				messages = append(messages, cp.StructuredMessage{Role: cp.RoleTool, ToolCallID: body.ToolCallID, Text: body.Result.Text})
			}
		}
	}
	return messages
}

func (s *Service) businessToolDefinitions(application string) []cp.ToolDefinition {
	adapter, ok := s.agentAdapters().Adapter(application)
	if !ok {
		return nil
	}
	tools := []cp.ToolDefinition{}
	for _, spec := range adapter.Tools() {
		tools = append(tools, cp.ToolDefinition{Name: spec.Name, Description: spec.Description, Parameters: spec.Parameters})
	}
	return tools
}

func itoaStep(step int) string {
	return string(rune('0' + step))
}

// structuredStepTask returns the task already submitted for a step key, or "".
func (s *Service) structuredStepTask(ownerID, application, key string) (string, error) {
	submission, err := s.repo.PlatformSubmissionByKey(ownerID, application, key)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return submission.TaskID, nil
}
