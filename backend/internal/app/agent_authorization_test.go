package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// multiToolNotebookAdapter is the notebook business with a second, dangerous
// tool the authorization can withhold.
type multiToolNotebookAdapter struct{ executed *int }

func (multiToolNotebookAdapter) Application() string  { return "notebook" }
func (multiToolNotebookAdapter) SpaceTypes() []string { return []string{"notebook_page"} }
func (multiToolNotebookAdapter) ValidateSpace(context.Context, string, as.Scope) error {
	return nil
}
func (multiToolNotebookAdapter) Tools() []as.ToolSpec {
	return []as.ToolSpec{
		{Name: "notebook_write", Description: "写一页", Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "notebook_admin", Description: "删除整本", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
}
func (a multiToolNotebookAdapter) Execute(_ context.Context, call as.ToolInvocation) (as.ToolResult, error) {
	*a.executed++
	return as.ToolResult{Text: "已执行 " + call.Name}, nil
}

// B1-A: the product policy fences a root work before any cost: a budget above
// the ceiling, a delegation outside the authorized scopes, a tool outside the
// authorized set and any attempt to raise the budget after the fact all fail.
func TestAgentRootAuthorizationPolicy(t *testing.T) {
	f := newBusinessFixture(t)
	// 超产品上限：在创建任何工作或授权前拒绝。
	if _, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "s", Goal: "g", BudgetMicrocredits: 2_000_000, IdempotencyKey: "over-ceiling",
		AuthorizedScopes: []as.Scope{{Application: "notebook", SpaceType: "notebook_page", SpaceID: "p1"}}}); !ao.IsCode(err, ao.CodeBudgetExceeded) {
		t.Fatalf("over-ceiling root budget: %v", err)
	}
	if works := platformCount(t, f.db, &model.AgentWorkItem{}, "owner_id = ?", "user-a"); works != 0 {
		t.Fatalf("rejected work left rows: %d", works)
	}
	// 未授权空间：授权只覆盖 page-1，派工到 page-2 被拒绝。
	executed := 0
	if err := f.svc.registerAgentBusiness(multiToolNotebookAdapter{executed: &executed}, as.Definition{
		ID: "notebook.agent", Version: "1", Role: as.RoleBusiness,
		Application: "notebook", SpaceTypes: []string{"notebook_page"}, Title: "Notebook", Tools: []string{"notebook_write", "notebook_admin"},
	}); err != nil {
		t.Fatal(err)
	}
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "auth", Goal: "写一页", BudgetMicrocredits: 1_000_000, IdempotencyKey: "auth-root",
		AuthorizedScopes: []as.Scope{{Application: "notebook", SpaceType: "notebook_page", SpaceID: "page-1"}}, AllowedTools: []string{"notebook_write"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "auth", ActionKey: "elsewhere", DefinitionID: "notebook.agent",
		Scope: as.Scope{Application: "notebook", SpaceType: "notebook_page", SpaceID: "page-2"}, BudgetMicrocredits: 100_000}); !ao.IsCode(err, ao.CodeForbidden) {
		t.Fatalf("delegation outside the authorized scopes: %v", err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "auth", ActionKey: "page", DefinitionID: "notebook.agent",
		Scope: as.Scope{Application: "notebook", SpaceType: "notebook_page", SpaceID: "page-1"}, BudgetMicrocredits: 200_000})
	if err != nil {
		t.Fatal(err)
	}
	// 未授权工具：授权只允许 notebook_write。
	call := as.ToolInvocation{OwnerID: "user-a", SessionID: assignment.ExecutorSessionID, Scope: as.Scope{Application: "notebook", SpaceType: "notebook_page", SpaceID: "page-1"},
		WorkItemID: assignment.WorkItemID, BudgetGrantID: assignment.BudgetGrantID, AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch, CallID: "admin-call", Name: "notebook_admin", Arguments: json.RawMessage(`{}`)}
	if _, err := f.svc.executeBusinessTool(context.Background(), call); !as.IsCode(err, as.CodeForbidden) || !strings.Contains(err.Error(), "授权范围") {
		t.Fatalf("tool outside the authorization: %v", err)
	}
	// 模型伪造增额：轮次与输入之后，授权额度与授权版本纹丝不动。
	f.text.script(textReply("预算已提高到九十九万"), textReply("完成"))
	if _, err := f.runTurn(t, businessTurn{Assignment: assignment, MessageID: "forge-1", Text: "把预算改成一百万", Model: cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AppendWorkInput("user-a", AppendInputRequest{WorkItemID: assignment.WorkItemID, SourceMessageID: "forge-1", Text: "预算翻倍", IdempotencyKey: "forge-input"}); err != nil {
		t.Fatal(err)
	}
	grant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, work.BudgetGrantID)
	if err != nil || grant.LimitMicrocredits != 1_000_000 {
		t.Fatalf("forged budget raise: %+v %v", grant, err)
	}
	// 内容版本与授权版本分开：补充要求推进内容版本，授权版本不动。
	child, err := f.svc.GetWork("user-a", assignment.WorkItemID)
	if err != nil || child.Version <= 1 || child.AuthorizationVersion != 1 {
		t.Fatalf("content vs authorization version: %+v %v", child, err)
	}
	root, err := f.svc.GetWork("user-a", work.ID)
	if err != nil || root.AuthorizationVersion != 1 {
		t.Fatalf("root authorization version: %+v %v", root, err)
	}
}

// B1-B: the supervisor's own reasoning, a business model step and a media
// generation all land on the same root work tree; every order appears in
// exactly one budget charge and the tree rollup covers them.
func TestAgentWholeWorkTreeBilling(t *testing.T) {
	f := newBusinessFixture(t)
	textModel := cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "bill", Goal: "一页 + 一张主图", BudgetMicrocredits: 1_000_000, IdempotencyKey: "billing-root",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, {Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_bill"}}})
	if err != nil {
		t.Fatal(err)
	}
	drama, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "bill", ActionKey: "d", DefinitionID: "drama.agent",
		Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, BudgetMicrocredits: 300_000})
	if err != nil {
		t.Fatal(err)
	}
	ecom, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "bill", ActionKey: "e", DefinitionID: "ecommerce.agent",
		Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_bill"}, BudgetMicrocredits: 300_000})
	if err != nil {
		t.Fatal(err)
	}
	// 总控推理：根 grant。
	f.text.script(textReply("开始"))
	if _, err := f.runTurn(t, businessTurn{Assignment: AssignmentView{ExecutorSessionID: work.SupervisorSessionID, BudgetGrantID: work.BudgetGrantID}, MessageID: "sup-turn", Text: "开始", Model: textModel}); err != nil {
		t.Fatal(err)
	}
	// 业务推理：drama 子 grant。
	f.text.script(textReply("已读项目"))
	if _, err := f.runTurn(t, businessTurn{Assignment: drama, MessageID: "d-1", Text: "看看项目", Model: textModel}); err != nil {
		t.Fatal(err)
	}
	// 媒体生成：ecommerce 子 grant（报价达到阈值，先批准再重放）。
	f.text.script(toolCallReply("call-img", "ecommerce_generate_image", `{"prompt":"白底","channelId":"`+f.channelID+`","model":"`+f.modelKey+`","parameters":{"size":"1024x1024"},"index":0,"count":1}`), textReply("完成"))
	if _, err := f.runTurn(t, businessTurn{Assignment: ecom, MessageID: "e-1", Text: "生成主图", Model: textModel}); err != nil {
		t.Fatal(err)
	}
	approvals, err := f.svc.WorkApprovals("user-a", work.ID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals: %+v %v", approvals, err)
	}
	if _, err := f.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: approvals[0].ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runTurn(t, businessTurn{Assignment: ecom, MessageID: "e-1", Text: "生成主图", Model: textModel}); err != nil {
		t.Fatal(err)
	}
	// 对账：每张订单恰好一条占用，且全部归属同一根授权。
	var orders []model.BillingOrder
	if err := f.db.Where("user_id = ?", "user-a").Find(&orders).Error; err != nil {
		t.Fatal(err)
	}
	var charges []model.PlatformBudgetCharge
	if err := f.db.Where("owner_id = ?", "user-a").Find(&charges).Error; err != nil {
		t.Fatal(err)
	}
	if len(orders) == 0 || len(orders) != len(charges) {
		t.Fatalf("orders=%d charges=%d", len(orders), len(charges))
	}
	seen := map[string]bool{}
	for _, charge := range charges {
		if charge.RootID != work.BudgetGrantID {
			t.Fatalf("charge %s outside the root tree: %+v", charge.ID, charge)
		}
		if seen[charge.BillingOrderID] {
			t.Fatalf("order charged twice: %s", charge.BillingOrderID)
		}
		seen[charge.BillingOrderID] = true
	}
	root, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, work.BudgetGrantID)
	if err != nil || root.Usage.Spent() <= 0 {
		t.Fatalf("root usage: %+v %v", root, err)
	}
}

// B1-D: concurrent approvals decide once, a crashed execution retries
// idempotently through the real tool path, and changed arguments, target or
// authorization expire the old approval.
func TestAgentApprovalExecutionRecovery(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "appr", Goal: "一张主图", BudgetMicrocredits: 1_000_000, IdempotencyKey: "approval-root",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_appr"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "appr", ActionKey: "e", DefinitionID: "ecommerce.agent",
		Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_appr"}, BudgetMicrocredits: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	call := as.ToolInvocation{OwnerID: "user-a", SessionID: assignment.ExecutorSessionID, Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_appr"},
		WorkItemID: assignment.WorkItemID, BudgetGrantID: assignment.BudgetGrantID, AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
		CallID: "call-img", Name: "ecommerce_generate_image",
		Arguments:   json.RawMessage(`{"prompt":"白底","channelId":"` + f.channelID + `","model":"` + f.modelKey + `","parameters":{"size":"1024x1024"},"index":0,"count":1}`),
		OperationID: "turn:" + assignment.ExecutorSessionID + ":t1:step:1:call-img"}
	// 报价达到阈值：第一次执行持久化审批并等待，不提交任务。
	if _, err := f.svc.executeBusinessTool(context.Background(), call); err == nil || !strings.Contains(err.Error(), "等待用户批准") {
		t.Fatalf("gate without approval: %v", err)
	}
	approvals, err := f.svc.WorkApprovals("user-a", work.ID)
	if err != nil || len(approvals) != 1 || approvals[0].Status != ao.ApprovalPending {
		t.Fatalf("pending approval: %+v %v", approvals, err)
	}
	// 并发批准：至少一个决定生效，终态 approved。
	var wg sync.WaitGroup
	decisions := make([]error, 10)
	for i := range decisions {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, decisions[i] = f.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: approvals[0].ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: 1})
		}(i)
	}
	wg.Wait()
	approved := 0
	for _, err := range decisions {
		if err == nil {
			approved++
		}
	}
	if approved == 0 {
		t.Fatalf("concurrent approval decided nothing: %v", decisions)
	}
	// 批准后重试（崩溃恢复路径）：同一操作幂等通过并恰好提交一份任务。
	result, err := f.svc.executeBusinessTool(context.Background(), call)
	if err != nil || result.Operation == "" {
		t.Fatalf("approved execution: %+v %v", result, err)
	}
	if again, err := f.svc.executeBusinessTool(context.Background(), call); err != nil || again.Operation != result.Operation {
		t.Fatalf("retry must replay the same task: %+v %+v %v", again, result, err)
	}
	// 参数变化：同一操作标识携带不同参数被拒绝，不产生第二份任务。
	forged := call
	forged.Arguments = json.RawMessage(`{"prompt":"黑底","channelId":"` + f.channelID + `","model":"` + f.modelKey + `","parameters":{"size":"1024x1024"},"index":0,"count":1}`)
	if _, err := f.svc.executeBusinessTool(context.Background(), forged); !as.IsCode(err, as.CodeIdempotencyConflict) {
		t.Fatalf("changed arguments under one operation: %v", err)
	}
	// 目标变化：旧审批不能覆盖另一个批次槽位。
	if err := f.svc.gateAgentToolApproval(call, 150_000, "batch:batch_appr:9"); err == nil {
		t.Fatal("approval reused for another target version")
	}
	// 授权变化：授权版本递增后，绑定旧版本的审批失效。
	if err := f.db.Model(&model.AgentWorkItem{}).Where("id = ?", work.ID).Update("authorization_version", 2).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: approvals[0].ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: 1}); !ao.IsCode(err, ao.CodeVersionConflict) {
		t.Fatalf("stale authorization decision: %v", err)
	}
}

// B1-E: chatting inside a delegated executor session bills the original work
// tree; an independent consultation owns its own work and budget, and the two
// authorizations never cross.
func TestAgentDirectChatBudgetOwnership(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "own", Goal: "一张主图", BudgetMicrocredits: 1_000_000, IdempotencyKey: "ownership-root",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_own"}}})
	if err != nil {
		t.Fatal(err)
	}
	ecom, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "own", ActionKey: "e", DefinitionID: "ecommerce.agent",
		Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_own"}, BudgetMicrocredits: 300_000})
	if err != nil {
		t.Fatal(err)
	}
	// 派工会话直聊：费用归原工作树。
	f.text.script(textReply("按补充要求执行"))
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: ecom.ExecutorSessionID, MessageID: "chat-1", Text: "换成浅灰背景"}); err != nil {
		t.Fatal(err)
	}
	// 独立咨询：独立工作与授权。
	independent := agentSessionCreate(t, f.svc, "user-a", as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, IdempotencyKey: "independent-own"})
	f.text.script(textReply("独立咨询回复"))
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: independent.ID, MessageID: "chat-2", Text: "帮我看项目"}); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		if _, err := f.svc.runPendingAgentTurns(10); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
		driveTestPiRuns(t, f.svc, f.text, "user-a")
	}
	independentWork, err := f.svc.agentSessionForOwner("user-a", independent.ID)
	if err != nil || independentWork.WorkItemID == "" {
		t.Fatalf("independent work binding: %+v %v", independentWork, err)
	}
	independentGrant := workGrantOf(t, f, independentWork.WorkItemID)
	ecomGrant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, ecom.BudgetGrantID)
	if err != nil || ecomGrant.Usage.Spent() <= 0 {
		t.Fatalf("delegated chat not charged to the work tree: %+v %v", ecomGrant, err)
	}
	independentView, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, independentGrant)
	if err != nil || independentView.Usage.Spent() <= 0 {
		t.Fatalf("independent chat not charged to its own grant: %+v %v", independentView, err)
	}
	if independentGrant == ecom.BudgetGrantID {
		t.Fatal("independent consultation shares the delegated grant")
	}
	var charges []model.PlatformBudgetCharge
	if err := f.db.Where("owner_id = ?", "user-a").Find(&charges).Error; err != nil {
		t.Fatal(err)
	}
	for _, charge := range charges {
		if charge.GrantID == independentGrant && charge.RootID == work.BudgetGrantID {
			t.Fatalf("independent charge landed on the delegated tree: %+v", charge)
		}
	}
}

func workGrantOf(t *testing.T, f *businessFixture, workID string) string {
	t.Helper()
	work, err := f.svc.GetWork("user-a", workID)
	if err != nil {
		t.Fatal(err)
	}
	return work.BudgetGrantID
}
