package app

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// SendSessionMessageRequest is a user message to a Session; the actor kind is
// always written by the server.
type SendSessionMessageRequest struct {
	SessionID string
	MessageID string
	Text      string
}

type SessionMessageReceipt struct {
	Accepted    bool   `json:"accepted"`
	WorkItemID  string `json:"workItemId,omitempty"`
	WorkVersion int64  `json:"workVersion,omitempty"`
}

// AppendInputRequest is a supervisor input to delegated work.
type AppendInputRequest struct {
	WorkItemID      string
	SourceMessageID string
	Text            string
	IdempotencyKey  string
}

type WorkInputReceipt struct {
	WorkItemID  string `json:"workItemId"`
	WorkVersion int64  `json:"workVersion"`
}

// SendSessionMessage is direct chat: a user message to a business Session.
// For an executor Session it becomes a work input under the same ordering as
// supervisor inputs, and the supervisor gets one notice. Temporary canvas
// subagent conversations are not Sessions here and keep their own rule.
func (s *Service) SendSessionMessage(ownerID string, req SendSessionMessageRequest) (SessionMessageReceipt, error) {
	session, err := s.agentSessionForOwner(ownerID, req.SessionID)
	if err != nil {
		return SessionMessageReceipt{}, err
	}
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" || strings.TrimSpace(req.Text) == "" {
		return SessionMessageReceipt{}, as.NewError(as.CodeInvalidRequest, "消息 ID 与正文必填")
	}
	if session.Status != as.SessionActive {
		return SessionMessageReceipt{}, as.NewError(as.CodeSessionConflict, "会话已归档")
	}
	if session.WorkItemID == "" || session.Role != as.RoleBusiness {
		// A Session without delegated work keeps the message in its own history.
		if err := s.repo.AppendAgentInboxMessage(ownerID, session.ID, model.AgentSessionMessage{ID: newID(), MessageID: messageID, Kind: as.MessageKindMessage, Actor: as.ActorUser, Role: "user", Body: req.Text}, time.Now()); err != nil {
			return SessionMessageReceipt{}, err
		}
		return SessionMessageReceipt{Accepted: true}, nil
	}
	receipt, err := s.appendWorkInput(ownerID, session.WorkItemID, as.ActorUser, messageID, req.Text, "user:"+session.ID+":"+messageID)
	if err != nil {
		return SessionMessageReceipt{}, err
	}
	return SessionMessageReceipt{Accepted: true, WorkItemID: receipt.WorkItemID, WorkVersion: receipt.WorkVersion}, nil
}

// AppendWorkInput is the supervisor's input command.
func (s *Service) AppendWorkInput(ownerID string, req AppendInputRequest) (WorkInputReceipt, error) {
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" || strings.TrimSpace(req.Text) == "" {
		return WorkInputReceipt{}, ao.NewError(ao.CodeInvalidRequest, "输入正文与幂等键必填")
	}
	return s.appendWorkInput(ownerID, req.WorkItemID, as.ActorSupervisor, req.SourceMessageID, req.Text, "supervisor:"+key)
}

// appendWorkInput is the single ordered inbox of a work item: each accepted
// input bumps the work version in the work-tree lock, so concurrent user and
// supervisor inputs get distinct versions and are delivered once.
func (s *Service) appendWorkInput(ownerID, workID, actor, sourceMessageID, text, key string) (WorkInputReceipt, error) {
	work, err := s.repo.AgentWorkForOwner(ownerID, strings.TrimSpace(workID))
	if err != nil {
		return WorkInputReceipt{}, workNotFound(err)
	}
	receipt := WorkInputReceipt{WorkItemID: work.ID}
	err = s.repo.InAgentWork(ownerID, work.RootWorkID, func(tx *repository.WorkTx) error {
		root, err := tx.Work(ownerID, work.RootWorkID)
		if err != nil {
			return err
		}
		current, err := tx.Work(ownerID, work.ID)
		if err != nil {
			return err
		}
		for _, status := range []string{root.Status, current.Status} {
			if err := ao.CheckDelegation(status); err != nil {
				return err
			}
		}
		input := &model.AgentWorkInput{ID: newID(), OwnerID: ownerID, WorkItemID: current.ID, IdempotencyKey: key, Actor: actor, SourceMessageID: sourceMessageID, Text: text, WorkVersion: current.Version + 1, CreatedAt: time.Now()}
		inserted, err := tx.InsertInput(input)
		if err != nil || !inserted {
			receipt.WorkVersion = current.Version
			return err
		}
		if err := tx.UpdateWork(ownerID, current.ID, current.Version, map[string]any{}); err != nil {
			return err
		}
		receipt.WorkVersion = input.WorkVersion
		assignment, err := tx.LatestAssignment(ownerID, current.ID)
		if err != nil {
			return err
		}
		if err := tx.Enqueue(outboxItem(ownerID, current.ID, assignment.ExecutorSessionID, "work_input", map[string]any{"inputId": input.ID, "actor": actor, "workVersion": input.WorkVersion})); err != nil {
			return err
		}
		if actor != as.ActorUser {
			return nil
		}
		parent, err := tx.Work(ownerID, current.ParentWorkID)
		if err != nil {
			return err
		}
		return tx.Enqueue(outboxItem(ownerID, current.ID, parent.SupervisorSessionID, "user_input", map[string]any{"workItemId": current.ID, "workVersion": input.WorkVersion}))
	})
	if err != nil {
		return WorkInputReceipt{}, workNotFound(err)
	}
	return receipt, nil
}

type ApprovalRequest struct {
	WorkItemID        string
	SessionID         string
	CallID            string
	QuoteMicrocredits int64
	TargetVersion     string
	Summary           string
}

type ApprovalView struct {
	ID                   string `json:"id"`
	WorkItemID           string `json:"workItemId"`
	SessionID            string `json:"sessionId"`
	Summary              string `json:"summary"`
	QuoteMicrocredits    int64  `json:"quoteMicrocredits"`
	AuthorizationVersion int64  `json:"authorizationVersion"`
	Status               string `json:"status"`
}

type DecideApprovalRequest struct {
	ApprovalID                   string
	Decision                     string
	ExpectedAuthorizationVersion int64
}

func approvalView(row model.AgentApproval) ApprovalView {
	return ApprovalView{ID: row.ID, WorkItemID: row.WorkItemID, SessionID: row.SessionID, Summary: row.Summary, QuoteMicrocredits: row.QuoteMicrocredits, AuthorizationVersion: row.AuthorizationVersion, Status: row.Status}
}

func approvalState(row model.AgentApproval) ao.Approval {
	return ao.Approval{Status: row.Status, AuthorizationVersion: row.AuthorizationVersion, QuoteMicrocredits: row.QuoteMicrocredits, TargetVersion: row.TargetVersion}
}

// RequestApproval records an approval for one tool call of an executor
// Session. Requesting again for the same call returns the same approval.
func (s *Service) RequestApproval(ownerID string, req ApprovalRequest) (ApprovalView, error) {
	work, err := s.repo.AgentWorkForOwner(ownerID, strings.TrimSpace(req.WorkItemID))
	if err != nil {
		return ApprovalView{}, workNotFound(err)
	}
	if strings.TrimSpace(req.CallID) == "" || req.QuoteMicrocredits < 0 {
		return ApprovalView{}, ao.NewError(ao.CodeInvalidRequest, "审批需要工具调用 ID 与报价")
	}
	session, err := s.agentSessionForOwner(ownerID, req.SessionID)
	if err != nil {
		return ApprovalView{}, err
	}
	if session.WorkItemID != work.ID {
		return ApprovalView{}, ao.NewError(ao.CodeForbidden, "会话不属于该工作")
	}
	var view ApprovalView
	err = s.repo.InAgentWork(ownerID, work.RootWorkID, func(tx *repository.WorkTx) error {
		current, err := tx.Work(ownerID, work.ID)
		if err != nil {
			return err
		}
		if err := ao.CheckDelegation(current.Status); err != nil {
			return err
		}
		stored, err := tx.InsertApproval(&model.AgentApproval{ID: newID(), OwnerID: ownerID, WorkItemID: work.ID, RootWorkID: work.RootWorkID, SessionID: session.ID, CallID: strings.TrimSpace(req.CallID),
			Summary: req.Summary, QuoteMicrocredits: req.QuoteMicrocredits, TargetVersion: req.TargetVersion, AuthorizationVersion: current.Version, Status: ao.ApprovalPending, CreatedAt: time.Now()})
		if err != nil {
			return err
		}
		view = approvalView(*stored)
		return nil
	})
	return view, workNotFound(err)
}

// WorkApprovals lists the approvals of a work item; for a root work it lists
// the whole tree, which is how the supervisor view shows the same IDs.
func (s *Service) WorkApprovals(ownerID, workID string) ([]ApprovalView, error) {
	work, err := s.repo.AgentWorkForOwner(ownerID, strings.TrimSpace(workID))
	if err != nil {
		return nil, workNotFound(err)
	}
	rows, err := s.repo.AgentApprovalsForWork(ownerID, work.ID, work.ParentWorkID == "")
	if err != nil {
		return nil, err
	}
	views := make([]ApprovalView, 0, len(rows))
	for _, row := range rows {
		views = append(views, approvalView(row))
	}
	return views, nil
}

// DecideApproval records the user's decision; deciding again the same way is
// a no-op, from either view.
func (s *Service) DecideApproval(ownerID string, req DecideApprovalRequest) (ApprovalView, error) {
	return s.changeApproval(ownerID, req.ApprovalID, func(approval model.AgentApproval, work *model.AgentWorkItem) (string, error) {
		return ao.DecideApproval(approvalState(approval), req.Decision, req.ExpectedAuthorizationVersion, work.Version, work.Status)
	})
}

// ConsumeApproval lets one execution use an approved approval, with the quote
// and target it is about to use.
func (s *Service) ConsumeApproval(ownerID, approvalID string, quote int64, targetVersion string) error {
	_, err := s.changeApproval(ownerID, approvalID, func(approval model.AgentApproval, work *model.AgentWorkItem) (string, error) {
		return ao.ConsumeApproval(approvalState(approval), quote, targetVersion, work.Version, work.Status)
	})
	return err
}

// changeApproval applies a rule under the work-tree lock. An expiry the rule
// reports is stored even though the call returns its error.
func (s *Service) changeApproval(ownerID, approvalID string, rule func(model.AgentApproval, *model.AgentWorkItem) (string, error)) (ApprovalView, error) {
	approval, err := s.repo.AgentApprovalForOwner(ownerID, strings.TrimSpace(approvalID))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ApprovalView{}, ao.NewError(ao.CodeNotFound, "审批不存在")
	}
	if err != nil {
		return ApprovalView{}, err
	}
	var view ApprovalView
	var ruleErr error
	err = s.repo.InAgentWork(ownerID, approval.RootWorkID, func(tx *repository.WorkTx) error {
		current, err := tx.Approval(ownerID, approval.ID)
		if err != nil {
			return err
		}
		work, err := tx.Work(ownerID, current.WorkItemID)
		if err != nil {
			return err
		}
		next, err := rule(*current, work)
		ruleErr = err
		view = approvalView(*current)
		if next == current.Status {
			return nil
		}
		if err := tx.SetApprovalStatus(ownerID, current.ID, current.Status, next, time.Now()); err != nil {
			return err
		}
		view.Status = next
		return nil
	})
	if err != nil {
		return ApprovalView{}, workNotFound(err)
	}
	return view, ruleErr
}

// SessionMessageView is one message as the SDK reads it.
type SessionMessageView struct {
	SessionID string    `json:"sessionId"`
	MessageID string    `json:"messageId"`
	Sequence  int64     `json:"sequence"`
	Kind      string    `json:"kind"`
	Actor     string    `json:"actor"`
	Role      string    `json:"role"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

type SessionMessagePage struct {
	Messages  []SessionMessageView `json:"messages"`
	NextAfter int64                `json:"nextAfter"`
}

// SessionMessages returns the owner's Session messages after a sequence.
// Canvas Sessions keep their history in the Pi tables and return none here.
func (s *Service) SessionMessages(ownerID, sessionID string, after int64) (SessionMessagePage, error) {
	session, err := s.agentSessionForOwner(ownerID, sessionID)
	if err != nil {
		return SessionMessagePage{}, err
	}
	rows, err := s.repo.AgentSessionMessages(ownerID, session.ID)
	if err != nil {
		return SessionMessagePage{}, err
	}
	page := SessionMessagePage{Messages: []SessionMessageView{}, NextAfter: after}
	for _, row := range rows {
		if row.Sequence <= after {
			continue
		}
		page.Messages = append(page.Messages, SessionMessageView{SessionID: row.SessionID, MessageID: row.MessageID, Sequence: row.Sequence, Kind: row.Kind, Actor: row.Actor, Role: row.Role, Text: row.Body, CreatedAt: row.CreatedAt})
		page.NextAfter = row.Sequence
	}
	return page, nil
}
