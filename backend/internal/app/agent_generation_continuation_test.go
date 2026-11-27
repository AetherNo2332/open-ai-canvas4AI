package app

import (
	"strings"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

func readyGenerationContinuationFixture(t *testing.T) (*Service, *gorm.DB, *model.CloudAgentExecution, *model.AgentGenerationBatch) {
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
	if err := db.Model(&model.Task{}).Where("id = ?", state.PendingGenerations[0].TaskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游拒绝"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePendingCloudAgentGenerations(); err != nil {
		t.Fatal(err)
	}
	finish := `{"summary":"已提交视频生成。","continuation":{"when":"all_generations_terminal","instruction":"检查本批结果，失败项只报告原因，不自动重新生成"}}`
	run, _ = agentSettleToolStep(t, s, db, root.ID, "finish-continuation", "finish_run", finish)
	if run.Status != "completed" {
		t.Fatalf("source run did not finish: %s", run.Status)
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchReady {
		t.Fatalf("fixture batch is not ready: %+v", batch)
	}
	return s, db, run, batch
}

func TestGenerationContinuationQueuesOneInternalMessage(t *testing.T) {
	s, _, run, batch := readyGenerationContinuationFixture(t)
	queued, err := s.QueueReadyGenerationContinuations(10)
	if err != nil || queued != 1 {
		t.Fatalf("queue ready = %d, %v", queued, err)
	}
	updated, err := s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != model.AgentGenerationBatchQueued || updated.QueuedMessageID == "" {
		t.Fatalf("batch was not queued: %+v", updated)
	}
	message, err := s.repo.AgentQueuedMessage("user", updated.ConversationID, updated.QueuedMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.Kind != model.AgentQueuedMessageGenerationContinuation || message.IdempotencyKey != "generation-batch:"+batch.ID {
		t.Fatalf("internal message identity: %+v", message)
	}
	if !strings.Contains(message.Prompt, "系统生成结果") || !strings.Contains(message.Prompt, "上游拒绝") || !strings.Contains(message.Prompt, "不自动重新生成") {
		t.Fatalf("continuation prompt lost results or instruction: %s", message.Prompt)
	}
	view, err := s.AgentMessageQueue("user", updated.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Items) != 0 {
		t.Fatalf("internal message leaked into the user queue: %+v", view)
	}
	again, err := s.QueueReadyGenerationContinuations(10)
	if err != nil || again != 0 {
		t.Fatalf("duplicate ready scan = %d, %v", again, err)
	}
	_ = run
}

func TestGenerationContinuationWaitsForUserRunAndConsumesBatch(t *testing.T) {
	s, db, run, batch := readyGenerationContinuationFixture(t)
	if _, err := s.QueueReadyGenerationContinuations(10); err != nil {
		t.Fatal(err)
	}
	conversation := batch.ConversationID
	if _, err := s.SendAgentConversationMessage("user", conversation, queueTestInput(run.ID, "user-before-continuation", "queue")); err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.LatestAgentConversationRun("user", conversation)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ID == run.ID {
		t.Fatal("user message did not start its own run")
	}
	queuedBatch, err := s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil || queuedBatch.Status != model.AgentGenerationBatchQueued {
		t.Fatalf("continuation must wait behind the user run: %+v %v", queuedBatch, err)
	}
	finishConversationTestRun(t, db, latest.ID)
	if n, err := s.DispatchAgentMessages(10); err != nil || n != 1 {
		t.Fatalf("continuation dispatch = %d, %v", n, err)
	}
	consumed, err := s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if consumed.Status != model.AgentGenerationBatchConsumed || consumed.ContinuationRunID == "" {
		t.Fatalf("continuation run did not consume the batch: %+v", consumed)
	}
	if consumed.ContinuationRunID == latest.ID || consumed.ContinuationRunID == run.ID {
		t.Fatalf("continuation reused an existing run: %s", consumed.ContinuationRunID)
	}
	var runs int64
	db.Model(&model.CloudAgentExecution{}).Count(&runs)
	if runs != 3 {
		t.Fatalf("expected source, user and one continuation run, got %d", runs)
	}
}
