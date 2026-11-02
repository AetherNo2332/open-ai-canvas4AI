package app

import (
	"fmt"
	"math"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 子代理写入权限：spawn 时的授权决策、父预算 carve 与审批上浮。
// 权限 = 父模式 × 画布子代理写策略 × spawn 显式声明，三者取最严；
// 子代理权限在 spawn 时定型，运行中不可切换。

const (
	// cloudAgentSubagentCarveSiblingsBase 让首个写型子代理拿到剩余预算的一半，
	// 并发越多单个份额越小，避免并行子代理合谋花满父预算。
	cloudAgentSubagentCarveSiblingsBase = 1
)

type cloudAgentSubagentWritePlan struct {
	Granted       bool
	Reason        string  // 未授予时的机器可读原因；授予时为空
	Permission    string  // 子运行权限模式（授予时继承父模式，否则 read_only）
	CarveCredits  float64 // 从父剩余切分给子运行的积分额度
	ParentRemain  float64 // 决策时父当前剩余积分（回执观测用）
	SiblingActive int     // 决策时仍在运行的兄弟子代理数
}

// planSubagentWrite 计算 spawn 的写入授权与预算 carve。
// 父剩余积分 = 父预算 − 父已产生订单 − 仍在运行的兄弟子代理 carve 份额。
func (s *Service) planSubagentWrite(run *model.CloudAgentExecution, state *cloudAgentRuntime, input spawnSubagentInput, siblingActive int) (cloudAgentSubagentWritePlan, error) {
	plan := cloudAgentSubagentWritePlan{Permission: "read_only", SiblingActive: siblingActive}
	if !input.Write {
		return plan, nil
	}
	switch {
	case state.Request.PermissionMode == "read_only":
		plan.Reason = "parent_read_only"
		return plan, nil
	case !state.Request.SubagentWriteEnabled:
		plan.Reason = "canvas_policy_disabled"
		return plan, nil
	case parentModeUnsupported(state.Request.PermissionMode):
		plan.Reason = "parent_mode_unsupported"
		return plan, nil
	}
	orders, err := s.repo.BillingOrdersByTaskIDs(run.UserID, state.TaskIDs)
	if err != nil {
		return plan, err
	}
	remaining := int64(math.Floor(state.Request.Budget.MaxCredits * float64(CreditScale)))
	for _, order := range orders {
		remaining -= order.AmountMicrocredits
	}
	remaining -= s.cloudAgentActiveSubagentCarves(run.UserID, state)
	if remaining < 0 {
		remaining = 0
	}
	plan.ParentRemain = float64(remaining) / float64(CreditScale)
	// carve = 剩余 × 1/(1+活跃兄弟数)；spawn 的 maxCredits 只能收紧不能放大。
	plan.CarveCredits = plan.ParentRemain / float64(cloudAgentSubagentCarveSiblingsBase+siblingActive)
	if input.MaxCredits > 0 && input.MaxCredits < plan.CarveCredits {
		plan.CarveCredits = input.MaxCredits
	}
	if plan.CarveCredits < 0 {
		plan.CarveCredits = 0
	}
	plan.Granted = true
	plan.Permission = state.Request.PermissionMode
	return plan, nil
}

func parentModeUnsupported(mode string) bool {
	return mode != "auto" && mode != "request_approval"
}

// cloudAgentActiveSubagentCarves 汇总父状态里仍在运行的子代理 carve 份额；
// 只对父运行有意义（子运行 Subagent != nil，没有自己的子代理）。
func (s *Service) cloudAgentActiveSubagentCarves(userID string, state *cloudAgentRuntime) int64 {
	if state == nil || state.Subagent != nil || len(state.SubagentBudgetCarves) == 0 {
		return 0
	}
	links, err := s.repo.AgentSubagentLinkByParent(userID, state.RuntimeRunID, []string{repository.SubagentStatusQueued, repository.SubagentStatusRunning})
	if err != nil {
		// 查询失败按保守处理：不扣减（宁可预算超支边界变松，也不能把父饿死）。
		return 0
	}
	active := map[string]bool{}
	for _, link := range links {
		active[link.ID] = true
	}
	var total int64
	for linkID, credits := range state.SubagentBudgetCarves {
		if active[linkID] {
			total += int64(math.Floor(credits * float64(CreditScale)))
		}
	}
	return total
}

// recordSubagentBudgetCarve 把 carve 份额记到父运行状态（best-effort，两次 CAS）。
func (s *Service) recordSubagentBudgetCarve(userID, parentRunID, linkID string, credits float64) {
	for attempt := 0; attempt < 2; attempt++ {
		run, err := s.repo.CloudAgent(userID, parentRunID)
		if err != nil || cloudAgentRunTerminal(run.Status) {
			return
		}
		if err := s.repo.MutateCloudAgent(userID, parentRunID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			if state.SubagentBudgetCarves == nil {
				state.SubagentBudgetCarves = map[string]float64{}
			}
			state.SubagentBudgetCarves[linkID] = credits
			return cloudAgentSave(current, &state)
		}); err == nil {
			return
		}
	}
}

// releaseSubagentBudgetCarve 在子代理终态后归还未用完的 carve 份额（best-effort）。
func (s *Service) releaseSubagentBudgetCarve(userID, parentRunID, linkID string) {
	for attempt := 0; attempt < 2; attempt++ {
		run, err := s.repo.CloudAgent(userID, parentRunID)
		if err != nil || cloudAgentRunTerminal(run.Status) {
			return
		}
		if err := s.repo.MutateCloudAgent(userID, parentRunID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			if _, exists := state.SubagentBudgetCarves[linkID]; !exists {
				return nil
			}
			delete(state.SubagentBudgetCarves, linkID)
			return cloudAgentSave(current, &state)
		}); err == nil {
			return
		}
	}
}

// surfaceSubagentApproval 把子代理的等待审批状态上浮到父会话：幂等键按
// childRun+approvalID 固定，重复 reconcile 不会重复投递。
func (s *Service) surfaceSubagentApproval(repo *repository.Repository, link *model.AgentSubagentLink, child *model.CloudAgentExecution) error {
	state, err := cloudAgentDecode(child)
	if err != nil {
		return err
	}
	if state.Approval == nil || state.Approval.Decision != "" {
		return nil
	}
	runtime := &SubagentRuntime{LinkID: link.ID, ParentRunID: link.ParentRunID, ChildRunID: link.ChildRunID, DisplayName: link.DisplayName, RoleLabel: link.RoleLabel}
	text := fmt.Sprintf("子代理等待审批（approvalId %s）：%s；请在子代理卡片或会话中批准/拒绝。", state.Approval.ID, state.Approval.Preview.Description)
	message, err := appendSubagentMessage(repo, link.UserID, runtime, "child_to_parent", "approval", text, "approval:"+link.ChildRunID+":"+state.Approval.ID)
	if err != nil {
		return err
	}
	return deliverSubagentMessage(repo, link.UserID, link.ParentRunID, runtime, message, text)
}
