package app

import (
	"context"
	"strings"
	"testing"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// A9-A/C/D: the full chain on one tree. The user authorizes root work; the
// supervisor delegates drama and ecommerce; the user chats directly with the
// ecommerce executor; its generation is billed to its grant; both report; the
// supervisor sees both reports. Then a crash-and-replay of every step and a
// second user prove nothing is duplicated or shared.
func TestAgentAcceptanceFullChain(t *testing.T) {
	f := newBusinessFixture(t)
	caller := cp.Caller{UserID: "user-a"}
	textModel := cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}

	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "acc-0", Goal: "第一集分镜 + 一套主图", BudgetMicrocredits: 1_000_000, IdempotencyKey: "acceptance", AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, {Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_accept"}}})
	if err != nil {
		t.Fatal(err)
	}
	delegate := func(action, definition string, scope as.Scope) AssignmentView {
		t.Helper()
		view, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "acc-0", ActionKey: action, DefinitionID: definition, Scope: scope, Handoff: action, BudgetMicrocredits: 400_000})
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	drama := delegate("drama", "drama.agent", as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"})
	ecommerce := delegate("ecommerce", "ecommerce.agent", as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_accept"})

	// The user goes straight to the ecommerce executor and changes the brief.
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: ecommerce.ExecutorSessionID, MessageID: "user-edit", Text: "背景改成浅灰"}); err != nil {
		t.Fatal(err)
	}
	f.text.script(toolCallReply("call-img", "ecommerce_generate_image", `{"prompt":"浅灰背景陶瓷杯","channelId":"`+f.channelID+`","model":"`+f.modelKey+`","parameters":{"size":"1024x1024"},"index":0,"count":1}`), textReply("已按浅灰背景生成"))
	// 报价达到审批阈值：第一轮等待批准，用户批准后重放同一操作提交任务。
	if _, err := f.runTurn(t, businessTurn{Assignment: ecommerce, MessageID: "exec-1", Text: "按最新要求生成主图", Model: textModel}); err != nil {
		t.Fatal(err)
	}
	pending, err := f.svc.WorkApprovals("user-a", work.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("approvals: %+v %v", pending, err)
	}
	if _, err := f.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: pending[0].ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: 1}); err != nil {
		t.Fatal(err)
	}
	turn, err := f.runTurn(t, businessTurn{Assignment: ecommerce, MessageID: "exec-1", Text: "按最新要求生成主图", Model: textModel})
	if err != nil || len(turn.Calls) != 1 || turn.Calls[0].Result.Operation == "" {
		t.Fatalf("ecommerce turn: %+v %v", turn, err)
	}
	f.text.script(toolCallReply("call-sum", "drama_summary", `{}`), textReply("项目已读取"))
	if _, err := f.runTurn(t, businessTurn{Assignment: drama, MessageID: "exec-1", Text: "看看项目", Model: textModel}); err != nil {
		t.Fatal(err)
	}

	// Replaying every step (a crash after each) changes nothing.
	calls := f.text.calls.Load()
	if _, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "acc-0", ActionKey: "ecommerce", DefinitionID: "ecommerce.agent", Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_accept"}, Handoff: "ecommerce", BudgetMicrocredits: 400_000}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: ecommerce.ExecutorSessionID, MessageID: "user-edit", Text: "背景改成浅灰"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runTurn(t, businessTurn{Assignment: ecommerce, MessageID: "exec-1", Text: "按最新要求生成主图", Model: textModel}); err != nil {
		t.Fatal(err)
	}
	if f.text.calls.Load() != calls {
		t.Fatalf("replay called the model again: %d -> %d", calls, f.text.calls.Load())
	}
	for table, want := range map[any]int64{&model.AgentWorkItem{}: 3, &model.AgentAssignment{}: 3, &model.AgentWorkInput{}: 1} {
		if got := platformCount(t, f.db, table, "owner_id = ?", "user-a"); got != want {
			t.Fatalf("%T = %d, want %d", table, got, want)
		}
	}
	if got := platformCount(t, f.db, &model.PlatformSubmission{}, "application = ? AND object_type = ?", "ecommerce", "workbench_image"); got != 1 {
		t.Fatalf("image submissions = %d", got)
	}

	// Reports close the loop; the supervisor reads them, not the executors' history.
	for _, assignment := range []AssignmentView{drama, ecommerce} {
		child, _ := f.svc.GetWork("user-a", assignment.WorkItemID)
		if _, err := f.svc.SubmitReport("user-a", SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: child.Version, ReportID: "final", Status: ao.ReportCompleted, Summary: assignment.WorkItemID + " 完成"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.deliverAgentOutbox("user-a", 100); err != nil {
		t.Fatal(err)
	}
	supervisor, err := f.svc.buildAgentSessionContext("user-a", work.SupervisorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	text := agentContextText(supervisor)
	if strings.Count(text, "子工作报告（completed）") != 2 || !strings.Contains(text, "用户直接补充") || strings.Contains(text, "浅灰背景陶瓷杯") {
		t.Fatalf("supervisor context:\n%s", text)
	}

	// Money: one wallet, one charge per order, child spend inside child grants.
	root, err := f.svc.BudgetGrants().Get(context.Background(), caller, work.BudgetGrantID)
	if err != nil {
		t.Fatal(err)
	}
	ecomGrant, err := f.svc.BudgetGrants().Get(context.Background(), caller, ecommerce.BudgetGrantID)
	if err != nil {
		t.Fatal(err)
	}
	// 两个 400k 子授权占用根额度：根可用 = 1M − 800k。
	if ecomGrant.Usage.Spent() == 0 || root.Usage.Spent() < ecomGrant.Usage.Spent() || root.AvailableMicrocredits != 200_000 {
		t.Fatalf("budget tree: root=%+v ecommerce=%+v", root, ecomGrant)
	}
	var orders []model.BillingOrder
	f.db.Where("user_id = ?", "user-a").Find(&orders)
	var charges int64
	f.db.Model(&model.PlatformBudgetCharge{}).Where("owner_id = ? AND billing_order_id <> ''", "user-a").Count(&charges)
	if charges != int64(len(orders)) {
		t.Fatalf("every order belongs to exactly one grant: orders=%d charges=%d", len(orders), charges)
	}

	// A second user shares nothing and can touch nothing.
	if page, err := f.svc.AgentSessions().List(context.Background(), as.Caller{UserID: "user-b"}, as.ListSessionsRequest{}); err != nil || len(page.Sessions) != 0 {
		t.Fatalf("user-b sessions: %+v %v", page, err)
	}
	if _, err := f.svc.GetWork("user-b", work.ID); !ao.IsCode(err, ao.CodeNotFound) {
		t.Fatalf("user-b work read: %v", err)
	}
	if _, err := f.svc.SendSessionMessage("user-b", SendSessionMessageRequest{SessionID: drama.ExecutorSessionID, MessageID: "x", Text: "越权"}); !as.IsCode(err, as.CodeNotFound) {
		t.Fatalf("user-b direct chat: %v", err)
	}

	// Stop: nothing revives, the root budget closes.
	current, _ := f.svc.GetWork("user-a", work.ID)
	if _, err := f.svc.StopWork("user-a", WorkControlRequest{WorkItemID: work.ID, ExpectedVersion: current.Version, Reason: "验收结束"}); err != nil {
		t.Fatal(err)
	}
	if grant, _ := f.svc.BudgetGrants().Get(context.Background(), caller, work.BudgetGrantID); grant.Status != cp.BudgetStatusClosed {
		t.Fatalf("root budget after stop: %+v", grant)
	}
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: ecommerce.ExecutorSessionID, MessageID: "after-stop", Text: "再来一张"}); !ao.IsCode(err, ao.CodeWorkStopped) {
		t.Fatalf("direct chat after stop: %v", err)
	}
}
