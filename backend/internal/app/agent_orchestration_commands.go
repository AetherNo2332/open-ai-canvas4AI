package app

import (
	"encoding/json"
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

func outboxItem(ownerID, workID, target, kind string, payload any) *model.AgentOutbox {
	raw, _ := json.Marshal(payload)
	return &model.AgentOutbox{ID: newID(), OwnerID: ownerID, WorkItemID: workID, TargetSessionID: target, Kind: kind, Payload: string(raw), Status: "pending", CreatedAt: time.Now()}
}

// DelegateWork creates (or replays) a child work item, its executor Session,
// a budget allocation, the first assignment and the executor's delivery, all
// in one transaction. The key is owner + source message + action key, so a
// replayed supervisor step returns the same assignment.
func (s *Service) DelegateWork(ownerID string, req DelegateRequest) (AssignmentView, error) {
	parent, err := s.repo.AgentWorkForOwner(ownerID, strings.TrimSpace(req.WorkItemID))
	if err != nil {
		return AssignmentView{}, workNotFound(err)
	}
	if strings.TrimSpace(req.ActionKey) == "" || strings.TrimSpace(req.SourceMessageID) == "" || req.BudgetMicrocredits <= 0 {
		return AssignmentView{}, ao.NewError(ao.CodeInvalidRequest, "派工需要来源消息、动作键与预算")
	}
	scope := req.Scope.Normalized()
	def, err := s.agentRegistry().Resolve(req.DefinitionID, scope)
	if err != nil {
		return AssignmentView{}, err
	}
	if def.Role != as.RoleBusiness {
		return AssignmentView{}, ao.NewError(ao.CodeForbidden, "只能派工给业务 Agent")
	}
	if err := s.validateAgentSpace(ownerID, scope); err != nil {
		return AssignmentView{}, err
	}
	root, err := s.repo.AgentWorkForOwner(ownerID, parent.RootWorkID)
	if err != nil {
		return AssignmentView{}, workNotFound(err)
	}
	auth, err := ao.DecodeAuthorization(root.AuthorizationJSON)
	if err != nil {
		return AssignmentView{}, err
	}
	if !auth.AllowsScope(ao.ScopeGrant{Application: scope.Application, SpaceType: scope.SpaceType, SpaceID: scope.SpaceID}) {
		return AssignmentView{}, ao.NewError(ao.CodeForbidden, "该业务空间不在工作授权范围内")
	}
	key := "child:" + ao.DelegationKey(ownerID, req.SourceMessageID, req.ActionKey)
	var view AssignmentView
	err = s.repo.InAgentWork(ownerID, parent.RootWorkID, func(tx *repository.WorkTx) error {
		root, err := tx.Work(ownerID, parent.RootWorkID)
		if err != nil {
			return err
		}
		// A stopped or paused tree fences replays as well as new delegations.
		if err := ao.CheckDelegation(root.Status); err != nil {
			return err
		}
		if existing, err := tx.WorkByKey(ownerID, key); err == nil {
			assignment, err := tx.LatestAssignment(ownerID, existing.ID)
			if err != nil {
				return err
			}
			view = assignmentView(*assignment, existing.BudgetGrantID)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		current, err := tx.Work(ownerID, parent.ID)
		if err != nil {
			return err
		}
		if err := ao.CheckDelegation(current.Status); err != nil {
			return err
		}
		view, err = s.createDelegation(tx, ownerID, current, key, def, scope, req, root.AuthorizationJSON)
		return err
	})
	if err != nil {
		return AssignmentView{}, platformBudgetError(workNotFound(err))
	}
	return view, nil
}

func (s *Service) createDelegation(tx *repository.WorkTx, ownerID string, parent *model.AgentWorkItem, key string, def as.Definition, scope as.Scope, req DelegateRequest, authorizationJSON string) (AssignmentView, error) {
	childID, assignmentID := newID(), newID()
	grant, err := tx.AllocateBudget(s.repo, &model.PlatformBudgetGrant{ID: newID(), OwnerID: ownerID, IdempotencyKey: "work-child:" + childID, Fingerprint: "work-child:" + childID,
		LimitMicrocredits: req.BudgetMicrocredits, Status: cp.BudgetStatusOpen, Version: 1, Application: scope.Application, ObjectType: "work", ObjectID: childID,
		RootID: s.budgetRootOf(ownerID, parent.BudgetGrantID), ParentID: parent.BudgetGrantID}, func(nodes []repository.BudgetNodeRow) error {
		tree, err := budgetTreeOf(nodes)
		if err != nil {
			return err
		}
		return tree.AdmitAllocation(parent.BudgetGrantID, req.BudgetMicrocredits)
	})
	if err != nil {
		return AssignmentView{}, err
	}
	sessionKey := "work-executor:" + childID
	session, err := tx.CreateSession(&model.AgentSession{ID: agentSessionID(ownerID, sessionKey), OwnerID: ownerID, IdempotencyKey: sessionKey, Fingerprint: sessionKey,
		DefinitionID: def.ID, DefinitionVersion: def.Version, Role: def.Role, Application: scope.Application, SpaceType: scope.SpaceType, SpaceID: scope.SpaceID,
		WorkItemID: childID, Title: def.Title, Status: as.SessionActive, Revision: 1})
	if err != nil {
		return AssignmentView{}, err
	}
	child := &model.AgentWorkItem{ID: childID, OwnerID: ownerID, IdempotencyKey: key, ParentWorkID: parent.ID, RootWorkID: parent.RootWorkID, Goal: strings.TrimSpace(req.Handoff),
		Status: ao.WorkRunning, Version: 1, SourceSessionID: parent.SupervisorSessionID, SourceMessageID: req.SourceMessageID,
		Application: scope.Application, SpaceType: scope.SpaceType, SpaceID: scope.SpaceID, BudgetGrantID: grant.ID, ActiveAssignmentID: assignmentID,
		AuthorizationJSON: authorizationJSON, AuthorizationVersion: 1}
	if child.Goal == "" {
		child.Goal = parent.Goal
	}
	if err := tx.CreateWork(child); err != nil {
		return AssignmentView{}, err
	}
	assignment := &model.AgentAssignment{ID: assignmentID, OwnerID: ownerID, WorkItemID: childID, Epoch: 1, ExecutorSessionID: session.ID, DefinitionID: def.ID, Handoff: req.Handoff, Status: ao.AssignmentRunning}
	if err := tx.CreateAssignment(assignment); err != nil {
		return AssignmentView{}, err
	}
	// The executor receives only the handoff: goal, references and budget, not
	// the supervisor's history.
	if err := tx.Enqueue(outboxItem(ownerID, childID, session.ID, "assignment_started", map[string]any{"assignmentId": assignmentID, "handoff": req.Handoff, "budgetGrantId": grant.ID})); err != nil {
		return AssignmentView{}, err
	}
	return assignmentView(*assignment, grant.ID), nil
}

func (s *Service) budgetRootOf(ownerID, grantID string) string {
	grant, err := s.repo.BudgetGrantForOwner(ownerID, grantID)
	if err != nil {
		return grantID
	}
	return grant.RootID
}

// SubmitReport stores an executor report and, when it is from the current
// assignment, applies it and notifies the supervisor in the same transaction.
func (s *Service) SubmitReport(ownerID string, req SubmitReportRequest) (ReportView, error) {
	assignment, err := s.repo.AgentAssignmentForOwner(ownerID, strings.TrimSpace(req.AssignmentID))
	if err != nil {
		return ReportView{}, workNotFound(err)
	}
	if strings.TrimSpace(req.ReportID) == "" {
		return ReportView{}, ao.NewError(ao.CodeInvalidRequest, "报告 ID 必填")
	}
	work, err := s.repo.AgentWorkForOwner(ownerID, assignment.WorkItemID)
	if err != nil {
		return ReportView{}, workNotFound(err)
	}
	var view ReportView
	var stale error
	err = s.repo.InAgentWork(ownerID, work.RootWorkID, func(tx *repository.WorkTx) error {
		current, err := tx.Work(ownerID, work.ID)
		if err != nil {
			return err
		}
		live, err := tx.Assignment(ownerID, assignment.ID)
		if err != nil {
			return err
		}
		row := &model.AgentWorkReport{ID: newID(), OwnerID: ownerID, WorkItemID: work.ID, AssignmentID: live.ID, ReportID: req.ReportID, LeaseEpoch: req.LeaseEpoch,
			WorkVersion: req.WorkVersion, Status: req.Status, Summary: req.Summary, CreatedAt: time.Now()}
		effect, applyErr := ao.ApplyReport(ao.Assignment{ID: current.ActiveAssignmentID, Epoch: live.Epoch, Status: live.Status}, current.Version, ao.Report{AssignmentID: live.ID, LeaseEpoch: req.LeaseEpoch, WorkVersion: req.WorkVersion, Status: req.Status})
		if applyErr == nil && ao.WorkTerminal(current.Status) {
			applyErr = ao.NewError(ao.CodeWorkStopped, "工作已结束")
		}
		row.Applied = applyErr == nil
		inserted, err := tx.InsertReport(row)
		if err != nil {
			return err
		}
		view = ReportView{ReportID: row.ReportID, AssignmentID: row.AssignmentID, Status: row.Status, Summary: row.Summary, Applied: row.Applied, CreatedAt: row.CreatedAt}
		if !inserted {
			return nil // a redelivered report changes nothing
		}
		if applyErr != nil {
			if ao.IsCode(applyErr, ao.CodeInvalidRequest) {
				return applyErr
			}
			stale = applyErr // keep the audit row, report the fence
			return nil
		}
		return s.applyReportEffect(tx, ownerID, current, live, effect, req)
	})
	if err != nil {
		return ReportView{}, workNotFound(err)
	}
	return view, stale
}

func (s *Service) applyReportEffect(tx *repository.WorkTx, ownerID string, work *model.AgentWorkItem, assignment *model.AgentAssignment, effect ao.ReportEffect, req SubmitReportRequest) error {
	now := time.Now()
	if effect.EndAssignment {
		if err := tx.EndAssignment(ownerID, assignment.ID, effect.EndReason, now); err != nil {
			return err
		}
	}
	if effect.WorkStatus != "" {
		fields := map[string]any{"status": effect.WorkStatus}
		if effect.EndAssignment {
			fields["active_assignment_id"], fields["result_summary"] = "", req.Summary
		}
		if err := tx.UpdateWork(ownerID, work.ID, work.Version, fields); err != nil {
			return err
		}
	}
	// 根工作（独立咨询）没有父工作：报告留在自己的目录里，不向总控投递。
	if work.ParentWorkID == "" {
		return nil
	}
	parent, err := tx.Work(ownerID, work.ParentWorkID)
	if err != nil {
		return err
	}
	target := parent.SupervisorSessionID
	if target == "" {
		target = parent.SourceSessionID
	}
	return tx.Enqueue(outboxItem(ownerID, work.ID, target, "report", map[string]any{"workItemId": work.ID, "reportId": req.ReportID, "status": req.Status, "summary": req.Summary}))
}

// ReassignWork ends the current assignment and starts a new one on the same
// executor Session with a higher epoch.
func (s *Service) ReassignWork(ownerID, workID string, expectedVersion int64) (AssignmentView, error) {
	work, err := s.repo.AgentWorkForOwner(ownerID, strings.TrimSpace(workID))
	if err != nil {
		return AssignmentView{}, workNotFound(err)
	}
	var view AssignmentView
	err = s.repo.InAgentWork(ownerID, work.RootWorkID, func(tx *repository.WorkTx) error {
		root, err := tx.Work(ownerID, work.RootWorkID)
		if err != nil {
			return err
		}
		if err := ao.CheckDelegation(root.Status); err != nil {
			return err
		}
		current, err := tx.Work(ownerID, work.ID)
		if err != nil {
			return err
		}
		if err := ao.CheckDelegation(current.Status); err != nil {
			return err
		}
		previous, err := tx.LatestAssignment(ownerID, current.ID)
		if err != nil {
			return err
		}
		if err := tx.EndAssignment(ownerID, previous.ID, "replaced", time.Now()); err != nil {
			return err
		}
		next := &model.AgentAssignment{ID: newID(), OwnerID: ownerID, WorkItemID: current.ID, Epoch: previous.Epoch + 1, ExecutorSessionID: previous.ExecutorSessionID, DefinitionID: previous.DefinitionID, Handoff: previous.Handoff, Status: ao.AssignmentRunning}
		if err := tx.CreateAssignment(next); err != nil {
			return err
		}
		if err := tx.UpdateWork(ownerID, current.ID, expectedVersion, map[string]any{"active_assignment_id": next.ID}); err != nil {
			return err
		}
		view = assignmentView(*next, current.BudgetGrantID)
		return tx.Enqueue(outboxItem(ownerID, current.ID, next.ExecutorSessionID, "assignment_started", map[string]any{"assignmentId": next.ID}))
	})
	if err != nil {
		return AssignmentView{}, workNotFound(err)
	}
	return view, nil
}

// StopWork stops a work item and everything under it: work becomes
// cancelled, live assignments end, pending deliveries are dropped and the
// budgets close, all in one transaction. Nothing can restart it afterwards.
func (s *Service) StopWork(ownerID string, req WorkControlRequest) (WorkView, error) {
	work, err := s.repo.AgentWorkForOwner(ownerID, strings.TrimSpace(req.WorkItemID))
	if err != nil {
		return WorkView{}, workNotFound(err)
	}
	now := time.Now()
	err = s.repo.InAgentWork(ownerID, work.RootWorkID, func(tx *repository.WorkTx) error {
		current, err := tx.Work(ownerID, work.ID)
		if err != nil {
			return err
		}
		if ao.WorkTerminal(current.Status) {
			return nil
		}
		if err := tx.UpdateWork(ownerID, current.ID, req.ExpectedVersion, map[string]any{"status": ao.WorkCancelled, "active_assignment_id": "", "stopped_reason": req.Reason, "stopped_at": now}); err != nil {
			return err
		}
		all, err := tx.Descendants(ownerID, current.RootWorkID)
		if err != nil {
			return err
		}
		stopped := []string{current.ID}
		grants := []string{current.BudgetGrantID}
		for _, item := range workSubtree(current.ID, all) {
			if !ao.WorkTerminal(item.Status) {
				if err := tx.UpdateWork(ownerID, item.ID, item.Version, map[string]any{"status": ao.WorkCancelled, "active_assignment_id": "", "stopped_reason": "上级工作已停止", "stopped_at": now}); err != nil {
					return err
				}
			}
			stopped, grants = append(stopped, item.ID), append(grants, item.BudgetGrantID)
		}
		for _, id := range stopped {
			if latest, err := tx.LatestAssignment(ownerID, id); err == nil {
				if err := tx.EndAssignment(ownerID, latest.ID, "stopped", now); err != nil {
					return err
				}
			}
		}
		if err := tx.CancelPendingOutbox(ownerID, stopped); err != nil {
			return err
		}
		sessions, err := tx.SessionsForWorks(ownerID, stopped)
		if err != nil {
			return err
		}
		sessionIDs := make([]string, 0, len(sessions))
		for _, session := range sessions {
			sessionIDs = append(sessionIDs, session.ID)
		}
		if err := tx.CancelPendingTurns(ownerID, sessionIDs, now); err != nil {
			return err
		}
		return tx.CloseBudgets(ownerID, grants, now)
	})
	if err != nil {
		return WorkView{}, workNotFound(err)
	}
	return s.GetWork(ownerID, work.ID)
}

// workSubtree returns the items under id, given all descendants of the root.
func workSubtree(id string, all []model.AgentWorkItem) []model.AgentWorkItem {
	children := map[string][]model.AgentWorkItem{}
	for _, item := range all {
		children[item.ParentWorkID] = append(children[item.ParentWorkID], item)
	}
	out := []model.AgentWorkItem{}
	queue := []string{id}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, child := range children[next] {
			out = append(out, child)
			queue = append(queue, child.ID)
		}
	}
	return out
}
