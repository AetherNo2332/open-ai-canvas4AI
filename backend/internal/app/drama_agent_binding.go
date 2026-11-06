package app

import (
	"strings"

	"yingce/backend/internal/model"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
)

// SetDramaRunningUnderWork is the Agent tool path: starting production under a
// work authorization binds the production to that work tree, so the scheduler's
// charged tasks are admitted against the tree's grant and stop when the work
// stops or its grant closes. Stopping changes nothing about the binding.
func (s *Service) SetDramaRunningUnderWork(userID, projectID string, running bool, workID, grantID string) (DramaSummary, error) {
	if !running {
		return s.SetDramaRunning(userID, projectID, false)
	}
	_, productionRow, err := s.dramaForUser(userID, projectID)
	if err != nil {
		return DramaSummary{}, err
	}
	work, err := s.repo.AgentWorkForOwner(userID, strings.TrimSpace(workID))
	if err != nil {
		return DramaSummary{}, workNotFound(err)
	}
	if ao.WorkTerminal(work.Status) {
		return DramaSummary{}, ao.NewError(ao.CodeWorkStopped, "工作已停止，不能启动制作")
	}
	if work.BudgetGrantID != strings.TrimSpace(grantID) {
		return DramaSummary{}, as.NewError(as.CodeForbidden, "预算授权与工作不一致")
	}
	grant, err := budgetGrantHost{s: s}.grant(userID, strings.TrimSpace(grantID))
	if err != nil {
		return DramaSummary{}, platformBudgetError(err)
	}
	if grant.Status != cp.BudgetStatusOpen {
		return DramaSummary{}, platformBudgetError(cp.NewError(cp.CodeBudgetExceeded, "预算授权已关闭"))
	}
	if err := s.repo.UpdateDramaProduction(productionRow.ProjectID, map[string]any{"agent_work_id": work.ID, "agent_grant_id": grant.ID}); err != nil {
		return DramaSummary{}, err
	}
	// 绑定后再读一次制作行并走启动校验；手动重启的清绑逻辑只属于 SetDramaRunning。
	if _, productionRow, err = s.dramaForUser(userID, projectID); err != nil {
		return DramaSummary{}, err
	}
	if err := s.resumeDrama(productionRow); err != nil {
		return DramaSummary{}, err
	}
	s.wakeDramaScheduler()
	return s.DramaSummary(userID, projectID)
}

// clearDramaAgentBinding 撤销制作的 Agent 授权绑定：手动重新开始制作意味着
// 制作不再由 Agent 工作驱动，后续任务回到项目预算口径。
func (s *Service) clearDramaAgentBinding(productionRow *model.DramaProduction) error {
	if productionRow.AgentWorkID == "" && productionRow.AgentGrantID == "" {
		return nil
	}
	return s.repo.UpdateDramaProduction(productionRow.ProjectID, map[string]any{"agent_work_id": "", "agent_grant_id": ""})
}
