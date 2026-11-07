package app

import (
	"strings"
	"time"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// independentAgentBudgetMicrocredits is the server policy ceiling for a direct
// business consultation. The user starts this isolated authorization by sending
// the first message; neither the browser nor the model can raise it.
const independentAgentBudgetMicrocredits int64 = 1_000_000

// acceptIndependentAgentMessage stores the first message, creates or reuses its
// isolated work/budget/assignment, binds the Session and queues the paid turn in
// one transaction. A crash therefore cannot leave a partially accepted request.
func (s *Service) acceptIndependentAgentMessage(session *model.AgentSession, messageID, text string) (*model.AgentWorkItem, error) {
	var work *model.AgentWorkItem
	err := s.repo.InAgentWork(session.OwnerID, "", func(tx *repository.WorkTx) error {
		current, err := tx.LockSessionForWork(session.OwnerID, session.ID)
		if err != nil {
			return err
		}
		now := time.Now()
		message := model.AgentSessionMessage{ID: newID(), MessageID: strings.TrimSpace(messageID), Kind: as.MessageKindMessage, Actor: as.ActorUser, Role: "user", Body: text}
		seq, err := tx.AppendSessionMessage(session.OwnerID, session.ID, message, now)
		if err != nil {
			return err
		}
		work, err = ensureIndependentAgentWorkTx(tx, current, messageID, text, now)
		if err != nil {
			return err
		}
		if !independentAgentWork(current, work) {
			return as.NewError(as.CodeScopeMismatch, "会话已绑定其他工作")
		}
		turn := &model.AgentTurn{
			ID: newID(), OwnerID: session.OwnerID, SessionID: session.ID,
			MessageID: message.MessageID, MessageSeq: seq, Text: text,
			Status: "pending", BudgetGrantID: work.BudgetGrantID,
		}
		return tx.EnqueueTurn(turn)
	})
	if err != nil {
		return nil, platformBudgetError(workNotFound(err))
	}
	s.wakeAgentRunner()
	return work, nil
}

// ensureIndependentAgentWork binds a previously unbound business Session to
// one root work, root budget and live assignment. It is retained as the narrow
// recovery primitive; normal message acceptance uses the atomic method above.
func (s *Service) ensureIndependentAgentWork(session *model.AgentSession, messageID, goal string) (*model.AgentWorkItem, error) {
	var work *model.AgentWorkItem
	err := s.repo.InAgentWork(session.OwnerID, "", func(tx *repository.WorkTx) error {
		current, err := tx.LockSessionForWork(session.OwnerID, session.ID)
		if err != nil {
			return err
		}
		work, err = ensureIndependentAgentWorkTx(tx, current, messageID, goal, time.Now())
		return err
	})
	if err != nil {
		return nil, platformBudgetError(workNotFound(err))
	}
	return work, nil
}

func ensureIndependentAgentWorkTx(tx *repository.WorkTx, session *model.AgentSession, messageID, goal string, now time.Time) (*model.AgentWorkItem, error) {
	if session.WorkItemID != "" {
		return tx.Work(session.OwnerID, session.WorkItemID)
	}
	workID := agentSessionID(session.OwnerID, "independent-work:"+session.ID)
	grantID := agentSessionID(session.OwnerID, "independent-budget:"+session.ID)
	assignmentID := agentSessionID(session.OwnerID, "independent-assignment:"+session.ID)
	authorization, err := ao.EncodeAuthorization(ao.WorkAuthorization{Version: 1, LimitMicrocredits: independentAgentBudgetMicrocredits,
		Scopes:                        []ao.ScopeGrant{{Application: session.Application, SpaceType: session.SpaceType, SpaceID: session.SpaceID}},
		ApprovalThresholdMicrocredits: agentApprovalThresholdMicrocredits})
	if err != nil {
		return nil, err
	}
	work := &model.AgentWorkItem{
		ID: workID, OwnerID: session.OwnerID, IdempotencyKey: "independent:" + session.ID,
		RootWorkID: workID, Goal: strings.TrimSpace(goal), Status: ao.WorkRunning, Version: 1,
		SourceSessionID: session.ID, SourceMessageID: strings.TrimSpace(messageID),
		Application: session.Application, SpaceType: session.SpaceType, SpaceID: session.SpaceID,
		BudgetGrantID: grantID, ActiveAssignmentID: assignmentID,
		AuthorizationJSON: authorization, AuthorizationVersion: 1,
	}
	if err := tx.CreateWork(work); err != nil {
		return nil, err
	}
	grant := &model.PlatformBudgetGrant{
		ID: grantID, OwnerID: session.OwnerID, IdempotencyKey: "work-root:" + workID,
		Fingerprint: "work-root:" + workID, RootID: grantID,
		LimitMicrocredits: independentAgentBudgetMicrocredits, Status: cp.BudgetStatusOpen, Version: 1,
		Application: session.Application, ObjectType: "work", ObjectID: workID,
	}
	if err := tx.CreateBudgetRoot(grant); err != nil {
		return nil, err
	}
	assignment := &model.AgentAssignment{
		ID: assignmentID, OwnerID: session.OwnerID, WorkItemID: workID, Epoch: 1,
		ExecutorSessionID: session.ID, DefinitionID: session.DefinitionID,
		Handoff: strings.TrimSpace(goal), Status: ao.AssignmentRunning,
	}
	if err := tx.CreateAssignment(assignment); err != nil {
		return nil, err
	}
	if err := tx.BindSessionWork(session.OwnerID, session.ID, workID, now); err != nil {
		return nil, err
	}
	return work, nil
}

func isIndependentBusinessSession(session *model.AgentSession) bool {
	return session.Role == as.RoleBusiness && session.WorkItemID == ""
}

func independentAgentWork(session *model.AgentSession, work *model.AgentWorkItem) bool {
	return work.ParentWorkID == "" && work.SourceSessionID == session.ID && work.SupervisorSessionID == "" && work.Application == session.Application && work.SpaceType == session.SpaceType && work.SpaceID == session.SpaceID
}
