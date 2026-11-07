package app

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// AgentSessionPiRunRequest is one user message dispatched to a Session's Pi
// worker. The server resolves Session, scope, work binding, tools and model;
// the caller only names the message that triggered the run.
type AgentSessionPiRunRequest struct {
	SessionID        string
	TriggerMessageID string
	Prompt           string
}

// AgentSessionPiRunView is the durable outcome of enqueueing one run. Reused is
// true when the same trigger message already produced this run.
type AgentSessionPiRunView struct {
	RunID  string `json:"runId"`
	Status string `json:"status"`
	Reused bool   `json:"reused"`
}

// agentSessionPiConversationID is the durable Pi conversation key of a Session.
// It is deliberately distinct from canvas conversation IDs so a Session can
// never attach to a canvas history tree by accident.
func agentSessionPiConversationID(sessionID string) string {
	return "agent-session:" + sessionID
}

func agentSessionPiRunID(ownerID, sessionID, triggerMessageID string) string {
	return cloudAgentID(ownerID, agentSessionPiConversationID(sessionID)+":"+triggerMessageID)
}

// EnqueueAgentSessionPiRun freezes one business-neutral Pi run. It is the only
// entry point that turns a Session message into an executable run; identity,
// scope, tool set, model and work binding are all resolved server-side.
func (s *Service) EnqueueAgentSessionPiRun(ownerID string, req AgentSessionPiRunRequest) (AgentSessionPiRunView, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return AgentSessionPiRunView{}, BadAuthRequest("请先登录")
	}
	session, err := s.agentSessionForOwner(ownerID, req.SessionID)
	if err != nil {
		return AgentSessionPiRunView{}, err
	}
	if session.Status != as.SessionActive {
		return AgentSessionPiRunView{}, as.NewError(as.CodeSessionConflict, "会话已归档，不能开始新的执行")
	}
	trigger := strings.TrimSpace(req.TriggerMessageID)
	prompt := strings.TrimSpace(req.Prompt)
	if trigger == "" || prompt == "" {
		return AgentSessionPiRunView{}, BadAuthRequest("触发消息与正文不能为空")
	}
	runID := agentSessionPiRunID(ownerID, session.ID, trigger)
	if existing, err := s.repo.CloudAgent(ownerID, runID); err == nil && existing != nil {
		return AgentSessionPiRunView{RunID: existing.ID, Status: existing.Status, Reused: true}, nil
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return AgentSessionPiRunView{}, err
	}
	binding, err := s.agentSessionPiBinding(ownerID, session)
	if err != nil {
		return AgentSessionPiRunView{}, err
	}
	tools, err := s.agentSessionPiTools(session)
	if err != nil {
		return AgentSessionPiRunView{}, err
	}
	if session.Role == as.RoleBusiness && len(tools) == 0 {
		return AgentSessionPiRunView{}, as.NewError(as.CodeInvalidRequest, "业务执行缺少有效工具")
	}
	selection, err := s.resolveAgentSessionModel(ownerID, session)
	if err != nil {
		return AgentSessionPiRunView{}, err
	}
	parentID := s.agentSessionPiParentRunID(ownerID, agentSessionPiConversationID(session.ID))
	state := agentSessionPiState(session, trigger, prompt, parentID, selection, binding, tools)
	if binding != nil {
		grant, err := s.repo.BudgetGrantForOwner(ownerID, binding.BudgetGrantID)
		if err != nil {
			return AgentSessionPiRunView{}, err
		}
		state.Request.Budget.MaxCredits = float64(grant.LimitMicrocredits) / float64(CreditScale)
	}
	now := time.Now()
	run := &model.CloudAgentExecution{
		ID: runID, UserID: ownerID, Status: "queued", Engine: "pi", Revision: 1,
		RuntimePhase: "ready", CanvasID: "", ParentID: parentID,
		ConversationID: agentSessionPiConversationID(session.ID),
		Title:          truncateRunes(prompt, 80), CreatedAt: now, UpdatedAt: now,
	}
	if err := cloudAgentSave(run, &state); err != nil {
		return AgentSessionPiRunView{}, err
	}
	if err := s.repo.CreateAgentSessionPiRun(run); err != nil {
		if errors.Is(err, repository.ErrAgentSessionBusy) {
			return AgentSessionPiRunView{}, as.NewError(as.CodeSessionConflict, "会话正在执行前一条消息，后续输入等待有序处理")
		}
		return AgentSessionPiRunView{}, err
	}
	return AgentSessionPiRunView{RunID: run.ID, Status: run.Status}, nil
}

// agentSessionPiParentRunID continues the durable Pi tree instead of starting a
// second branch from the same active run.
func (s *Service) agentSessionPiParentRunID(ownerID, conversationID string) string {
	session, _, err := s.repo.CloudAgentPiSession(ownerID, conversationID)
	if err != nil || session == nil {
		return ""
	}
	return session.ActiveRunID
}

// agentSessionPiBinding resolves the live work and assignment from the
// authorization records. A Session outside a work tree leaves the binding nil
// rather than fabricating a root.
func (s *Service) agentSessionPiBinding(ownerID string, session *model.AgentSession) (*as.ExecutionBinding, error) {
	if strings.TrimSpace(session.WorkItemID) == "" {
		return nil, nil
	}
	work, err := s.repo.AgentWorkForOwner(ownerID, session.WorkItemID)
	if err != nil {
		return nil, workNotFound(err)
	}
	if strings.TrimSpace(work.ActiveAssignmentID) == "" {
		return nil, as.NewError(as.CodeStaleAssignment, "工作没有有效派工")
	}
	assignment, err := s.repo.AgentAssignmentForOwner(ownerID, work.ActiveAssignmentID)
	if err != nil {
		return nil, workNotFound(err)
	}
	if assignment.ExecutorSessionID != session.ID {
		return nil, as.NewError(as.CodeStaleAssignment, "派工已改派给其他会话")
	}
	return &as.ExecutionBinding{
		WorkItemID: work.ID, AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
		BudgetGrantID: work.BudgetGrantID, AuthorizationVersion: work.AuthorizationVersion,
	}, nil
}

// agentSessionPiTools freezes the definition's tool set for the run. Execution
// still re-checks authorization on every call.
func (s *Service) agentSessionPiTools(session *model.AgentSession) ([]as.ToolSpec, error) {
	definitions := s.businessToolDefinitions(session.Application)
	if session.Role == as.RoleSupervisor {
		definitions = supervisorToolDefinitions()
	}
	var authorization *ao.WorkAuthorization
	if session.Role == as.RoleBusiness && session.WorkItemID != "" {
		work, err := s.repo.AgentWorkForOwner(session.OwnerID, session.WorkItemID)
		if err != nil {
			return nil, workNotFound(err)
		}
		auth, err := ao.DecodeAuthorization(work.AuthorizationJSON)
		if err != nil {
			return nil, err
		}
		authorization = &auth
	}
	tools := make([]as.ToolSpec, 0, len(definitions))
	for _, definition := range definitions {
		if authorization != nil && !authorization.AllowsTool(definition.Name) {
			continue
		}
		tools = append(tools, as.ToolSpec{Name: definition.Name, Description: definition.Description, Parameters: definition.Parameters})
	}
	return tools, nil
}

func agentSessionPiState(session *model.AgentSession, trigger, prompt, parentID string, selection cp.ModelSelection, binding *as.ExecutionBinding, tools []as.ToolSpec) cloudAgentRuntime {
	return cloudAgentRuntime{
		Request: CloudAgentRequest{
			Prompt: prompt, PermissionMode: "auto",
			Model: selection.ModelKey, LogicalModelID: selection.LogicalModelID,
			ChannelID: selection.ChannelID, ChannelModelKey: selection.ModelKey,
			IdempotencyKey: "agent-session:" + session.ID + ":" + trigger,
		},
		Canonical: canonicalAgentRequest{
			SystemPrompt: agentPlatformRules,
			Messages:     []map[string]any{{"role": "user", "content": prompt}},
			Tools:        piExecutionCanonicalTools(tools),
			ToolChoice:   "auto",
		},
		ParentID:  parentID,
		Decisions: map[string]string{},
		Events:    []CloudAgentEvent{},
		TaskIDs:   []string{},
		Execution: &cloudAgentExecutionState{
			ProtocolVersion: as.PiExecutionProtocolVersion, SessionID: session.ID,
			DefinitionID: session.DefinitionID, DefinitionVersion: session.DefinitionVersion,
			Role: session.Role, Scope: as.Scope{Application: session.Application, SpaceType: session.SpaceType, SpaceID: session.SpaceID},
			TriggerMessageID: trigger, Binding: binding, Tools: tools,
		},
	}
}
