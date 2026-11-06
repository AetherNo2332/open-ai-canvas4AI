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

const supervisorDefinitionID = "supervisor.agent"

// StartWorkRequest creates root work with a supervisor Session and a budget root.
type StartWorkRequest struct {
	SourceSessionID    string
	SourceMessageID    string
	Goal               string
	BudgetMicrocredits int64
	IdempotencyKey     string
}

type WorkView struct {
	ID                  string   `json:"id"`
	ParentWorkID        string   `json:"parentWorkId,omitempty"`
	RootWorkID          string   `json:"rootWorkId"`
	Goal                string   `json:"goal"`
	Status              string   `json:"status"`
	Version             int64    `json:"version"`
	SupervisorSessionID string   `json:"supervisorSessionId,omitempty"`
	Scope               as.Scope `json:"scope"`
	BudgetGrantID       string   `json:"budgetGrantId"`
	ActiveAssignmentID  string   `json:"activeAssignmentId,omitempty"`
	ResultSummary       string   `json:"resultSummary,omitempty"`
}

// DelegateRequest is the supervisor's only way to hand work to a business Agent.
type DelegateRequest struct {
	WorkItemID         string
	SourceMessageID    string
	ActionKey          string
	DefinitionID       string
	Scope              as.Scope
	Handoff            string
	BudgetMicrocredits int64
}

type AssignmentView struct {
	ID                string `json:"id"`
	WorkItemID        string `json:"workItemId"`
	Epoch             int64  `json:"epoch"`
	ExecutorSessionID string `json:"executorSessionId"`
	BudgetGrantID     string `json:"budgetGrantId"`
	Status            string `json:"status"`
}

type SubmitReportRequest struct {
	AssignmentID string
	LeaseEpoch   int64
	WorkVersion  int64
	ReportID     string
	Status       string
	Summary      string
}

type ReportView struct {
	ReportID     string    `json:"reportId"`
	AssignmentID string    `json:"assignmentId"`
	Status       string    `json:"status"`
	Summary      string    `json:"summary"`
	Applied      bool      `json:"applied"`
	CreatedAt    time.Time `json:"createdAt"`
}

type WorkControlRequest struct {
	WorkItemID      string
	ExpectedVersion int64
	Reason          string
}

func workView(row model.AgentWorkItem) WorkView {
	return WorkView{ID: row.ID, ParentWorkID: row.ParentWorkID, RootWorkID: row.RootWorkID, Goal: row.Goal, Status: row.Status, Version: row.Version,
		SupervisorSessionID: row.SupervisorSessionID, Scope: as.Scope{Application: row.Application, SpaceType: row.SpaceType, SpaceID: row.SpaceID},
		BudgetGrantID: row.BudgetGrantID, ActiveAssignmentID: row.ActiveAssignmentID, ResultSummary: row.ResultSummary}
}

func assignmentView(row model.AgentAssignment, grantID string) AssignmentView {
	return AssignmentView{ID: row.ID, WorkItemID: row.WorkItemID, Epoch: row.Epoch, ExecutorSessionID: row.ExecutorSessionID, BudgetGrantID: grantID, Status: row.Status}
}

func workNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ao.NewError(ao.CodeNotFound, "工作不存在")
	}
	if errors.Is(err, repository.ErrAgentWorkChanged) {
		return ao.NewError(ao.CodeVersionConflict, "工作已更新，请刷新后再操作")
	}
	return err
}

// StartWork creates a root work item, its supervisor Session and a budget
// root in one transaction. The budget is the user's authorization; nothing a
// model says later can raise it.
func (s *Service) StartWork(ownerID string, req StartWorkRequest) (WorkView, error) {
	if strings.TrimSpace(ownerID) == "" {
		return WorkView{}, ao.NewError(ao.CodeForbidden, "请先登录")
	}
	key := strings.TrimSpace(req.IdempotencyKey)
	if key == "" || strings.TrimSpace(req.Goal) == "" || req.BudgetMicrocredits <= 0 {
		return WorkView{}, ao.NewError(ao.CodeInvalidRequest, "工作需要目标、预算与幂等键")
	}
	if existing, err := s.repo.AgentWorkByKey(ownerID, "root:"+key); err == nil {
		return workView(*existing), nil
	}
	workID, grantID := newID(), newID()
	def, _ := s.agentRegistry().Definition(supervisorDefinitionID)
	sessionKey := "work-supervisor:" + workID
	work := &model.AgentWorkItem{ID: workID, OwnerID: ownerID, IdempotencyKey: "root:" + key, RootWorkID: workID, Goal: strings.TrimSpace(req.Goal), Status: ao.WorkRunning, Version: 1,
		SourceSessionID: req.SourceSessionID, SourceMessageID: req.SourceMessageID, SupervisorSessionID: agentSessionID(ownerID, sessionKey),
		Application: as.ApplicationOrchestration, SpaceType: as.SpaceWork, SpaceID: workID, BudgetGrantID: grantID}
	err := s.repo.InAgentWork(ownerID, "", func(tx *repository.WorkTx) error {
		if err := tx.CreateWork(work); err != nil {
			return err
		}
		grant := &model.PlatformBudgetGrant{ID: grantID, OwnerID: ownerID, IdempotencyKey: "work-root:" + workID, Fingerprint: "work-root:" + workID, RootID: grantID,
			LimitMicrocredits: req.BudgetMicrocredits, Status: cp.BudgetStatusOpen, Version: 1, Application: as.ApplicationOrchestration, ObjectType: "work", ObjectID: workID}
		if err := tx.CreateBudgetRoot(grant); err != nil {
			return err
		}
		_, err := tx.CreateSession(&model.AgentSession{ID: work.SupervisorSessionID, OwnerID: ownerID, IdempotencyKey: sessionKey, Fingerprint: sessionKey,
			DefinitionID: def.ID, DefinitionVersion: def.Version, Role: def.Role, Application: as.ApplicationOrchestration, SpaceType: as.SpaceWork, SpaceID: workID,
			WorkItemID: workID, Title: def.Title, Status: as.SessionActive, Revision: 1})
		return err
	})
	if err != nil {
		if existing, lookupErr := s.repo.AgentWorkByKey(ownerID, "root:"+key); lookupErr == nil {
			return workView(*existing), nil
		}
		return WorkView{}, err
	}
	return workView(*work), nil
}

func (s *Service) GetWork(ownerID, workID string) (WorkView, error) {
	work, err := s.repo.AgentWorkForOwner(ownerID, strings.TrimSpace(workID))
	if err != nil {
		return WorkView{}, workNotFound(err)
	}
	return workView(*work), nil
}

func (s *Service) ListReports(ownerID, workID string) ([]ReportView, error) {
	if _, err := s.GetWork(ownerID, workID); err != nil {
		return nil, err
	}
	rows, err := s.repo.AgentWorkReports(ownerID, workID)
	if err != nil {
		return nil, err
	}
	views := make([]ReportView, 0, len(rows))
	for _, row := range rows {
		views = append(views, ReportView{ReportID: row.ReportID, AssignmentID: row.AssignmentID, Status: row.Status, Summary: row.Summary, Applied: row.Applied, CreatedAt: row.CreatedAt})
	}
	return views, nil
}
