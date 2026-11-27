package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

// collectingGenerationContinuationFixture 造一个"已提交生成、source run 仍在运行"
// 的批次，用于验证失败、取消、停止路径把 collecting 收口为 detached。
func collectingGenerationContinuationFixture(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution, *model.AgentGenerationBatch, string) {
	t.Helper()
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 1, 12
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args)})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	_, state := agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 1 {
		t.Fatalf("expected one submitted generation, got %d", len(state.PendingGenerations))
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchCollecting {
		t.Fatalf("fixture batch is not collecting: %s", batch.Status)
	}
	return s, db, run, batch, state.PendingGenerations[0].TaskID
}

func TestGenerationBatchDetachedWhenSourceCancelled(t *testing.T) {
	s, db, run, batch, taskID := collectingGenerationContinuationFixture(t)
	if err := s.CancelCloudAgent(context.Background(), "user", run.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.AgentGenerationBatchDetached {
		t.Fatalf("cancelled source left batch %s", updated.Status)
	}
	var task model.Task
	if err := db.First(&task, "id = ?", taskID).Error; err != nil {
		t.Fatalf("generation task row lost on cancel: %v", err)
	}
	var orders int64
	if err := db.Model(&model.BillingOrder{}).Count(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if orders == 0 {
		t.Fatal("billing rows lost on cancel")
	}
}

func TestGenerationBatchDetachedAfterSourceFailureAndRestart(t *testing.T) {
	s, db, run, batch, _ := collectingGenerationContinuationFixture(t)
	// 任意非 finish_run 的终态（工具失败、步骤超时、worker 恢复、进程崩溃后由外部
	// 写库）都可能留下 collecting；重启后的第一次派发必须收口。
	if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Update("status", "failed").Error; err != nil {
		t.Fatal(err)
	}
	s, db = reopenGenerationContinuationService(t, s, db)
	if _, err := s.QueueReadyGenerationContinuations(10); err != nil {
		t.Fatal(err)
	}
	updated, err := s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.AgentGenerationBatchDetached {
		t.Fatalf("failed source batch stayed %s after restart", updated.Status)
	}
	// 重复扫描保持幂等。
	if _, err := s.QueueReadyGenerationContinuations(10); err != nil {
		t.Fatal(err)
	}
	again, err := s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil || again.Status != model.AgentGenerationBatchDetached || again.Revision != updated.Revision {
		t.Fatalf("repeat sweep changed detached batch: %+v %v", again, err)
	}
}

func TestGenerationContinuationInstructionSharesPromptBudget(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 1, 12
	source, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, source.ID, "", []cloudAgentCall{agentMediaCall(args)})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	_, state := agentInterjectionState(t, s, source.ID)
	if err := db.Model(&model.Task{}).Where("id = ?", state.PendingGenerations[0].TaskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游拒绝"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePendingCloudAgentGenerations(); err != nil {
		t.Fatal(err)
	}
	limit := generationContinuationInstructionLimit(1)
	if limit >= cloudAgentPromptMaxRunes {
		t.Fatalf("instruction budget does not reserve prompt space: %d", limit)
	}
	// 16000 字指令在旧实现里会被接受，随后拼装结果后必然 400；现在在 finish_run
	// 就被拒绝，source run 不结束、批次保持收集状态。
	tooLong, _ := json.Marshal(map[string]any{"summary": "先收尾", "continuation": map[string]any{
		"when": "all_generations_terminal", "instruction": strings.Repeat("a", cloudAgentPromptMaxRunes),
	}})
	run, state = agentSettleToolStep(t, s, db, run.ID, "finish-too-long", "finish_run", string(tooLong))
	if run.Status == "completed" {
		t.Fatal("over-budget continuation still completed the source run")
	}
	var events []model.CloudAgentEventRecord
	if err := db.Where("user_id = ? AND run_id = ?", "user", run.ID).Order("sequence").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	receipts := run.StateJSON + mustMarshalState(t, state)
	for _, event := range events {
		receipts += event.EventJSON
	}
	if !strings.Contains(receipts, "过长") {
		t.Fatalf("over-budget instruction did not return an actionable field error: %s", truncateRunes(receipts, 600))
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", source.ID)
	if err != nil || batch.Status != model.AgentGenerationBatchCollecting {
		t.Fatalf("rejected continuation changed batch: %+v %v", batch, err)
	}
	// 预算内指令正常登记，拼装后的提示词不超过运行提示词上限。
	fit, _ := json.Marshal(map[string]any{"summary": "先收尾", "continuation": map[string]any{
		"when": "all_generations_terminal", "instruction": strings.Repeat("b", limit),
	}})
	run, _ = agentSettleToolStep(t, s, db, run.ID, "finish-fit", "finish_run", string(fit))
	if run.Status != "completed" {
		t.Fatalf("in-budget continuation was rejected: %s", run.Status)
	}
	if _, err := s.QueueReadyGenerationContinuations(10); err != nil {
		t.Fatal(err)
	}
	queued, err := s.repo.AgentGenerationBatchBySourceRun("user", source.ID)
	if err != nil || queued.Status != model.AgentGenerationBatchQueued {
		t.Fatalf("in-budget continuation not queued: %+v %v", queued, err)
	}
	message, err := s.repo.AgentQueuedMessage("user", queued.ConversationID, queued.QueuedMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if got := runeCount(message.Prompt); got > cloudAgentPromptMaxRunes {
		t.Fatalf("continuation prompt exceeds the shared limit: %d", got)
	}
	if err := validateCloudAgentPrompt(message.Prompt, cloudAgentPromptMaxRunes); err != nil {
		t.Fatalf("continuation prompt would be rejected at admission: %v", err)
	}
}

func TestGenerationContinuationPromptStaysWithinLimitWithManyTasks(t *testing.T) {
	const taskCount = 48
	instruction := strings.Repeat("检", generationContinuationInstructionLimit(taskCount))
	batch := model.AgentGenerationBatch{ID: "batch-many", ExpectedTasks: taskCount, TerminalTasks: taskCount, Instruction: instruction}
	tasks := make([]model.AgentGenerationBatchTask, 0, taskCount)
	results := map[string]json.RawMessage{}
	taskIDs := make([]string, 0, taskCount)
	for index := 0; index < taskCount; index++ {
		// 合法上限的身份信息：80 字符 taskId / nodeId 与 240 字标题。
		taskID := "task-" + fmt.Sprintf("%02d", index) + strings.Repeat("t", 73)
		nodeID := "node-" + fmt.Sprintf("%02d", index) + strings.Repeat("n", 73)
		tasks = append(tasks, model.AgentGenerationBatchTask{BatchID: batch.ID, TaskID: taskID, NodeID: nodeID, Mode: "image", Title: strings.Repeat("镜", 240), Status: model.AgentGenerationBatchTaskTerminal})
		taskIDs = append(taskIDs, taskID)
		outcome, _ := json.Marshal(map[string]any{"status": "succeeded", "summary": strings.Repeat("摘", 600), "error": strings.Repeat("错", 600)})
		results[taskID] = outcome
	}
	encoded, _ := json.Marshal(results)
	batch.ResultsJSON = string(encoded)
	prompt := cloudAgentGenerationContinuationPrompt(batch, tasks)
	if got := runeCount(prompt); got > cloudAgentPromptMaxRunes {
		t.Fatalf("prompt with %d tasks exceeds limit: %d", taskCount, got)
	}
	if err := validateCloudAgentPrompt(prompt, cloudAgentPromptMaxRunes); err != nil {
		t.Fatalf("prompt with %d tasks rejected by admission: %v", taskCount, err)
	}
	// 每个任务都必须能从续接上下文里按 taskId 取回（身份行不允许被省略）。
	for _, taskID := range taskIDs {
		if !strings.Contains(prompt, taskID) {
			t.Fatalf("task %s lost from continuation prompt", taskID)
		}
	}
	if !strings.Contains(prompt, "node-00") {
		t.Fatal("prompt lost node identity")
	}
	if !strings.Contains(prompt, "检") || !strings.Contains(prompt, "不要自动重新提交付费生成") {
		t.Fatal("prompt lost instruction or no-retry contract")
	}
}

func TestContinuationScanRotatesFailedBatchesSoLaterAccountsAdvance(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	_, anchor := queueReadyContinuationForUser(t, s, db, args, "user", "检查本批结果")
	base := time.Now().UTC().Add(-2 * time.Hour)
	// A 账号：64 条 ready 批次占满整个扫描窗口，且随后配额耗尽。
	backlog := make([]model.AgentGenerationBatch, 0, 63)
	for index := 0; index < 63; index++ {
		batch := seedReadyContinuationBatch(t, db, "user", "agent-canvas", anchor.ConversationID,
			fmt.Sprintf("src-backlog-%02d", index), agentTestRequest(), fmt.Sprintf("task-backlog-%02d", index))
		if err := db.Model(&model.AgentGenerationBatch{}).Where("id = ?", batch.ID).
			Updates(map[string]any{"updated_at": base.Add(time.Duration(index) * time.Second)}).Error; err != nil {
			t.Fatal(err)
		}
		backlog = append(backlog, *batch)
	}
	if err := db.Model(&model.AgentGenerationBatch{}).Where("id = ?", anchor.ID).
		Update("updated_at", base.Add(90*time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TaskTextDelta{ID: "quota-fill-scan", UserID: "user", TaskID: "quota-fill-scan", Sequence: 1, ByteCount: gigabytes(policy.Resource.TaskDataGB)}).Error; err != nil {
		t.Fatal(err)
	}
	// B 账号的批次更新时间最新，第一轮窗口装不下。
	seedAgentContinuationAccount(t, db, "user-b", "canvas-b")
	reqB := agentTestRequest()
	reqB.CanvasID, reqB.IdempotencyKey = "canvas-b", "scan-user-b-root"
	rootB, err := s.CreateCloudAgentRun("user-b", reqB, "")
	if err != nil {
		t.Fatal(err)
	}
	runB, err := s.repo.CloudAgent("user-b", rootB.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversationB := firstNonEmpty(runB.ConversationID, runB.ID)
	finishConversationTestRun(t, db, rootB.ID)
	batchB := seedReadyContinuationBatch(t, db, "user-b", "canvas-b", conversationB, rootB.ID, reqB, "task-scan-b")
	if err := db.Model(&model.AgentGenerationBatch{}).Where("id = ?", batchB.ID).Update("updated_at", base.Add(180*time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if queued, err := s.QueueReadyGenerationContinuations(64); err == nil || queued != 0 {
		t.Fatalf("first window should only meet the exhausted account: queued=%d err=%v", queued, err)
	}
	if current, err := s.repo.AgentGenerationBatch("user-b", batchB.ID); err != nil || current.Status != model.AgentGenerationBatchReady {
		t.Fatalf("B must still be outside the first window: %+v %v", current, err)
	}
	// 失败批次让出窗口后，B 在下一轮就能推进；A 的批次保持 ready 等额度。
	if queued, err := s.QueueReadyGenerationContinuations(64); err == nil || queued != 1 {
		t.Fatalf("second window should reach B: queued=%d err=%v", queued, err)
	}
	if current, err := s.repo.AgentGenerationBatch("user-b", batchB.ID); err != nil || current.Status != model.AgentGenerationBatchQueued {
		t.Fatalf("B did not advance after rotation: %+v %v", current, err)
	}
	if current, err := s.repo.AgentGenerationBatch("user", anchor.ID); err != nil || current.Status != model.AgentGenerationBatchReady {
		t.Fatalf("A batch must stay ready while quota is exhausted: %+v %v", current, err)
	}
	for _, batch := range backlog {
		if current, err := s.repo.AgentGenerationBatch("user", batch.ID); err != nil || current.Status != model.AgentGenerationBatchReady {
			t.Fatalf("backlog batch %s changed: %+v %v", batch.ID, current, err)
		}
	}
}

func TestUserStorageUsageCountsQueuedMessagesAndBatches(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	usage := func(userID string) int64 {
		t.Helper()
		got, err := s.repo.UserStorageUsage(userID)
		if err != nil {
			t.Fatal(err)
		}
		return got.TaskBytes
	}
	baseline := usage("user")
	row := &model.AgentQueuedMessage{
		ID: "am-storage-1", UserID: "user", CanvasID: "agent-canvas", ConversationID: "conv-storage",
		IdempotencyKey: "storage-key-0001", Fingerprint: "fp", Mode: "queue", Kind: model.AgentQueuedMessageGenerationContinuation,
		Prompt: strings.Repeat("p", 100), RequestJSON: strings.Repeat("r", 200), LastError: strings.Repeat("e", 10),
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	batch := &model.AgentGenerationBatch{
		ID: "batch-storage-1", UserID: "user", CanvasID: "agent-canvas", ConversationID: "conv-storage",
		SourceRunID: "run-storage-1", Status: model.AgentGenerationBatchReady, Revision: 1,
		Instruction: strings.Repeat("i", 50), RequestJSON: strings.Repeat("q", 60), ResultsJSON: strings.Repeat("s", 70),
	}
	if err := db.Create(batch).Error; err != nil {
		t.Fatal(err)
	}
	want := baseline + 100 + 200 + 10 + 50 + 60 + 70
	if got := usage("user"); got != want {
		t.Fatalf("queue/batch body not counted: got %d want %d", got, want)
	}
	// 批次任务的结果副本同样计入；结算写入、重放不重复、清理后扣减。
	task := &model.AgentGenerationBatchTask{
		BatchID: batch.ID, TaskID: "task-storage-1", UserID: "user", NodeID: "node-storage-1", Mode: "image",
		Status: model.AgentGenerationBatchTaskTerminal, OutcomeJSON: strings.Repeat("o", 90),
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if got := usage("user"); got != want+90 {
		t.Fatalf("batch task outcome not counted: got %d want %d", got, want+90)
	}
	// 真实结算路径（写入 outcome_json 与 results_json）后的增量按两行正文核对。
	pending := &model.AgentGenerationBatchTask{
		BatchID: batch.ID, TaskID: "task-storage-2", UserID: "user", NodeID: "node-storage-2", Mode: "image",
		Status: model.AgentGenerationBatchTaskPending,
	}
	if err := db.Create(pending).Error; err != nil {
		t.Fatal(err)
	}
	beforeSettle := usage("user")
	settled, changed, err := s.repo.SettleAgentGenerationBatchTask("user", batch.ID, pending.TaskID, map[string]any{"status": "succeeded", "summary": strings.Repeat("结", 120)})
	if err != nil || !changed {
		t.Fatalf("settle batch task: %v %v", changed, err)
	}
	var settledTask model.AgentGenerationBatchTask
	if err := db.Where("batch_id = ? AND task_id = ?", batch.ID, pending.TaskID).First(&settledTask).Error; err != nil {
		t.Fatal(err)
	}
	expectedSettle := int64(len(settledTask.OutcomeJSON) + len(settled.ResultsJSON) - 70)
	if got := usage("user") - beforeSettle; got != expectedSettle {
		t.Fatalf("settlement storage delta = %d want %d", got, expectedSettle)
	}
	// 重放同一结算不重复计量。
	afterSettle := usage("user")
	if _, changed, err := s.repo.SettleAgentGenerationBatchTask("user", batch.ID, pending.TaskID, map[string]any{"status": "succeeded", "summary": strings.Repeat("结", 120)}); err != nil || changed {
		t.Fatalf("replayed settlement changed rows: %v %v", changed, err)
	}
	if got := usage("user"); got != afterSettle {
		t.Fatalf("replayed settlement changed usage: %d want %d", got, afterSettle)
	}
	// 跨账号隔离：别人的未消费内部项不占本账号额度。
	if err := db.Create(&model.AgentQueuedMessage{
		ID: "am-storage-2", UserID: "other", CanvasID: "agent-canvas", ConversationID: "conv-other",
		IdempotencyKey: "storage-key-0002", Fingerprint: "fp", Mode: "queue", Kind: model.AgentQueuedMessageGenerationContinuation,
		Prompt: strings.Repeat("x", 500), RequestJSON: strings.Repeat("y", 500),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if got := usage("user"); got != afterSettle {
		t.Fatalf("other account's queue leaked into usage: %d", got)
	}
	// 消费/清理后同一份正文不再占用配额。
	if err := db.Where("batch_id = ?", batch.ID).Delete(&model.AgentGenerationBatchTask{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", row.ID).Delete(&model.AgentQueuedMessage{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", batch.ID).Delete(&model.AgentGenerationBatch{}).Error; err != nil {
		t.Fatal(err)
	}
	if got := usage("user"); got != baseline {
		t.Fatalf("consumed continuation still counted: got %d want %d", got, baseline)
	}
}

func TestDispatchContinuesAfterContinuationQuotaRejection(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	source, batchA := queueReadyContinuationForUser(t, s, db, args, "user", "检查本批结果")
	// A 账号任务历史额度已满：续接入队必须失败，但不能挡住 B。
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TaskTextDelta{ID: "quota-fill", UserID: "user", TaskID: "quota-fill", Sequence: 1, ByteCount: gigabytes(policy.Resource.TaskDataGB)}).Error; err != nil {
		t.Fatal(err)
	}
	seedAgentContinuationAccount(t, db, "user-b", "canvas-b")
	reqB := agentTestRequest()
	reqB.CanvasID, reqB.IdempotencyKey = "canvas-b", "user-b-root-key"
	rootB, err := s.CreateCloudAgentRun("user-b", reqB, "")
	if err != nil {
		t.Fatal(err)
	}
	messageInput := queueTestInput(rootB.ID, "user-b-message", "queue")
	messageInput.Request = reqB
	messageInput.Request.IdempotencyKey = "user-b-message"
	messageInput.Request.Prompt = "user-b-message"
	if _, err := s.SendAgentConversationMessage("user-b", rootB.ID, messageInput); err != nil {
		t.Fatal(err)
	}
	runB, err := s.repo.CloudAgent("user-b", rootB.ID)
	if err != nil {
		t.Fatal(err)
	}
	conversationB := firstNonEmpty(runB.ConversationID, runB.ID)
	finishConversationTestRun(t, db, rootB.ID)
	batchB := seedReadyContinuationBatch(t, db, "user-b", "canvas-b", conversationB, rootB.ID, reqB, "task-b")
	usageBefore, err := s.repo.UserStorageUsage("user-b")
	if err != nil {
		t.Fatal(err)
	}
	queueBefore := agentQueuedMessageByKey(t, db, "user-b", "user-b-message")
	if err != nil {
		t.Fatal(err)
	}
	dispatched, dispatchErr := s.DispatchAgentMessages(10)
	if dispatchErr == nil {
		t.Fatal("A 的配额错误没有回传")
	}
	if !strings.Contains(dispatchErr.Error(), "任务历史数据") {
		t.Fatalf("A 的真实错误被替换: %v", dispatchErr)
	}
	if dispatched < 1 {
		t.Fatalf("B 的普通消息没有推进: dispatched=%d", dispatched)
	}
	blockedA, err := s.repo.AgentGenerationBatch("user", batchA.ID)
	if err != nil || blockedA.Status != model.AgentGenerationBatchReady {
		t.Fatalf("A 批次没有保持 ready 等待重试: %+v %v", blockedA, err)
	}
	queuedB, err := s.repo.AgentGenerationBatch("user-b", batchB.ID)
	if err != nil || queuedB.Status != model.AgentGenerationBatchQueued {
		t.Fatalf("B 的续接没有入队: %+v %v", queuedB, err)
	}
	messageB := agentQueuedMessageByKey(t, db, "user-b", "user-b-message")
	if messageB.Status != "started" {
		t.Fatalf("B 的普通消息没有启动: %s", messageB.Status)
	}
	// 准入时扣掉了队列正文：新增只等于任务正文减去队列正文，不重复计费配额。
	latest, err := s.repo.LatestAgentConversationRun("user-b", conversationB)
	if err != nil {
		t.Fatal(err)
	}
	var task model.Task
	if err := db.First(&task, "id = ?", latest.ID).Error; err != nil {
		t.Fatal(err)
	}
	incoming := int64(len(task.Prompt) + len(task.InputJSON) + len(task.Error) - len(queueBefore.RequestJSON) - len(queueBefore.Prompt) - len(queueBefore.LastError))
	// 同一轮里 B 的内部续接消息也已入队，它同样计入累计存储。
	var continuationRow model.AgentQueuedMessage
	if err := db.Where("user_id = ? AND kind = ?", "user-b", model.AgentQueuedMessageGenerationContinuation).First(&continuationRow).Error; err != nil {
		t.Fatal(err)
	}
	incoming += int64(len(continuationRow.Prompt) + len(continuationRow.RequestJSON) + len(continuationRow.LastError))
	usageAfter, err := s.repo.UserStorageUsage("user-b")
	if err != nil {
		t.Fatal(err)
	}
	if usageAfter.TaskBytes-usageBefore.TaskBytes != incoming {
		t.Fatalf("admission storage delta = %d want %d", usageAfter.TaskBytes-usageBefore.TaskBytes, incoming)
	}
	// 用户轮次结束后，B 的续接仍能继续推进，A 依旧只等自己的额度。
	finishConversationTestRun(t, db, latest.ID)
	if n, err := s.DispatchAgentMessages(10); n != 1 || err == nil || !strings.Contains(err.Error(), "任务历史数据") {
		t.Fatalf("continuation dispatch = %d, %v", n, err)
	}
	consumedB, err := s.repo.AgentGenerationBatch("user-b", batchB.ID)
	if err != nil || consumedB.Status != model.AgentGenerationBatchConsumed || consumedB.ContinuationRunID == "" {
		t.Fatalf("B 的续接没有消费批次: %+v %v", consumedB, err)
	}
	blockedA, err = s.repo.AgentGenerationBatch("user", batchA.ID)
	if err != nil || blockedA.Status != model.AgentGenerationBatchReady {
		t.Fatalf("A 批次被错误推进: %+v %v", blockedA, err)
	}
	_ = source
}

// queueReadyContinuationForUser 造一个 source run 已通过 finish_run 登记续接、
// 批次处于 ready 的账号。
func queueReadyContinuationForUser(t *testing.T, s *Service, db *gorm.DB, args cloudAgentMediaArgs, userID, instruction string) (*model.CloudAgentExecution, *model.AgentGenerationBatch) {
	t.Helper()
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 1, 12
	source, err := s.CreateCloudAgentRun(userID, req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, source.ID, "", []cloudAgentCall{agentMediaCall(args)})
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	_, state := agentInterjectionState(t, s, source.ID)
	if err := db.Model(&model.Task{}).Where("id = ?", state.PendingGenerations[0].TaskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游拒绝"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePendingCloudAgentGenerations(); err != nil {
		t.Fatal(err)
	}
	finish, _ := json.Marshal(map[string]any{"summary": "已提交生成。", "continuation": map[string]any{
		"when": model.AgentGenerationContinuationAllTerminal, "instruction": instruction,
	}})
	run, _ = agentSettleToolStep(t, s, db, source.ID, "finish-continuation", "finish_run", string(finish))
	if run.Status != "completed" {
		t.Fatalf("source run did not finish: %s", run.Status)
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun(userID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchReady {
		t.Fatalf("fixture batch is not ready: %+v", batch)
	}
	return run, batch
}

func seedAgentContinuationAccount(t *testing.T, db *gorm.DB, userID, canvasID string) {
	t.Helper()
	for _, row := range []any{
		&model.User{ID: userID, Username: userID, Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.CreditAccount{UserID: userID, AvailableMicrocredits: 10_000},
		&model.CanvasProject{ID: canvasID, UserID: userID, PayloadJSON: `{"nodes":[],"connections":[]}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func seedReadyContinuationBatch(t *testing.T, db *gorm.DB, userID, canvasID, conversationID, sourceRunID string, req CloudAgentRequest, taskID string) *model.AgentGenerationBatch {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	batch := &model.AgentGenerationBatch{
		ID: "batch-" + sourceRunID, UserID: userID, CanvasID: canvasID, ConversationID: conversationID,
		SourceRunID: sourceRunID, Status: model.AgentGenerationBatchReady,
		TriggerWhen: model.AgentGenerationContinuationAllTerminal, Instruction: "检查B批次结果",
		RequestJSON: string(raw), ExpectedTasks: 1, TerminalTasks: 1, Revision: 2, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(batch).Error; err != nil {
		t.Fatal(err)
	}
	outcome, _ := json.Marshal(map[string]any{"status": "failed", "error": "上游拒绝"})
	results, _ := json.Marshal(map[string]json.RawMessage{taskID: outcome})
	if err := db.Model(batch).Update("results_json", string(results)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AgentGenerationBatchTask{
		BatchID: batch.ID, TaskID: taskID, UserID: userID, NodeID: "node-" + taskID, Mode: "image",
		Status: model.AgentGenerationBatchTaskTerminal, OutcomeJSON: string(outcome), CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return batch
}

func agentQueuedMessageByKey(t *testing.T, db *gorm.DB, userID, key string) *model.AgentQueuedMessage {
	t.Helper()
	var row model.AgentQueuedMessage
	if err := db.Where("user_id = ? AND idempotency_key = ?", userID, key).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return &row
}

func mustMarshalState(t *testing.T, state cloudAgentRuntime) string {
	raw, err := json.Marshal(state.Calls)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
