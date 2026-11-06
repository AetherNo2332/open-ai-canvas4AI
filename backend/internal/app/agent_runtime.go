package app

import (
	"context"
	"strings"

	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
)

// agentRuntimeWorkerPrefix identifies the in-process writer of new-runtime
// turns. The Service worker ID keeps leases distinct across backend instances.
const agentRuntimeWorkerPrefix = "go-runtime"

func (s *Service) agentRuntimeOwner() string {
	return agentRuntimeWorkerPrefix + ":" + s.workerID
}

// agentTurnRequest is one user turn of a non-canvas Session.
type agentTurnRequest struct {
	SessionID     string
	MessageID     string
	Text          string
	Model         cp.ModelSelection
	BudgetGrantID string
}

// agentTurnStep is the durable identity of a turn's model step.
type agentTurnStep struct {
	SessionID string
	MessageID string
	TaskID    string
	Lease     as.Lease
}

// agentTurnDone is the completed turn.
type agentTurnDone struct {
	Text      string
	ToolCalls []cp.ToolCall
}

// startAgentTurn takes the Session writer, records the user message, freezes
// the context and submits the model step. The step's idempotency key is the
// Session plus the user message, so a retry after any crash returns the same
// task instead of a second model call or charge.
func (s *Service) startAgentTurn(ownerID string, turn agentTurnRequest) (agentTurnStep, error) {
	session, err := s.agentSessionForOwner(ownerID, turn.SessionID)
	if err != nil {
		return agentTurnStep{}, err
	}
	if session.ConversationID != "" {
		return agentTurnStep{}, as.NewError(as.CodeScopeMismatch, "画布会话由画布 Agent 执行")
	}
	messageID := strings.TrimSpace(turn.MessageID)
	if messageID == "" || strings.TrimSpace(turn.Text) == "" {
		return agentTurnStep{}, as.NewError(as.CodeInvalidRequest, "消息 ID 与正文必填")
	}
	if turn.BudgetGrantID != "" {
		if _, err := (budgetGrantHost{s: s}).grant(ownerID, turn.BudgetGrantID); err != nil {
			return agentTurnStep{}, err
		}
	}
	key := "turn:" + session.ID + ":" + messageID
	runID := agentSessionID(ownerID, key)
	lease, err := s.acquireAgentTurnLease(ownerID, session.ID, runID)
	if err != nil {
		return agentTurnStep{}, err
	}
	step := agentTurnStep{SessionID: session.ID, MessageID: messageID, Lease: lease}
	release := true
	defer func() {
		if release {
			_ = s.releaseAgentSessionWriter(ownerID, session.ID, lease)
		}
	}()
	current, err := s.agentSessionForOwner(ownerID, session.ID)
	if err != nil {
		return agentTurnStep{}, err
	}
	user := as.HistoryMessage{ID: messageID, Kind: as.MessageKindMessage, Actor: as.ActorUser, Role: cp.RoleUser, Text: turn.Text}
	if _, err := s.appendAgentSessionMessages(ownerID, session.ID, lease, current.Revision, []as.HistoryMessage{user}); err != nil {
		return agentTurnStep{}, err
	}
	snapshot, err := s.buildAgentSessionContext(ownerID, session.ID)
	if err != nil {
		return agentTurnStep{}, err
	}
	req := agentStepRequest(snapshot, turn.Model, key)
	receipt, err := s.SubmitStructuredText(context.Background(), cp.Caller{UserID: ownerID}, req, turn.BudgetGrantID)
	if err != nil {
		return agentTurnStep{}, err
	}
	step.TaskID = receipt.Snapshot.TaskID
	release = false
	return step, nil
}

// acquireAgentTurnLease reuses the lease of the same turn after a crash, or
// takes a new one. Another live writer is a session_conflict.
func (s *Service) acquireAgentTurnLease(ownerID, sessionID, runID string) (as.Lease, error) {
	session, err := s.agentSessionForOwner(ownerID, sessionID)
	if err != nil {
		return as.Lease{}, err
	}
	if session.ActiveRunID == runID && session.LeaseOwner == s.agentRuntimeOwner() {
		return agentSessionLease(*session), nil
	}
	return s.acquireAgentSessionWriter(ownerID, sessionID, s.agentRuntimeOwner(), runID)
}

// agentStepRequest turns a frozen context into the structured model request.
// History blocks become messages; the rest becomes the system context.
func agentStepRequest(snapshot as.ContextSnapshot, selection cp.ModelSelection, key string) cp.StructuredTextRequest {
	req := cp.StructuredTextRequest{
		Model: selection, System: as.RenderText(snapshot), ToolChoice: cp.ToolChoiceNone, IdempotencyKey: key,
		Business: cp.BusinessRef{Application: agentBillingApplication(snapshot.Scope.Application), ProjectID: snapshot.Scope.SpaceID, ObjectType: "agent_session", ObjectID: snapshot.SessionID, OperationID: key},
	}
	for _, block := range snapshot.Blocks {
		if block.Kind != as.BlockHistory && block.Kind != as.BlockSummary {
			continue
		}
		role := cp.RoleUser
		if block.Role == cp.RoleAssistant {
			role = cp.RoleAssistant
		}
		req.Messages = append(req.Messages, cp.StructuredMessage{Role: role, Text: block.Text})
	}
	return req
}

// completeAgentTurn records the assistant reply once the step succeeded and
// releases the writer. Completing twice appends nothing new.
func (s *Service) completeAgentTurn(ownerID string, step agentTurnStep) (agentTurnDone, error) {
	result, snapshot, err := s.StructuredResult(context.Background(), cp.Caller{UserID: ownerID}, step.TaskID)
	if err != nil {
		return agentTurnDone{}, err
	}
	if snapshot.Execution != cp.ExecutionSucceeded {
		return agentTurnDone{}, as.NewError(as.CodeSessionConflict, "模型步尚未完成")
	}
	done := agentTurnDone{Text: result.Text, ToolCalls: result.ToolCalls}
	session, err := s.agentSessionForOwner(ownerID, step.SessionID)
	if err != nil {
		return agentTurnDone{}, err
	}
	reply := as.HistoryMessage{ID: step.MessageID + ":reply", Kind: as.MessageKindMessage, Actor: as.ActorExecutor, Role: cp.RoleAssistant, Text: result.Text}
	if _, err := s.appendAgentSessionMessages(ownerID, step.SessionID, step.Lease, session.Revision, []as.HistoryMessage{reply}); err != nil {
		if as.IsCode(err, as.CodeStaleLease) && s.agentReplyRecorded(ownerID, step) {
			return done, nil
		}
		return agentTurnDone{}, err
	}
	if err := s.releaseAgentSessionWriter(ownerID, step.SessionID, step.Lease); err != nil && !as.IsCode(err, as.CodeStaleLease) {
		return agentTurnDone{}, err
	}
	return done, nil
}

// agentReplyRecorded reports whether an earlier completion already stored the
// reply, which makes a repeated completion after release a no-op.
func (s *Service) agentReplyRecorded(ownerID string, step agentTurnStep) bool {
	rows, err := s.repo.AgentSessionMessages(ownerID, step.SessionID)
	if err != nil {
		return false
	}
	for _, row := range rows {
		if row.MessageID == step.MessageID+":reply" {
			return true
		}
	}
	return false
}

// agentBillingApplication is the platform billing label of an Agent's model
// steps. The platform accepts a fixed list; the supervisor and registered
// businesses bill under "test" until they are added to it. The work tree's
// budget grant, not this label, is what attributes the cost.
func agentBillingApplication(application string) string {
	switch application {
	case as.ApplicationCanvas, as.ApplicationDrama, as.ApplicationEcommerce:
		return application
	}
	return "test"
}
