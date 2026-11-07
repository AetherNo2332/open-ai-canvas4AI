package app

import (
	"strings"

	"yingce/backend/internal/model"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
)

func (a dramaAgentAdapter) bindChargedOperation(call as.ToolInvocation) error {
	switch call.Name {
	case "drama_retry_failed", "drama_generate_segments", "drama_approve_episode":
		if call.WorkItemID == "" || call.BudgetGrantID == "" {
			return as.NewError(as.CodeForbidden, "收费操作缺少工作授权")
		}
		_, err := a.s.bindDramaWork(call.OwnerID, call.Scope.SpaceID, call.WorkItemID, call.BudgetGrantID)
		return err
	}
	return nil
}

// SetDramaRunningUnderWork is the Agent tool path: starting production under a
// work authorization binds the production to that work tree, so the scheduler's
// charged tasks are admitted against the tree's grant and stop when the work
// stops or its grant closes. Stopping changes nothing about the binding.
func (s *Service) SetDramaRunningUnderWork(userID, projectID string, running bool, workID, grantID string) (DramaSummary, error) {
	if !running {
		return s.SetDramaRunning(userID, projectID, false)
	}
	productionRow, err := s.bindDramaWork(userID, projectID, workID, grantID)
	if err != nil {
		return DramaSummary{}, err
	}
	if err := s.resumeDrama(productionRow); err != nil {
		return DramaSummary{}, err
	}
	s.wakeDramaScheduler()
	return s.DramaSummary(userID, projectID)
}

// bindDramaWork attaches authority before any paid operation can wake the
// production scheduler. It does not start production or clear an existing grant.
func (s *Service) bindDramaWork(userID, projectID, workID, grantID string) (*model.DramaProduction, error) {
	_, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return nil, err
	}
	work, err := s.repo.AgentWorkForOwner(userID, strings.TrimSpace(workID))
	if err != nil {
		return nil, workNotFound(err)
	}
	if ao.WorkTerminal(work.Status) {
		return nil, ao.NewError(ao.CodeWorkStopped, "工作已停止，不能启动制作")
	}
	if work.Application != as.ApplicationDrama || work.SpaceType != as.SpaceDramaProject || work.SpaceID != projectID {
		return nil, as.NewError(as.CodeScopeMismatch, "工作与短剧项目不一致")
	}
	if work.BudgetGrantID != strings.TrimSpace(grantID) {
		return nil, as.NewError(as.CodeForbidden, "预算授权与工作不一致")
	}
	grant, err := budgetGrantHost{s: s}.grant(userID, strings.TrimSpace(grantID))
	if err != nil {
		return nil, platformBudgetError(err)
	}
	if grant.Status != cp.BudgetStatusOpen {
		return nil, platformBudgetError(cp.NewError(cp.CodeBudgetExceeded, "预算授权已关闭"))
	}
	if err := s.repo.UpdateDramaProduction(productionRow.ProjectID, map[string]any{"agent_work_id": work.ID, "agent_grant_id": grant.ID}); err != nil {
		return nil, err
	}
	// 绑定后再读一次制作行并走启动校验；手动重启的清绑逻辑只属于 SetDramaRunning。
	if _, productionRow, err = s.dramaForUser(userID, projectID); err != nil {
		return nil, err
	}
	return productionRow, nil
}

// clearDramaAgentBinding 撤销制作的 Agent 授权绑定：手动重新开始制作意味着
// 制作不再由 Agent 工作驱动，后续任务回到项目预算口径。
func (s *Service) clearDramaAgentBinding(productionRow *model.DramaProduction) error {
	if productionRow.AgentWorkID == "" && productionRow.AgentGrantID == "" {
		return nil
	}
	return s.repo.UpdateDramaProduction(productionRow.ProjectID, map[string]any{"agent_work_id": "", "agent_grant_id": ""})
}
