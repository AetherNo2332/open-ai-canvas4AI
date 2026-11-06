package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	ao "yingce/backend/internal/agentorchestration"
	as "yingce/backend/internal/agentservice"
	cp "yingce/backend/internal/creationplatform"
	"yingce/backend/internal/model"
)

// B3-A: the drama tools go through the existing drama service — a shot edit
// lands where the page reads it, and generating or approving against invalid
// business state is refused by the domain, not by the adapter.
func TestDramaAgentUsesExistingDomainRules(t *testing.T) {
	f := newBusinessFixture(t)
	// 一集一个镜头：与页面相同的业务数据。
	if err := f.db.Create(&model.DramaEpisode{ID: "ep-1", ProjectID: "drama-1", Number: 1, Title: "第一集"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.DramaShot{ID: "shot-1", ProjectID: "drama-1", EpisodeID: "ep-1", Position: 0, Dialogue: "旧台词"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.DramaSegment{ID: "seg-1", ProjectID: "drama-1", EpisodeID: "ep-1", SegmentIndex: 0}).Error; err != nil {
		t.Fatal(err)
	}
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "d3", Goal: "改镜头并出片", BudgetMicrocredits: 1_000_000, IdempotencyKey: "drama-flow",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "d3", ActionKey: "d", DefinitionID: "drama.agent",
		Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, BudgetMicrocredits: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	invocation := func(name, arguments string) as.ToolInvocation {
		return as.ToolInvocation{OwnerID: "user-a", SessionID: assignment.ExecutorSessionID, Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"},
			WorkItemID: assignment.WorkItemID, BudgetGrantID: assignment.BudgetGrantID, AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
			CallID: name + "-1", Name: name, Arguments: json.RawMessage(arguments), OperationID: "turn:" + assignment.ExecutorSessionID + ":flow:step:1:" + name + "-1"}
	}
	// 修改镜头：经 UpdateDramaShot 的业务校验，页面读到同一内容。
	if _, err := f.svc.executeBusinessTool(context.Background(), invocation("drama_update_shot", `{"shotId":"shot-1","dialogue":"新台词"}`)); err != nil {
		t.Fatal(err)
	}
	summary, err := f.svc.DramaSummary("user-a", "drama-1")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, episode := range summary.Episodes {
		if episode.ID == "ep-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("episode missing from the page summary: %+v", summary)
	}
	shot, err := f.svc.repo.DramaShot("drama-1", "shot-1")
	if err != nil || shot.Dialogue != "新台词" {
		t.Fatalf("shot after the agent edit: %+v %v", shot, err)
	}
	// 不存在的剧情段与未完成的分集：业务规则拒绝，而不是适配器放行。
	if _, err := f.svc.executeBusinessTool(context.Background(), invocation("drama_generate_segments", `{"episodeId":"ep-1","segments":[9]}`)); err == nil {
		t.Fatal("invalid segment generation was accepted")
	}
	if _, err := f.svc.executeBusinessTool(context.Background(), invocation("drama_approve_episode", `{"episodeId":"ep-1"}`)); err == nil {
		t.Fatal("approving an unfinished episode was accepted")
	}
	// 伪造空间与跨账户：执行栅栏拒绝。
	foreign := invocation("drama_update_shot", `{"shotId":"shot-1","dialogue":"越权"}`)
	foreign.OwnerID = "user-b"
	if _, err := f.svc.executeBusinessTool(context.Background(), foreign); !as.IsCode(err, as.CodeNotFound) {
		t.Fatalf("foreign owner shot edit: %v", err)
	}
}

// B3-B: the ecommerce batch is durable: product material, preset and tasks
// survive a restart, and retrying failures resubmits only the failed items.
func TestEcommerceAgentDurableBatch(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "e3", Goal: "一套主图", BudgetMicrocredits: 1_000_000, IdempotencyKey: "ecom-batch",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_b3"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "e3", ActionKey: "e", DefinitionID: "ecommerce.agent",
		Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_b3"}, BudgetMicrocredits: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	invocation := func(name, arguments string) as.ToolInvocation {
		return as.ToolInvocation{OwnerID: "user-a", SessionID: assignment.ExecutorSessionID, Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_b3"},
			WorkItemID: assignment.WorkItemID, BudgetGrantID: assignment.BudgetGrantID, AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
			CallID: name + "-1", Name: name, Arguments: json.RawMessage(arguments), OperationID: "turn:" + assignment.ExecutorSessionID + ":batch:step:1:" + name + "-1"}
	}
	if _, err := f.svc.executeBusinessTool(context.Background(), invocation("ecommerce_save_batch", `{"product":"白底陶瓷杯 商品资料 v1","preset":"portrait"}`)); err != nil {
		t.Fatal(err)
	}
	// 报价达到审批阈值：先等一次、批准、再重放同一操作提交任务。
	if _, err := f.svc.executeBusinessTool(context.Background(), invocation("ecommerce_generate_image",
		`{"prompt":"白底陶瓷杯","channelId":"`+f.channelID+`","model":"`+f.modelKey+`","parameters":{"size":"1024x1024"},"index":0,"count":1}`)); err == nil {
		t.Fatal("threshold quote did not wait for approval")
	}
	approvals, err := f.svc.WorkApprovals("user-a", work.ID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals: %+v %v", approvals, err)
	}
	if _, err := f.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: approvals[0].ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: 1}); err != nil {
		t.Fatal(err)
	}
	result, err := f.svc.executeBusinessTool(context.Background(), invocation("ecommerce_generate_image",
		`{"prompt":"白底陶瓷杯","channelId":"`+f.channelID+`","model":"`+f.modelKey+`","parameters":{"size":"1024x1024"},"index":0,"count":1}`))
	if err != nil {
		t.Fatal(err)
	}
	firstTask := strings.TrimPrefix(result.Operation, "task:")
	// 服务重启：新实例读同一库，批次资料与任务关联都在。
	second := newSecondaryAgentService(t, f.db)
	restarted := invocation("ecommerce_batch_status", "{}")
	restarted.SessionID = assignment.ExecutorSessionID
	session, err := second.agentSessionForOwner("user-a", assignment.ExecutorSessionID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := second.executeBusinessTool(context.Background(), func() as.ToolInvocation {
		restarted.OwnerID, restarted.SessionID = "user-a", session.ID
		return restarted
	}())
	if err != nil || !strings.Contains(status.Text, "白底陶瓷杯 商品资料 v1") || !strings.Contains(status.Text, firstTask) {
		t.Fatalf("batch after restart: %+v %v", status, err)
	}
	// 失败项重试：把该任务置为失败后重试，只重试失败项。
	if err := f.db.Model(&model.Task{}).Where("id = ?", firstTask).Update("status", model.TaskStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	retry, err := second.executeBusinessTool(context.Background(), func() as.ToolInvocation {
		call := restarted
		call.Name, call.Arguments, call.CallID = "ecommerce_retry_failed", json.RawMessage(`{}`), "retry-1"
		call.OperationID = "turn:" + assignment.ExecutorSessionID + ":batch:step:2:retry-1"
		return call
	}())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(retry.Text, firstTask) {
		t.Fatalf("retry did not target the failed task: %+v", retry)
	}
	// 未失败时重试：没有任何提交。
	noop, err := second.executeBusinessTool(context.Background(), func() as.ToolInvocation {
		call := restarted
		call.Name, call.Arguments, call.CallID = "ecommerce_retry_failed", json.RawMessage(`{}`), "retry-2"
		call.OperationID = "turn:" + assignment.ExecutorSessionID + ":batch:step:3:retry-2"
		return call
	}())
	if err != nil || !strings.Contains(noop.Text, "没有失败") {
		t.Fatalf("empty retry: %+v %v", noop, err)
	}
}

// B3-C: a submitted task projects onto the work as a progress report once and
// a terminal report once; repeated reconciles stay no-ops.
func TestAgentAsyncOperationReport(t *testing.T) {
	f := newBusinessFixture(t)
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "r3", Goal: "一张图", BudgetMicrocredits: 1_000_000, IdempotencyKey: "reconcile-root",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_rec"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "r3", ActionKey: "e", DefinitionID: "ecommerce.agent",
		Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_rec"}, BudgetMicrocredits: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	call := as.ToolInvocation{OwnerID: "user-a", SessionID: assignment.ExecutorSessionID, Scope: as.Scope{Application: as.ApplicationEcommerce, SpaceType: as.SpaceWorkbenchBatch, SpaceID: "batch_rec"},
		WorkItemID: assignment.WorkItemID, BudgetGrantID: assignment.BudgetGrantID, AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
		CallID: "gen-1", Name: "ecommerce_generate_image",
		Arguments:   json.RawMessage(`{"prompt":"白底","channelId":"` + f.channelID + `","model":"` + f.modelKey + `","parameters":{"size":"1024x1024"},"index":0,"count":1}`),
		OperationID: "turn:" + assignment.ExecutorSessionID + ":rec:step:1:gen-1"}
	// 报价达阈值：先批准再执行。
	if _, err := f.svc.executeBusinessTool(context.Background(), call); err == nil {
		t.Fatal("threshold quote did not wait for approval")
	}
	approvals, err := f.svc.WorkApprovals("user-a", work.ID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("approvals: %+v %v", approvals, err)
	}
	if _, err := f.svc.DecideApproval("user-a", DecideApprovalRequest{ApprovalID: approvals[0].ID, Decision: ao.ApprovalApproved, ExpectedAuthorizationVersion: 1}); err != nil {
		t.Fatal(err)
	}
	result, err := f.svc.executeBusinessTool(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	taskID := strings.TrimPrefix(result.Operation, "task:")
	// 任务排队中：对账产生一次进行中报告；重复对账不新增。
	if err := f.svc.reconcileAgentOperations("user-a"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.reconcileAgentOperations("user-a"); err != nil {
		t.Fatal(err)
	}
	reports, err := f.svc.ListReports("user-a", assignment.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	progressReports := 0
	for _, report := range reports {
		if report.Status == ao.ReportProgress {
			progressReports++
		}
	}
	if progressReports != 1 {
		t.Fatalf("progress reports = %d in %+v", progressReports, reports)
	}
	// 任务完成：对账产生一次完成报告，子工作随之完成。
	f.drain(t)
	if err := f.svc.reconcileAgentOperations("user-a"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.svc.CreationPlatform().Task(context.Background(), cp.Caller{UserID: "user-a"}, taskID)
	if err != nil || snapshot.Execution != cp.ExecutionSucceeded {
		t.Fatalf("task state: %+v %v", snapshot, err)
	}
	reports, err = f.svc.ListReports("user-a", assignment.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	completed := 0
	for _, report := range reports {
		if report.Status == ao.ReportCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("completed reports = %d in %+v", completed, reports)
	}
	child, err := f.svc.GetWork("user-a", assignment.WorkItemID)
	if err != nil || child.Status != ao.WorkCompleted {
		t.Fatalf("child work after the terminal report: %+v %v", child, err)
	}
}

// B3-D: asset references are scoped: a foreign or unknown resource is
// refused, an authorized one is usable and traceable through its receipt.
func TestAgentAssetReferenceScope(t *testing.T) {
	f := newBusinessFixture(t)
	picture := mediaTestPNG(t)
	own, _, err := f.svc.storeResource("user-a", "image", "own.png", "image/png", int64(len(picture)), 2, 3, 0, bytes.NewReader(picture), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	foreign, _, err := f.svc.storeResource("user-b", "image", "foreign.png", "image/png", int64(len(picture)), 2, 3, 0, bytes.NewReader(picture), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.resolveAgentAssetReference("user-a", own.ID); err != nil {
		t.Fatalf("own resource: %v", err)
	}
	if _, err := f.svc.resolveAgentAssetReference("user-a", foreign.ID); !as.IsCode(err, as.CodeNotFound) {
		t.Fatalf("foreign resource: %v", err)
	}
	if _, err := f.svc.resolveAgentAssetReference("user-a", "resource-missing"); !as.IsCode(err, as.CodeNotFound) {
		t.Fatalf("missing resource: %v", err)
	}
	// 通过短剧工具引用：授权引用记录进操作回执，可追溯来源。
	work, err := f.svc.StartWork("user-a", StartWorkRequest{SourceMessageID: "a3", Goal: "引用素材", BudgetMicrocredits: 1_000_000, IdempotencyKey: "asset-root",
		AuthorizedScopes: []as.Scope{{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := f.svc.DelegateWork("user-a", DelegateRequest{WorkItemID: work.ID, SourceMessageID: "a3", ActionKey: "d", DefinitionID: "drama.agent",
		Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}, BudgetMicrocredits: 500_000})
	if err != nil {
		t.Fatal(err)
	}
	operationID := "turn:" + assignment.ExecutorSessionID + ":asset:step:1:ref-1"
	result, err := f.svc.executeBusinessTool(context.Background(), as.ToolInvocation{OwnerID: "user-a", SessionID: assignment.ExecutorSessionID,
		Scope: as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"},
		WorkItemID: assignment.WorkItemID, BudgetGrantID: assignment.BudgetGrantID, AssignmentID: assignment.ID, AssignmentEpoch: assignment.Epoch,
		CallID: "ref-1", Name: "drama_reference_asset", Arguments: json.RawMessage(`{"resourceId":"` + own.ID + `"}`), OperationID: operationID})
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation != own.ID {
		t.Fatalf("reference receipt: %+v", result)
	}
	operation, err := f.svc.repo.AgentOperation("user-a", operationID)
	if err != nil || operation.Status != "succeeded" || operation.OperationRef != own.ID {
		t.Fatalf("durable reference receipt: %+v %v", operation, err)
	}
}
