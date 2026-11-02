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
	// cloudAgentSubagentCarveParentShare 让首个写型子代理只拿父剩余的一半，
	// 分母为 2+活跃兄弟数，给父运行至少保留一半：避免并行子代理合谋花满
	// 父预算，也避免第二个子代理 spawn 时预算为 0 而创建失败。
	cloudAgentSubagentCarveParentShare = 2
)

type cloudAgentSubagentWritePlan struct {
	Granted       bool
	Reason        string  // 未授予时的机器可读原因；授予时为空
	Permission    string  // 子运行权限模式（授予时继承父模式，否则 read_only）
	CarveCredits  float64 // 从父剩余切分给子运行的积分额度（未授予时 0）
	GenAllowance  int     // 从父剩余生成次数切分给子运行的份额（父不限时 0=不限）
	ParentRemain  float64 // 决策时父当前剩余积分（回执观测用）
	SiblingActive int     // 决策时仍在运行的兄弟子代理数
}

// planSubagentWrite 计算 spawn 的写入授权与预算 carve。
// 父剩余积分 = 父预算 − 父已产生订单 − 活跃子代理 carve 份额 − 已终态子代理实际花费。
// carve 为 0（父无剩余）时不授予写入并返回 budget_exhausted，避免子运行
// 带着 MaxCredits=0 在任务准入处收到误导性报错。查询失败按错误返回，拒绝 spawn。
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
	case state.Request.PermissionMode != "auto" && state.Request.PermissionMode != "request_approval":
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
	holdbacks, err := s.cloudAgentSubagentBudgetHoldbacks(state)
	if err != nil {
		return plan, err
	}
	remaining -= holdbacks
	if remaining < 0 {
		remaining = 0
	}
	plan.ParentRemain = float64(remaining) / float64(CreditScale)
	plan.CarveCredits = float64(remaining) / float64(cloudAgentSubagentCarveParentShare+siblingActive) / float64(CreditScale)
	if input.MaxCredits > 0 && input.MaxCredits < plan.CarveCredits {
		plan.CarveCredits = input.MaxCredits
	}
	if plan.CarveCredits <= 0 {
		plan.Reason = "budget_exhausted"
		return plan, nil
	}
	plan.GenAllowance = planSubagentGenerationAllowance(state, siblingActive)
	plan.Granted = true
	plan.Permission = state.Request.PermissionMode
	return plan, nil
}

// planSubagentGenerationAllowance 按 carve 比例切分父剩余生成次数；
// 父预算不限生成（MaxGenerationTasks=0）时返回 0，子运行沿用不限语义。
func planSubagentGenerationAllowance(state *cloudAgentRuntime, siblingActive int) int {
	if state.Request.Budget.MaxGenerationTasks <= 0 {
		return 0
	}
	remaining := state.Request.Budget.MaxGenerationTasks - state.Generations
	for _, carve := range state.SubagentGenerationCarves {
		remaining -= carve
	}
	allowance := remaining / (cloudAgentSubagentCarveParentShare + siblingActive)
	if allowance < 0 {
		return 0
	}
	return allowance
}

// cloudAgentSubagentBudgetHoldbacks 汇总父运行当前被子代理占用的积分：
// 仍在运行子代理的 carve 份额 + 已终态子代理的实际花费（不归还）。
// 只对父运行有意义（子运行没有自己的子代理）。carve 记账与结算都发生在
// 父运行检查点事务里，这里只做纯汇总。
func (s *Service) cloudAgentSubagentBudgetHoldbacks(state *cloudAgentRuntime) (int64, error) {
	if state == nil || state.Subagent != nil {
		return 0, nil
	}
	var total int64
	for _, credits := range state.SubagentBudgetCarves {
		total += int64(math.Floor(credits * float64(CreditScale)))
	}
	for _, credits := range state.SubagentBudgetSpent {
		total += int64(math.Floor(credits * float64(CreditScale)))
	}
	return total, nil
}

// applySubagentBudgetCarve 在 spawn 回执的同一检查点事务里登记 carve 份额；
// 单独的事务会把父 revision +1，导致随后的回执保存必然 CAS 冲突。
func applySubagentBudgetCarve(state *cloudAgentRuntime, linkID string, plan cloudAgentSubagentWritePlan) {
	if !plan.Granted {
		return
	}
	if state.SubagentBudgetCarves == nil {
		state.SubagentBudgetCarves = map[string]float64{}
	}
	state.SubagentBudgetCarves[linkID] = plan.CarveCredits
	if plan.GenAllowance > 0 {
		if state.SubagentGenerationCarves == nil {
			state.SubagentGenerationCarves = map[string]int{}
		}
		state.SubagentGenerationCarves[linkID] = plan.GenAllowance
	}
}

// settleSubagentBudget 在子代理终态后结算父预算：释放 carve 份额，
// 把子代理实际花费（其任务订单合计）永久计入父 holdback。失败返回错误，
// 由 reconcile 重试直到成功——否则父剩余会永久偏大。
func (s *Service) settleSubagentBudget(userID, parentRunID, linkID string, child *model.CloudAgentExecution) error {
	childState, err := cloudAgentDecode(child)
	if err != nil {
		return err
	}
	orders, err := s.repo.BillingOrdersByTaskIDs(userID, childState.TaskIDs)
	if err != nil {
		return err
	}
	var spentMicro int64
	for _, order := range orders {
		spentMicro += order.AmountMicrocredits
	}
	spent := float64(spentMicro) / float64(CreditScale)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		parent, err := s.repo.CloudAgent(userID, parentRunID)
		if err != nil {
			return err
		}
		err = s.repo.MutateCloudAgent(userID, parentRunID, parent.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
			state, err := cloudAgentDecode(current)
			if err != nil {
				return err
			}
			delete(state.SubagentBudgetCarves, linkID)
			delete(state.SubagentGenerationCarves, linkID)
			if spent > 0 {
				if state.SubagentBudgetSpent == nil {
					state.SubagentBudgetSpent = map[string]float64{}
				}
				state.SubagentBudgetSpent[linkID] = spent
			}
			return cloudAgentSave(current, &state)
		})
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

// surfaceSubagentApproval 把子代理的等待审批状态上浮到父会话：幂等键按
// childRun+approvalID 固定，重复 reconcile 不会重复投递；payload 携带
// 结构化审批字段供前端渲染批准/拒绝入口。
func (s *Service) surfaceSubagentApproval(repo *repository.Repository, link *model.AgentSubagentLink, child *model.CloudAgentExecution) error {
	state, err := cloudAgentDecode(child)
	if err != nil {
		return err
	}
	if state.Approval == nil || state.Approval.Decision != "" {
		return nil
	}
	runtime := &SubagentRuntime{LinkID: link.ID, ParentRunID: link.ParentRunID, ChildRunID: link.ChildRunID, DisplayName: link.DisplayName, RoleLabel: link.RoleLabel}
	text := fmt.Sprintf("子代理等待审批（approvalId %s）：%s；请在子代理会话中批准或拒绝。", state.Approval.ID, state.Approval.Preview.Description)
	extra := map[string]any{"approvalId": state.Approval.ID, "preview": state.Approval.Preview, "quoteHash": state.Approval.QuoteHash, "childRunId": link.ChildRunID}
	message, err := appendSubagentMessageWithExtra(repo, link.UserID, runtime, "child_to_parent", "approval", text, "approval:"+link.ChildRunID+":"+state.Approval.ID, extra)
	if err != nil {
		return err
	}
	return deliverSubagentMessage(repo, link.UserID, link.ParentRunID, runtime, message, text)
}

// cloudAgentParentGenerationAllowance 返回父运行还可提交的生成次数；
// MaxGenerationTasks<=0 表示不限，返回 math.MaxInt32。
func cloudAgentParentGenerationAllowance(state *cloudAgentRuntime) int {
	if state.Request.Budget.MaxGenerationTasks <= 0 {
		return math.MaxInt32
	}
	allowance := state.Request.Budget.MaxGenerationTasks - state.Generations
	for _, carve := range state.SubagentGenerationCarves {
		allowance -= carve
	}
	if allowance < 0 {
		return 0
	}
	return allowance
}
