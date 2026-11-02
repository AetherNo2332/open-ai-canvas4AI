package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 子代理写入权限：授权矩阵、预算 carve 与聚合、审批上浮、工具披露。

func TestSubagentWriteChildKeepsWriteTools(t *testing.T) {
	child := CloudAgentRequest{SubagentEnabled: false, PermissionMode: "auto", ContextScope: []string{"canvas"}, subagent: &SubagentRuntime{LinkID: "link", ParentRunID: "parent", ChildRunID: "child", Depth: 1, WriteGranted: true}}
	for _, tool := range []string{"canvas_apply_ops", "generate_media", "storyboard", "send_parent_message", "finish_subagent"} {
		if !cloudAgentToolAllowed(child, tool) {
			t.Fatalf("write-granted child lost tool %s", tool)
		}
	}
	for _, tool := range []string{"spawn_subagent", "wait_subagents", "subagent_status", "message_subagent", "memory_update"} {
		if cloudAgentToolAllowed(child, tool) {
			t.Fatalf("write-granted child must not receive %s", tool)
		}
	}
	readonly := child
	readonly.subagent = &SubagentRuntime{LinkID: "link", ParentRunID: "parent", ChildRunID: "child", Depth: 1}
	readonly.PermissionMode = "read_only"
	for _, tool := range []string{"canvas_apply_ops", "generate_media", "storyboard"} {
		if cloudAgentToolAllowed(readonly, tool) {
			t.Fatalf("readonly child must not receive %s", tool)
		}
	}
}

func TestSubagentChildRequestWriteMapping(t *testing.T) {
	parent := CloudAgentRequest{SubagentEnabled: true, SubagentWriteEnabled: true, PermissionMode: "auto", ContextScope: []string{"canvas"}}
	parent.Budget.MaxCredits = 1
	parent.Budget.MaxGenerationTasks = 2
	granted := cloudAgentSubagentWritePlan{Granted: true, Permission: "auto", CarveCredits: 0.4, GenAllowance: 1}
	child := dynamicSubagentChildRequest(parent, spawnSubagentInput{MaxSteps: 12}, "key", granted)
	if child.PermissionMode != "auto" || child.Budget.MaxCredits != 0.4 || child.SubagentEnabled || child.SubagentWriteEnabled {
		t.Fatalf("granted mapping wrong: %+v", child.Budget)
	}
	if child.Budget.MaxGenerationTasks != 1 {
		t.Fatalf("generation allowance not applied: %d", child.Budget.MaxGenerationTasks)
	}
	if child.Budget.MaxSteps != 12 {
		t.Fatalf("maxSteps not applied: %d", child.Budget.MaxSteps)
	}
	denied := cloudAgentSubagentWritePlan{Granted: false, Reason: "parent_read_only", Permission: "read_only"}
	child = dynamicSubagentChildRequest(parent, spawnSubagentInput{Write: true}, "key", denied)
	if child.PermissionMode != "read_only" || child.Budget.MaxGenerationTasks != 2 {
		t.Fatalf("denied child must stay read-only and inherit generation budget: %+v", child.Budget)
	}
}

func TestPlanSubagentWriteMatrix(t *testing.T) {
	s, _, parent := piAgentTestFixture(t)
	state, err := cloudAgentDecode(parent)
	if err != nil {
		t.Fatal(err)
	}
	state.Request.SubagentWriteEnabled = true

	readonly := state.Request
	readonly.PermissionMode = "read_only"
	plan, err := s.planSubagentWrite(parent, &cloudAgentRuntime{Request: readonly}, spawnSubagentInput{Write: true}, 0)
	if err != nil || plan.Granted || plan.Reason != "parent_read_only" {
		t.Fatalf("read_only parent must deny writes: %+v %v", plan, err)
	}

	plan, err = s.planSubagentWrite(parent, &cloudAgentRuntime{Request: state.Request}, spawnSubagentInput{Write: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 首个写型子代理拿父剩余（fixture 预算 1 积分）的一半：给父保留一半。
	if !plan.Granted || plan.Permission != "auto" || plan.CarveCredits != 0.5 || plan.ParentRemain != 1 {
		t.Fatalf("granted plan wrong: %+v", plan)
	}

	plan, err = s.planSubagentWrite(parent, &cloudAgentRuntime{Request: state.Request}, spawnSubagentInput{Write: true, MaxCredits: 0.25}, 0)
	if err != nil || !plan.Granted || plan.CarveCredits != 0.25 {
		t.Fatalf("maxCredits must tighten carve: %+v %v", plan, err)
	}

	// 第二个并行写子：分母 2+1=3，carve = 1/3，仍然授予。
	plan, err = s.planSubagentWrite(parent, &cloudAgentRuntime{Request: state.Request}, spawnSubagentInput{Write: true}, 1)
	if err != nil || !plan.Granted || plan.CarveCredits <= 0 {
		t.Fatalf("second parallel write child must still be granted: %+v %v", plan, err)
	}

	plan, err = s.planSubagentWrite(parent, &cloudAgentRuntime{Request: state.Request}, spawnSubagentInput{}, 0)
	if err != nil || plan.Granted || plan.CarveCredits != 0 {
		t.Fatalf("spawn without write stays read-only: %+v %v", plan, err)
	}

	policyDisabled := state.Request
	policyDisabled.SubagentWriteEnabled = false
	plan, err = s.planSubagentWrite(parent, &cloudAgentRuntime{Request: policyDisabled}, spawnSubagentInput{Write: true}, 0)
	if err != nil || plan.Granted || plan.Reason != "canvas_policy_disabled" {
		t.Fatalf("policy-disabled canvas must deny writes: %+v %v", plan, err)
	}

	// 父剩余为 0 时拒绝授予并返回 budget_exhausted，不再让子带着 MaxCredits=0 去撞准入。
	exhausted := state.Request
	exhausted.Budget.MaxCredits = 0
	plan, err = s.planSubagentWrite(parent, &cloudAgentRuntime{Request: exhausted}, spawnSubagentInput{Write: true}, 0)
	if err != nil || plan.Granted || plan.Reason != "budget_exhausted" {
		t.Fatalf("zero remaining must deny with budget_exhausted: %+v %v", plan, err)
	}
}

func TestSubagentCarveAccountingReducesParentRemaining(t *testing.T) {
	s, _, parent := piAgentTestFixture(t)
	state, err := cloudAgentDecode(parent)
	if err != nil {
		t.Fatal(err)
	}
	state.Request.SubagentWriteEnabled = true
	// 活跃兄弟 carve 0.4 + 已终态子实花 0.3 → holdback 0.7。
	held := cloudAgentRuntime{Request: state.Request, SubagentBudgetCarves: map[string]float64{"l1": 0.4}, SubagentBudgetSpent: map[string]float64{"l2": 0.3}}
	plan, err := s.planSubagentWrite(parent, &held, spawnSubagentInput{Write: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 剩余 = 1 − 0.7 = 0.3；首个写型子拿一半 = 0.15。
	if plan.ParentRemain != 0.3 || plan.CarveCredits != 0.15 {
		t.Fatalf("holdbacks must reduce parent remaining: %+v", plan)
	}
}

func TestSubagentGenerationAllowanceCarve(t *testing.T) {
	state := cloudAgentRuntime{Request: CloudAgentRequest{ContextScope: []string{"canvas"}}}
	state.Request.Budget.MaxGenerationTasks = 2
	if got := planSubagentGenerationAllowance(&state, 0); got != 1 {
		t.Fatalf("first child generation allowance = %d, want 1", got)
	}
	if got := planSubagentGenerationAllowance(&state, 1); got != 0 {
		t.Fatalf("with a sibling the second child gets 0 (floor), got %d", got)
	}
	state.SubagentGenerationCarves = map[string]int{"l1": 1}
	if got := cloudAgentParentGenerationAllowance(&state); got != 1 {
		t.Fatalf("parent allowance must subtract active carve: %d", got)
	}
	state.Request.Budget.MaxGenerationTasks = 0
	if got := cloudAgentParentGenerationAllowance(&state); got == 0 {
		t.Fatal("unlimited parent budget must stay unlimited")
	}
}

func TestSpawnWriteSubagentRecordsCarveInReceiptTransaction(t *testing.T) {
	s, db, parentID := dynamicSubagentFixture(t)
	if err := db.Model(&model.AgentSubagentPolicy{}).Where("canvas_id = ?", "agent-canvas").Updates(map[string]any{"write_enabled": true}).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.repo.CloudAgent("user", parentID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Request.SubagentWriteEnabled = true
	state.Request.PermissionMode = "auto"
	if err := s.repo.MutateCloudAgent("user", parentID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, err = s.repo.CloudAgent("user", parentID)
	if err != nil {
		t.Fatal(err)
	}
	state, err = cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	call := dynamicToolCall("spawn_subagent", "spawn-write", `{"name":"写手","role":"写","objective":"修改分镜行","write":true,"maxSteps":6}`)
	result, plan, spawnErr := s.spawnDynamicSubagent(run, &state, call)
	if spawnErr != nil {
		t.Fatal(spawnErr)
	}
	if !plan.Granted || plan.Permission != "auto" || plan.CarveCredits <= 0 || plan.GenAllowance != 0 {
		t.Fatalf("write plan wrong: %+v", plan)
	}
	// 模拟 dispatch 的回执事务：carve 与回执同一检查点保存（B4 修复点）。
	if err := s.repo.MutateCloudAgent("user", parentID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		applySubagentBudgetCarve(&state, result["linkId"].(string), *plan)
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatalf("receipt transaction must not conflict: %v", err)
	}
	link, err := s.repo.AgentSubagentLinkForUser("user", result["linkId"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !link.WriteGranted {
		t.Fatal("link must record writeGranted")
	}
	child, err := s.repo.CloudAgent("user", link.ChildRunID)
	if err != nil {
		t.Fatal(err)
	}
	childState, err := cloudAgentDecode(child)
	if err != nil {
		t.Fatal(err)
	}
	if childState.Request.PermissionMode != "auto" || childState.Request.Budget.MaxCredits != plan.CarveCredits {
		t.Fatalf("child run permission/budget wrong: %s %+v", childState.Request.PermissionMode, childState.Request.Budget)
	}
	latest, err := s.repo.CloudAgent("user", parentID)
	if err != nil {
		t.Fatal(err)
	}
	latestState, err := cloudAgentDecode(latest)
	if err != nil {
		t.Fatal(err)
	}
	if latestState.SubagentBudgetCarves[link.ID] != plan.CarveCredits {
		t.Fatalf("carve not recorded in receipt transaction: %+v", latestState.SubagentBudgetCarves)
	}
	if got, err := s.cloudAgentSubagentBudgetHoldbacks(&latestState); err != nil || int(got) != int(plan.CarveCredits*float64(CreditScale)) {
		t.Fatalf("holdbacks wrong: %d %v", got, err)
	}
}

func TestSettleSubagentBudgetRecordsChildSpend(t *testing.T) {
	s, db, parentID := dynamicSubagentFixture(t)
	parent, err := s.repo.CloudAgent("user", parentID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(parent)
	if err != nil {
		t.Fatal(err)
	}
	state.Request.SubagentWriteEnabled = true
	if err := db.Create(&model.Task{ID: "spend-task", UserID: "user", ProjectID: "agent-canvas", Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: `{"text":""}`}).Error; err != nil {
		t.Fatal(err)
	}
	// 子运行名下 0.2 积分订单。
	if err := db.Create(&model.BillingOrder{ID: "spend-order", UserID: "user", TaskID: "spend-task", AmountMicrocredits: int64(0.2 * float64(CreditScale)), Status: "completed"}).Error; err != nil {
		t.Fatal(err)
	}
	state.SubagentBudgetCarves = map[string]float64{"settle-link": 0.5}
	if err := s.repo.MutateCloudAgent("user", parentID, parent.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	// 子运行用真实 CreateCloudAgentRun 生成，避免手写 StateJSON 触发运行时校验。
	childRequest := agentTestRequest()
	childRequest.IdempotencyKey = "settle-child-run"
	childRequest.PermissionMode = "auto"
	childRequest.SubagentEnabled = false
	childRun, err := s.CreateCloudAgentRun("user", childRequest, "")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.repo.CloudAgent("user", childRun.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 子运行名下登记 0.2 积分订单（挂在子运行的任务上）。
	if err := db.Model(&model.Task{}).Where("id = ?", "spend-task").Update("agent_run_id", child.ID).Error; err != nil {
		t.Fatal(err)
	}
	childState, err := cloudAgentDecode(child)
	if err != nil {
		t.Fatal(err)
	}
	childState.TaskIDs = append(childState.TaskIDs, "spend-task")
	if err := s.repo.MutateCloudAgent("user", child.ID, child.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &childState)
	}); err != nil {
		t.Fatal(err)
	}
	link := &model.AgentSubagentLink{ID: "settle-link", UserID: "user", CanvasID: "agent-canvas", ParentRunID: parentID, ChildRunID: child.ID, IdempotencyKey: "settle-key", DisplayName: "写手", RoleLabel: "写", Objective: "写", Depth: 1, WriteGranted: true, Status: repository.SubagentStatusRunning}
	if err := db.Create(link).Error; err != nil {
		t.Fatal(err)
	}
	// settle 内部会重新解码传入的子运行；先刷新拿到带 TaskIDs 的最新行。
	child, err = s.repo.CloudAgent("user", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.settleSubagentBudget("user", parentID, link.ID, child); err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.CloudAgent("user", parentID)
	if err != nil {
		t.Fatal(err)
	}
	latestState, err := cloudAgentDecode(latest)
	if err != nil {
		t.Fatal(err)
	}
	if _, active := latestState.SubagentBudgetCarves["settle-link"]; active {
		t.Fatal("carve must be released after settle")
	}
	if latestState.SubagentBudgetSpent["settle-link"] != 0.2 {
		t.Fatalf("child spend must be recorded as holdback: %+v", latestState.SubagentBudgetSpent)
	}
}

func TestSurfaceSubagentApprovalDeliversOnce(t *testing.T) {
	s, db, parentID := dynamicSubagentFixture(t)
	if _, err := s.repo.CloudAgent("user", parentID); err != nil {
		t.Fatal(err)
	}
	pendingCall := cloudAgentCall{ID: "appr-call", Type: "function", Function: struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: "canvas_apply_ops", Arguments: `{"ops":[]}`}}
	system, policy, policyErr := compileCloudAgentPolicies(agentTestRequest(), nil, "")
	if policyErr != nil {
		t.Fatal(policyErr)
	}
	childState := cloudAgentRuntime{Request: agentTestRequest(), Policy: policy, Decisions: map[string]string{}, Canonical: cloudAgentCanonical(system, nil, agentTestRequest().Prompt, agentTestRequest()), TaskIDs: []string{"appr-child-task"}, Calls: []cloudAgentCall{pendingCall}, Approval: &cloudAgentApproval{ID: "appr-1", Call: pendingCall, Preview: cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "子代理写入", Description: "子代理准备修改分镜行"}}}
	if err := db.Create(&model.Task{ID: "appr-child-task", UserID: "user", ProjectID: "agent-canvas", Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: `{"text":""}`}).Error; err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(childState)
	if err != nil {
		t.Fatal(err)
	}
	child := &model.CloudAgentExecution{ID: "appr-child", UserID: "user", Status: "waiting_approval", Engine: "pi", Revision: 1, CanvasID: "agent-canvas", ConversationID: "appr-child", StateJSON: string(encoded)}
	if err := db.Create(child).Error; err != nil {
		t.Fatal(err)
	}
	link := &model.AgentSubagentLink{ID: "appr-link", UserID: "user", CanvasID: "agent-canvas", ParentRunID: parentID, ChildRunID: child.ID, IdempotencyKey: "appr-key", DisplayName: "写手", RoleLabel: "写", Objective: "写", Depth: 1, WriteGranted: true, Status: repository.SubagentStatusQueued}
	if err := db.Create(link).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.surfaceSubagentApproval(s.repo, link, child); err != nil {
		t.Fatal(err)
	}
	if err := s.surfaceSubagentApproval(s.repo, link, child); err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.CloudAgent("user", parentID)
	if err != nil {
		t.Fatal(err)
	}
	parentState, err := cloudAgentDecode(latest)
	if err != nil {
		t.Fatal(err)
	}
	approved := 0
	for _, interjection := range parentState.PendingInterjections {
		if interjection.Source == "subagent" && strings.Contains(interjection.Text, "appr-1") {
			approved++
		}
	}
	if approved != 1 {
		t.Fatalf("approval must surface exactly once: %d", approved)
	}
	messages, err := s.repo.AgentSubagentMessages("user", parentID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range messages {
		if message.Kind != "approval" || message.ChildRunID != child.ID {
			continue
		}
		count++
		var payload map[string]any
		if err := json.Unmarshal([]byte(message.PayloadJSON), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["approvalId"] != "appr-1" {
			t.Fatalf("approval payload missing structured approvalId: %+v", payload)
		}
	}
	if count != 1 {
		t.Fatalf("approval message rows must be exactly one: %d", count)
	}
}
