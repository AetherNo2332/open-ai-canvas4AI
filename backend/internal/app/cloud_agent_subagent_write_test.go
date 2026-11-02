package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 子代理写入权限 P0：授权矩阵、预算 carve、审批上浮、工具披露。

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
	granted := cloudAgentSubagentWritePlan{Granted: true, Permission: "auto", CarveCredits: 0.4}
	child := dynamicSubagentChildRequest(parent, spawnSubagentInput{MaxSteps: 12}, "key", granted)
	if child.PermissionMode != "auto" || child.Budget.MaxCredits != 0.4 || child.SubagentEnabled || child.SubagentWriteEnabled {
		t.Fatalf("granted mapping wrong: %+v", child.Budget)
	}
	if child.Budget.MaxSteps != 12 {
		t.Fatalf("maxSteps not applied: %d", child.Budget.MaxSteps)
	}
	denied := cloudAgentSubagentWritePlan{Granted: false, Reason: "parent_read_only", Permission: "read_only"}
	child = dynamicSubagentChildRequest(parent, spawnSubagentInput{Write: true}, "key", denied)
	if child.PermissionMode != "read_only" {
		t.Fatalf("denied child must stay read-only: %s", child.PermissionMode)
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
	// fixture run: SubagentWriteEnabled 已打开，父预算 1 积分、无订单、无兄弟 → 全额 carve。
	if !plan.Granted || plan.Permission != "auto" || plan.CarveCredits != 1 || plan.ParentRemain != 1 {
		t.Fatalf("granted plan wrong: %+v", plan)
	}

	plan, err = s.planSubagentWrite(parent, &cloudAgentRuntime{Request: state.Request}, spawnSubagentInput{Write: true, MaxCredits: 0.25}, 0)
	if err != nil || !plan.Granted || plan.CarveCredits != 0.25 {
		t.Fatalf("maxCredits must tighten carve: %+v %v", plan, err)
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
}

func TestSubagentCarveAccountingReducesParentRemaining(t *testing.T) {
	s, db, parent := piAgentTestFixture(t)
	state, err := cloudAgentDecode(parent)
	if err != nil {
		t.Fatal(err)
	}
	// 活跃子代理 carve 0.4 积分：链接行 queued + 子运行 running。
	if err := db.Create(&model.CloudAgentExecution{ID: "carve-child", UserID: "user", Status: "running", Engine: "pi", Revision: 1, CanvasID: parent.CanvasID, ConversationID: "carve-child", StateJSON: `{"version":1,"request":{"canvasId":"agent-canvas","prompt":"x"}}`}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AgentSubagentLink{ID: "carve-link", UserID: "user", CanvasID: parent.CanvasID, ParentRunID: parent.ID, ChildRunID: "carve-child", IdempotencyKey: "carve-key", DisplayName: "写手", RoleLabel: "写", Objective: "写", Depth: 1, WriteGranted: true, Status: repository.SubagentStatusQueued}).Error; err != nil {
		t.Fatal(err)
	}
	state.SubagentBudgetCarves = map[string]float64{"carve-link": 0.4}
	if got := s.cloudAgentActiveSubagentCarves("user", &state); got != int64(0.4*float64(CreditScale)) {
		t.Fatalf("active carve not counted: %d", got)
	}
	// 子代理终态后链接状态更新，份额释放。
	if err := db.Model(&model.AgentSubagentLink{}).Where("id = ?", "carve-link").Update("status", repository.SubagentStatusCompleted).Error; err != nil {
		t.Fatal(err)
	}
	if got := s.cloudAgentActiveSubagentCarves("user", &state); got != 0 {
		t.Fatalf("settled carve must be released: %d", got)
	}
}

func TestSurfaceSubagentApprovalDeliversOnce(t *testing.T) {
	s, db, parent := piAgentTestFixture(t)
	// 子运行：request_approval 且挂着待审批卡。
	system, policy, err := compileCloudAgentPolicies(agentTestRequest(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	pendingCall := cloudAgentCall{ID: "appr-call", Type: "function", Function: struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}{Name: "canvas_apply_ops", Arguments: `{"ops":[]}`}}
	childState := cloudAgentRuntime{Request: agentTestRequest(), Policy: policy, Decisions: map[string]string{}, Canonical: cloudAgentCanonical(system, nil, agentTestRequest().Prompt, agentTestRequest())}
	childState.TaskIDs = []string{"appr-child-task"}
	childState.Calls = []cloudAgentCall{pendingCall}
	childState.Approval = &cloudAgentApproval{ID: "appr-1", Call: pendingCall, Preview: cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "子代理写入", Description: "子代理准备修改分镜行"}}
	if err := db.Create(&model.Task{ID: "appr-child-task", UserID: "user", ProjectID: parent.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: `{"text":""}`}).Error; err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(childState)
	if err != nil {
		t.Fatal(err)
	}
	child := &model.CloudAgentExecution{ID: "appr-child", UserID: "user", Status: "waiting_approval", Engine: "pi", Revision: 1, CanvasID: parent.CanvasID, ConversationID: "appr-child", StateJSON: string(encoded)}
	if err := db.Create(child).Error; err != nil {
		t.Fatal(err)
	}
	link := &model.AgentSubagentLink{ID: "appr-link", UserID: "user", CanvasID: parent.CanvasID, ParentRunID: parent.ID, ChildRunID: child.ID, IdempotencyKey: "appr-key", DisplayName: "写手", RoleLabel: "写", Objective: "写", Depth: 1, WriteGranted: true, Status: repository.SubagentStatusQueued}
	if err := db.Create(link).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.surfaceSubagentApproval(s.repo, link, child); err != nil {
		t.Fatal(err)
	}
	if err := s.surfaceSubagentApproval(s.repo, link, child); err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.CloudAgent("user", parent.ID)
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
	messages, err := s.repo.AgentSubagentMessages("user", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range messages {
		if message.Kind == "approval" && message.ChildRunID == child.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("approval message rows must be exactly one: %d", count)
	}
}
