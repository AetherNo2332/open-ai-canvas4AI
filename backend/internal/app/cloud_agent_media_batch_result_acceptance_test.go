package app

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// canvasVideoTaskCounts 只统计画布媒体任务及其计费单，避免把 Agent 模型步的订单算进批次额度。
func canvasVideoTaskCounts(t *testing.T, db *gorm.DB) (int64, int64) {
	t.Helper()
	var tasks, orders int64
	if err := db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.BillingOrder{}).
		Where("task_id IN (?)", db.Model(&model.Task{}).Select("id").Where("type = ?", "canvas_video")).
		Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	return tasks, orders
}

// 一次审批后的批量任务必须各自独立结算：一条成功不能把另一条失败标成成功，
// 重复审批和重复推进也不能创建第二任务或第二笔计费。
func TestCloudAgentMediaBatchMixedOutcomesStayIndependentAndReplaySafe(t *testing.T) {
	s, db := agentMediaBatchFixture(t)
	run, state := agentMediaBatchRun(t, s, agentMediaBatchArgs("v1", "v2"), 4)
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CloudAgentRun("user", run.ID)
	if err != nil || waiting.Status != "waiting_approval" || waiting.Approval == nil {
		t.Fatalf("batch should wait for one approval: status=%v err=%v", waiting.Status, err)
	}
	if tasks, orders := canvasVideoTaskCounts(t, db); tasks != 0 || orders != 0 {
		t.Fatalf("unapproved batch created tasks/orders: tasks=%d orders=%d", tasks, orders)
	}

	// 同一审批重复提交必须幂等：第二次不能开启新的批次或产生任务。
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", ""); err != nil {
		t.Fatalf("identical approval replay should be idempotent: %v", err)
	}
	if tasks, orders := canvasVideoTaskCounts(t, db); tasks != 0 || orders != 0 {
		t.Fatalf("approval replay created tasks/orders: tasks=%d orders=%d", tasks, orders)
	}

	ctx := context.Background()
	_, approved := agentMediaBatchState(t, s, run.ID)
	if _, err := s.PiToolAdvance("user", run.ID, "worker-a", approved.PiToolBatchTaskID, approved.Calls[0].ID); err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 2; step++ {
		if _, err := s.stepCloudAgentMediaBatch(ctx, "user", run.ID); err != nil {
			t.Fatal(err)
		}
	}
	tasks, orders := canvasVideoTaskCounts(t, db)
	if tasks != 2 || orders != 2 {
		t.Fatalf("expected exactly one task/order per approved item: tasks=%d orders=%d", tasks, orders)
	}
	_, submitted := agentMediaBatchState(t, s, run.ID)
	if len(submitted.MediaBatch.Items) != 2 {
		t.Fatalf("batch items changed: %+v", submitted.MediaBatch.Items)
	}
	firstTaskID, secondTaskID := submitted.MediaBatch.Items[0].taskID(), submitted.MediaBatch.Items[1].taskID()
	if firstTaskID == "" || secondTaskID == "" || firstTaskID == secondTaskID {
		t.Fatalf("batch items must own distinct tasks: first=%q second=%q", firstTaskID, secondTaskID)
	}
	for index, item := range submitted.MediaBatch.Items {
		nodeID := []string{"v1", "v2"}[index]
		meta, _ := agentCanvasNode(t, s, nodeID)["metadata"].(map[string]any)
		if stringValue(meta["taskId"]) != item.taskID() {
			t.Fatalf("node %s not bound to its own task: meta=%+v item=%+v", nodeID, meta, item)
		}
	}

	// 推进一轮不应重复提交已经 submitted 的条目。
	if _, err := s.stepCloudAgentMediaBatch(ctx, "user", run.ID); err != nil {
		t.Fatal(err)
	}
	if tasks, orders := canvasVideoTaskCounts(t, db); tasks != 2 || orders != 2 {
		t.Fatalf("replaying submitted items created more work: tasks=%d orders=%d", tasks, orders)
	}

	if err := db.Create(&model.Resource{ID: "batch-mixed-output", UserID: "user", Kind: "video", Status: model.ResourceStatusReady, MimeType: "video/mp4", Width: 720, Height: 1280}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", firstTaskID).Updates(map[string]any{
		"status": model.TaskStatusSucceeded, "result_json": `{"mode":"video","video":{"storageKey":"resource:batch-mixed-output"}}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", secondTaskID).Updates(map[string]any{
		"status": model.TaskStatusFailed, "error": "上游拒绝该镜头",
	}).Error; err != nil {
		t.Fatal(err)
	}

	done := false
	for step := 0; step < 6 && !done; step++ {
		if done, err = s.stepCloudAgentMediaBatch(ctx, "user", run.ID); err != nil {
			t.Fatal(err)
		}
	}
	if !done {
		t.Fatal("batch did not finish after both items became terminal")
	}
	latestRun, settled := agentMediaBatchState(t, s, run.ID)
	if settled.MediaBatch.Phase != cloudAgentMediaBatchPhaseDone {
		t.Fatalf("batch phase = %s, want done", settled.MediaBatch.Phase)
	}
	if settled.MediaBatch.Items[0].Status != cloudAgentMediaBatchItemSucceeded || settled.MediaBatch.Items[1].Status != cloudAgentMediaBatchItemFailed {
		t.Fatalf("mixed outcomes were not kept independent: %+v", settled.MediaBatch.Items)
	}
	if settled.MediaBatch.Items[0].TaskIDs[0] != firstTaskID || settled.MediaBatch.Items[1].TaskIDs[0] != secondTaskID {
		t.Fatalf("settlement rewrote item ownership: %+v", settled.MediaBatch.Items)
	}

	successMeta, _ := agentCanvasNode(t, s, "v1")["metadata"].(map[string]any)
	if successMeta["status"] != "success" || successMeta["storageKey"] != "resource:batch-mixed-output" || successMeta["taskStatus"] != string(model.TaskStatusSucceeded) {
		t.Fatalf("successful item did not write its own result: %+v", successMeta)
	}
	failureMeta, _ := agentCanvasNode(t, s, "v2")["metadata"].(map[string]any)
	if failureMeta["status"] != "error" || failureMeta["taskStatus"] != string(model.TaskStatusFailed) || stringValue(failureMeta["errorDetails"]) == "" {
		t.Fatalf("failed item did not get its own failure state: %+v", failureMeta)
	}
	if stringValue(failureMeta["storageKey"]) != "" {
		t.Fatalf("failed item was marked as if it had an artifact: %+v", failureMeta)
	}

	receipt, ok := agentBatchToolMessage(&settled, "batch-call")
	if !ok || !strings.Contains(receipt, "成功 1 / 2") || !strings.Contains(receipt, "失败 #2") || !strings.Contains(receipt, "上游拒绝该镜头") {
		t.Fatalf("batch receipt lost mixed counts: %s", receipt)
	}
	if tasks, orders := canvasVideoTaskCounts(t, db); tasks != 2 || orders != 2 {
		t.Fatalf("settlement created or removed billed work: tasks=%d orders=%d", tasks, orders)
	}

	// 终态后再次投递不应重新结算任何条目。
	var snapshotsBefore int64
	if err := db.Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", "agent-canvas").Count(&snapshotsBefore).Error; err != nil {
		t.Fatal(err)
	}
	if again, err := s.settleFinishedCloudAgentBatchItem(latestRun, &settled); err != nil || again {
		t.Fatalf("terminal batch accepted another result delivery: settled=%v err=%v", again, err)
	}
	if tasks, orders := canvasVideoTaskCounts(t, db); tasks != 2 || orders != 2 {
		t.Fatalf("duplicate delivery changed billed work: tasks=%d orders=%d", tasks, orders)
	}
	var snapshotsAfter int64
	if err := db.Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", "agent-canvas").Count(&snapshotsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if snapshotsAfter != snapshotsBefore {
		t.Fatalf("duplicate delivery added canvas snapshots: %d -> %d", snapshotsBefore, snapshotsAfter)
	}
}

// 单项媒体报价超过本轮剩余积分时，必须在 CreateTask 前拒绝：没有任务、没有计费单，
// 回执把可纠正原因交回模型，而不是错误扣费或登记一个待回写生成。
func TestCloudAgentAdmissionMediaQuoteOverBudgetCreatesNoTaskOrCharge(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	run, state := agentMediaPiRun(t, s, db, args, "auto")
	orders, err := s.repo.BillingOrdersByTaskIDs("user", state.TaskIDs)
	if err != nil {
		t.Fatal(err)
	}
	var spent int64
	for _, order := range orders {
		spent += order.AmountMicrocredits
	}
	state.Request.Budget.MaxCredits = float64(spent+1) / float64(CreditScale)
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}

	var beforeOrders int64
	if err := db.Model(&model.BillingOrder{}).Count(&beforeOrders).Error; err != nil {
		t.Fatal(err)
	}
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	latest, after := agentInterjectionState(t, s, run.ID)
	if latest.Status == "failed" {
		t.Fatalf("correctable over-budget admission should not fail the run: %s", latest.FailureMessage)
	}
	if after.MediaTaskID != "" || len(after.PendingGenerations) != 0 {
		t.Fatalf("rejected media call registered work: task=%q pending=%d", after.MediaTaskID, len(after.PendingGenerations))
	}
	message := cloudAgentToolMessageForTest(t, after, "media-call")
	if !strings.Contains(message, "积分上限") && !strings.Contains(message, "积分预算") {
		t.Fatalf("over-budget receipt missing actionable reason: %s", message)
	}
	if tasks, _ := canvasVideoTaskCounts(t, db); tasks != 0 {
		t.Fatalf("over-budget media created %d tasks", tasks)
	}
	var afterOrders int64
	if err := db.Model(&model.BillingOrder{}).Count(&afterOrders).Error; err != nil {
		t.Fatal(err)
	}
	if afterOrders != beforeOrders {
		t.Fatalf("over-budget media changed billing orders: %d -> %d", beforeOrders, afterOrders)
	}
}
