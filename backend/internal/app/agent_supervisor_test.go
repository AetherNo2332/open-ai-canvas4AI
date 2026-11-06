package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// driveSupervisor advances pending turns and drains the worker the way the
// background runner would, for the given number of passes.
func driveSupervisor(t *testing.T, f *businessFixture, passes int) {
	t.Helper()
	for range passes {
		if _, err := f.svc.runPendingAgentTurns(10); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
	}
}

// workChildren reads the descendants of one work item.
func workChildren(t *testing.T, f *businessFixture, workID string) []model.AgentWorkItem {
	t.Helper()
	children := []model.AgentWorkItem{}
	err := f.svc.repo.InAgentWork("user-a", "", func(tx *repository.WorkTx) error {
		rows, err := tx.Descendants("user-a", workID)
		if err != nil {
			return err
		}
		children = rows
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return children
}

// supervisorGoal starts a root work with the user's goal message; the first
// supervisor turn commits with the work.
func supervisorGoal(t *testing.T, f *businessFixture, key, goal string, scopes []as.Scope) WorkView {
	t.Helper()
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: key + "-msg", Goal: goal, BudgetMicrocredits: 1_000_000, IdempotencyKey: key,
		AuthorizedScopes: scopes, InitialMessage: goal})
	if err != nil {
		t.Fatal(err)
	}
	return work
}

func delegateCall(actionKey, definitionID, application, spaceType, spaceID, handoff string, budget int64) string {
	raw, _ := json.Marshal(map[string]any{"actionKey": actionKey, "definitionId": definitionID, "application": application,
		"spaceType": spaceType, "spaceId": spaceID, "handoff": handoff, "budgetMicrocredits": budget})
	return string(raw)
}

func dramaDelegateCall(actionKey, spaceID, handoff string, budget int64) string {
	return delegateCall(actionKey, "drama.agent", "drama", "drama_project", spaceID, handoff, budget)
}

// B2-A: the user submits the goal through the public entry; the runner's
// supervisor turn discovers the business and delegates it. The test never
// calls DelegateWork itself.
func TestSupervisorStartsFromUserMessage(t *testing.T) {
	f := newBusinessFixture(t)
	work := supervisorGoal(t, f, "sup-start", "梳理第一集并开始制作",
		[]as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}})
	f.text.script(
		toolCallReply("call-list", "list_business_agents", `{}`),
		toolCallReply("call-del", "delegate_work", dramaDelegateCall("drama", "drama-1", "梳理第一集并开始制作", 300_000)),
		textReply("已派工给短剧 Agent，等待报告。"),
	)
	driveSupervisor(t, f, 10)
	children := workChildren(t, f, work.ID)
	if len(children) != 1 {
		t.Fatalf("runner-delegated children: %+v", children)
	}
	var assignment model.AgentAssignment
	if err := f.db.First(&assignment, "owner_id = ? AND work_item_id = ?", "user-a", children[0].ID).Error; err != nil || assignment.Status != ao.AssignmentRunning {
		t.Fatalf("assignment: %+v %v", assignment, err)
	}
	if assignment.ExecutorSessionID == "" {
		t.Fatal("delegation did not bind an executor session")
	}
	// 重放目标轮次：同一派工键只产生一份子工作，模型也不重跑。
	calls := f.text.calls.Load()
	driveSupervisor(t, f, 4)
	if got := platformCount(t, f.db, &model.AgentWorkItem{}, "owner_id = ? AND parent_work_id = ?", "user-a", work.ID); got != 1 {
		t.Fatalf("replayed goal created another child: %d", got)
	}
	if f.text.calls.Load() != calls {
		t.Fatalf("replay called the model: %d -> %d", calls, f.text.calls.Load())
	}
}

// B2-B: the business report wakes the supervisor once; ten redeliveries of the
// same durable event produce one turn and no follow-up model decision.
func TestSupervisorReportWakeupOnce(t *testing.T) {
	f := newBusinessFixture(t)
	work := supervisorGoal(t, f, "sup-wake", "看一眼项目",
		[]as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}})
	f.text.script(
		toolCallReply("call-del", "delegate_work", dramaDelegateCall("drama", "drama-1", "看项目", 300_000)),
		textReply("已派工，等待报告。"),
	)
	driveSupervisor(t, f, 10)
	children := workChildren(t, f, work.ID)
	if len(children) != 1 {
		t.Fatalf("children: %+v", children)
	}
	assignment, err := f.svc.repo.AgentAssignmentForOwner("user-a", children[0].ActiveAssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	// 业务执行器完成并提交报告；同一报告事件重复投递十次。
	if _, err := f.svc.SubmitReport("user-a", SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: children[0].Version, ReportID: "wake-report", Status: ao.ReportCompleted, Summary: "项目已读完"}); err != nil {
		t.Fatal(err)
	}
	var outbox model.AgentOutbox
	if err := f.db.First(&outbox, "owner_id = ? AND kind = ?", "user-a", "report").Error; err != nil {
		t.Fatal(err)
	}
	driveSupervisor(t, f, 4)
	before := f.text.calls.Load()
	for range 10 {
		if err := f.db.Model(&model.AgentOutbox{}).Where("id = ?", outbox.ID).Updates(map[string]any{"status": "pending", "delivered_at": nil}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.deliverAgentOutbox("user-a", 10); err != nil {
			t.Fatal(err)
		}
	}
	driveSupervisor(t, f, 6)
	if f.text.calls.Load() != before {
		t.Fatalf("redelivery produced new model decisions: %d -> %d", before, f.text.calls.Load())
	}
	var turns int64
	if err := f.db.Model(&model.AgentTurn{}).Where("session_id = ? AND message_id = ?", work.SupervisorSessionID, "outbox:"+outbox.ID).Count(&turns).Error; err != nil || turns != 1 {
		t.Fatalf("report turns = %d, err=%v", turns, err)
	}
	reports, err := f.svc.ListReports("user-a", children[0].ID)
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports: %+v %v", reports, err)
	}
}

// B2-C: while the supervisor waits for its children, no model call happens;
// the completion event resumes it exactly once.
func TestSupervisorWaitsWithoutModelPolling(t *testing.T) {
	f := newBusinessFixture(t)
	work := supervisorGoal(t, f, "sup-wait", "看项目",
		[]as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}})
	f.text.script(
		toolCallReply("call-del", "delegate_work", dramaDelegateCall("drama", "drama-1", "看项目", 300_000)),
		textReply("已派工，等待报告。"),
	)
	driveSupervisor(t, f, 10)
	steady := f.text.calls.Load()
	for range 4 {
		driveSupervisor(t, f, 2)
	}
	if f.text.calls.Load() != steady {
		t.Fatalf("waiting supervisor polled the model: %d -> %d", steady, f.text.calls.Load())
	}
	children := workChildren(t, f, work.ID)
	assignment, err := f.svc.repo.AgentAssignmentForOwner("user-a", children[0].ActiveAssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SubmitReport("user-a", SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: children[0].Version, ReportID: "done-report", Status: ao.ReportCompleted, Summary: "完成"}); err != nil {
		t.Fatal(err)
	}
	f.text.script(textReply("收到报告，工作完成。"))
	driveSupervisor(t, f, 10)
	if f.text.calls.Load() <= steady {
		t.Fatalf("completion event did not resume the supervisor: %d", f.text.calls.Load())
	}
	after := f.text.calls.Load()
	driveSupervisor(t, f, 4)
	if f.text.calls.Load() != after {
		t.Fatalf("resumed supervisor kept polling: %d -> %d", after, f.text.calls.Load())
	}
}

// B2-D: the supervisor's commands are bound to its own root: forged scopes are
// refused, the grant lineage stays on its tree, and a newly registered
// business is discoverable without touching the supervisor core.
func TestSupervisorCommandsBoundToRoot(t *testing.T) {
	f := newBusinessFixture(t)
	work := supervisorGoal(t, f, "sup-bind", "做一页",
		[]as.Scope{{Application: "notebook", SpaceType: "notebook_page", SpaceID: "page-1"}})
	session, err := f.svc.agentSessionForOwner("user-a", work.SupervisorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	// 先注册业务，让伪造空间走到授权栅栏而不是定义查找。
	executed := 0
	if err := f.svc.registerAgentBusiness(testBusinessAdapter{executed: &executed}, as.Definition{
		ID: "notebook.agent", Version: "1", Role: as.RoleBusiness,
		Application: "notebook", SpaceTypes: []string{"notebook_page"}, Title: "Notebook", Tools: []string{"notebook_write"},
	}); err != nil {
		t.Fatal(err)
	}
	invocation := func(name, arguments string) as.ToolInvocation {
		return as.ToolInvocation{OwnerID: "user-a", SessionID: session.ID, Scope: as.Scope{Application: as.ApplicationOrchestration, SpaceType: as.SpaceWork, SpaceID: work.ID},
			WorkItemID: session.WorkItemID, BudgetGrantID: work.BudgetGrantID, CallID: name + "-call", Name: name, Arguments: json.RawMessage(arguments),
			OperationID: "turn:" + session.ID + ":bind:step:1:" + name + "-call"}
	}
	// 伪造未授权空间：派工被授权栅栏拒绝。
	forged := delegateCall("x", "notebook.agent", "notebook", "notebook_page", "page-2", "x", 100_000)
	if _, err := f.svc.executeSupervisorTool(context.Background(), session, invocation("delegate_work", forged)); !ao.IsCode(err, ao.CodeForbidden) {
		t.Fatalf("forged scope delegation: %v", err)
	}
	// 新注册的业务仅通过注册即可被发现与派工。
	listed, err := f.svc.executeSupervisorTool(context.Background(), session, invocation("list_business_agents", `{}`))
	if err != nil || !strings.Contains(listed.Text, "notebook.agent") {
		t.Fatalf("registered business not listed: %+v %v", listed, err)
	}
	allowed := delegateCall("nb", "notebook.agent", "notebook", "notebook_page", "page-1", "写一页", 100_000)
	result, err := f.svc.executeSupervisorTool(context.Background(), session, invocation("delegate_work", allowed))
	if err != nil || result.Operation == "" {
		t.Fatalf("delegation to the registered business: %+v %v", result, err)
	}
	// 派工的授权沿根工作：子 grant 的根就是根工作授权。
	child, err := f.svc.GetWork("user-a", result.Operation)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, child.BudgetGrantID)
	if err != nil || grant.RootID != work.BudgetGrantID {
		t.Fatalf("child grant lineage: %+v %v", grant, err)
	}
	// 停止后的根工作：除读取外的一切命令被拒绝。
	if _, err := f.svc.StopWork("user-a", WorkControlRequest{WorkItemID: work.ID, ExpectedVersion: work.Version, Reason: "stop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.executeSupervisorTool(context.Background(), session, invocation("delegate_work", allowed)); !as.IsCode(err, as.CodeWorkStopped) {
		t.Fatalf("stopped root accepted a delegation: %v", err)
	}
}

// B2-E: restarts, user overrides, reassignments and stops interleave; the
// supervisor resumes from the latest valid work version and stale reports stay
// audit-only.
func TestSupervisorRestartAndUserOverride(t *testing.T) {
	f := newBusinessFixture(t)
	second := newSecondaryAgentService(t, f.db)
	work := supervisorGoal(t, f, "sup-restart", "看项目",
		[]as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}})
	f.text.script(
		toolCallReply("call-del", "delegate_work", dramaDelegateCall("drama", "drama-1", "看项目", 300_000)),
		textReply("已派工。"),
	)
	driveSupervisor(t, f, 10)
	children := workChildren(t, f, work.ID)
	child := children[0]
	executorAssignment, err := f.svc.repo.AgentAssignmentForOwner("user-a", child.ActiveAssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	// 用户直聊执行会话：工作版本推进，总控收到一次变更通知。
	f.text.script(textReply("好的，重点写反派"), textReply("收到用户补充，继续等待报告。"))
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: executorAssignment.ExecutorSessionID, MessageID: "user-add", Text: "重点写反派"}); err != nil {
		t.Fatal(err)
	}
	driveSupervisor(t, f, 10)
	overridden, err := f.svc.GetWork("user-a", child.ID)
	if err != nil || overridden.Version <= child.Version {
		t.Fatalf("user input did not move the work version: %+v %v", overridden, err)
	}
	supervisor, err := f.svc.buildAgentSessionContext("user-a", work.SupervisorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if text := agentContextText(supervisor); strings.Count(text, "用户直接补充") != 1 {
		t.Fatalf("supervisor saw the user override %d times:\n%s", strings.Count(text, "用户直接补充"), text)
	}
	// 改派：旧派工结束；旧报告只剩审计。
	assignment, err := f.svc.repo.AgentAssignmentForOwner("user-a", overridden.ActiveAssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	reassigned, err := f.svc.ReassignWork("user-a", child.ID, overridden.Version)
	if err != nil {
		t.Fatal(err)
	}
	// 旧派工的报告：调用方收到陈旧栅栏错误，报告本身只留审计。
	if _, reportErr := f.svc.SubmitReport("user-a", SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: overridden.Version, ReportID: "stale-report", Status: ao.ReportCompleted, Summary: "旧派工的报告"}); !ao.IsCode(reportErr, ao.CodeStaleAssignment) {
		t.Fatalf("stale report fence: %v", reportErr)
	}
	reports, err := f.svc.ListReports("user-a", child.ID)
	if err != nil {
		t.Fatal(err)
	}
	seenStale := false
	for _, report := range reports {
		if report.ReportID == "stale-report" {
			if report.Applied {
				t.Fatal("stale assignment report was applied")
			}
			seenStale = true
		}
	}
	if !seenStale {
		t.Fatal("stale report was not kept for audit")
	}
	// 重启：新实例从持久事件恢复，用最新版本继续（派工投递 → 执行轮次）。
	f.text.script(textReply("重启后已按最新要求读取项目"))
	for range 8 {
		if _, err := second.runPendingAgentTurns(10); err != nil {
			t.Fatal(err)
		}
		f.drain(t)
	}
	latest, err := f.svc.repo.AgentAssignmentForOwner("user-a", reassigned.ID)
	if err != nil || latest.Epoch != assignment.Epoch+1 {
		t.Fatalf("reassigned assignment: %+v %v", latest, err)
	}
	// 停止：一切新写入被拒绝，队列事件失效。
	if _, err := f.svc.StopWork("user-a", WorkControlRequest{WorkItemID: work.ID, ExpectedVersion: work.Version, Reason: "用户停止"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SendSessionMessage("user-a", SendSessionMessageRequest{SessionID: reassigned.ExecutorSessionID, MessageID: "after-stop", Text: "再改一次"}); err == nil {
		t.Fatal("stopped work accepted a new message")
	}
}

// B2-F: the supervisor may only finish from business facts: a child that has
// merely submitted its work keeps the root open; completion arrives only after
// the child reports completed, and finishing closes the root authorization.
func TestSupervisorFinishesFromBusinessFacts(t *testing.T) {
	f := newBusinessFixture(t)
	work := supervisorGoal(t, f, "sup-finish", "生成一张主图并汇总",
		[]as.Scope{{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_finish"}})
	session, err := f.svc.agentSessionForOwner("user-a", work.SupervisorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	f.text.script(
		toolCallReply("call-del", "delegate_work", delegateCall("e", "ecommerce.agent", "ecommerce", "workbench_batch", "batch_finish", "生成主图", 300_000)),
		textReply("已派工，等待生成结果。"),
	)
	driveSupervisor(t, f, 10)
	children := workChildren(t, f, work.ID)
	if len(children) != 1 {
		t.Fatalf("children: %+v", children)
	}
	// 执行会话提交生成任务（报价达阈值：批准后重放提交）。
	assignment, err := f.svc.repo.AgentAssignmentForOwner("user-a", children[0].ActiveAssignmentID)
	if err != nil {
		t.Fatal(err)
	}
	f.text.script(toolCallReply("call-img", "ecommerce_generate_image", `{"prompt":"白底主图","channelId":"`+f.channelID+`","model":"`+f.modelKey+`","parameters":{"size":"1024x1024"},"index":0,"count":1}`), textReply("已提交生成任务。"))
	if _, err := f.runTurn(t, businessTurn{Assignment: AssignmentView{ID: assignment.ID, WorkItemID: children[0].ID, Epoch: assignment.Epoch, ExecutorSessionID: assignment.ExecutorSessionID, BudgetGrantID: children[0].BudgetGrantID}, MessageID: "exec-img", Text: "生成主图", Model: cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}}); err != nil {
		t.Fatal(err)
	}
	approvals, err := f.svc.WorkApprovals("user-a", work.ID)
	if err != nil || len(approvals) == 0 {
		t.Fatalf("approvals: %+v %v", approvals, err)
	}
	if _, err := f.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: approvals[0].ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.runTurn(t, businessTurn{Assignment: AssignmentView{ID: assignment.ID, WorkItemID: children[0].ID, Epoch: assignment.Epoch, ExecutorSessionID: assignment.ExecutorSessionID, BudgetGrantID: children[0].BudgetGrantID}, MessageID: "exec-img", Text: "生成主图", Model: cp.ModelSelection{ChannelID: f.text.channel, ModelKey: f.text.modelKey}}); err != nil {
		t.Fatal(err)
	}
	f.drain(t)
	// 生成已提交但未报告完成：finish_work 被拒绝，根工作保持 running。
	finishArgs := `{"resultSummary":"主图完成"}`
	finishInvocation := as.ToolInvocation{OwnerID: "user-a", SessionID: session.ID, Scope: as.Scope{Application: as.ApplicationOrchestration, SpaceType: as.SpaceWork, SpaceID: work.ID},
		WorkItemID: session.WorkItemID, BudgetGrantID: work.BudgetGrantID, CallID: "finish-early", Name: "finish_work", Arguments: json.RawMessage(finishArgs),
		OperationID: "turn:" + session.ID + ":finish:step:1:finish-early"}
	if _, err := f.svc.executeSupervisorTool(context.Background(), session, finishInvocation); err == nil || !strings.Contains(err.Error(), "尚未完成") {
		t.Fatalf("early finish: %v", err)
	}
	if done, err := f.svc.GetWork("user-a", work.ID); err != nil || done.Status != ao.WorkRunning {
		t.Fatalf("root finished early: %+v %v", done, err)
	}
	// 生成完成后业务报告 completed：finish_work 通过，根工作转 completed。
	if _, err := f.svc.SubmitReport("user-a", SubmitReportRequest{AssignmentID: assignment.ID, LeaseEpoch: assignment.Epoch, WorkVersion: children[0].Version, ReportID: "final-report", Status: ao.ReportCompleted, Summary: "主图已生成"}); err != nil {
		t.Fatal(err)
	}
	result, err := f.svc.executeSupervisorTool(context.Background(), session, finishInvocation)
	if err != nil {
		t.Fatalf("finish after the report: %v", err)
	}
	if !strings.Contains(result.Text, "已完成") {
		t.Fatalf("finish result: %+v", result)
	}
	done, err := f.svc.GetWork("user-a", work.ID)
	if err != nil || done.Status != ao.WorkCompleted || done.ResultSummary == "" {
		t.Fatalf("completed root: %+v %v", done, err)
	}
	grant, err := f.svc.BudgetGrants().Get(context.Background(), cp.Caller{UserID: "user-a"}, work.BudgetGrantID)
	if err != nil || grant.Status != cp.BudgetStatusClosed {
		t.Fatalf("root grant after finish: %+v %v", grant, err)
	}
	_ = fmt.Sprint()
}
