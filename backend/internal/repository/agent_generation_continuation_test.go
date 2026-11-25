package repository

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"yingce/backend/internal/model"
)

func agentGenerationBatchRepository(t *testing.T) (*Repository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "generation-batch.db")+"?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.AgentGenerationBatch{}, &model.AgentGenerationBatchTask{}); err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { _ = conn.Close() })
	return New(db), db
}

func registerAgentGenerationTask(t *testing.T, repo *Repository, taskID string) *model.AgentGenerationBatch {
	t.Helper()
	return registerAgentGenerationTaskForRun(t, repo, "source-run", taskID)
}

func registerAgentGenerationTaskForRun(t *testing.T, repo *Repository, sourceRun, taskID string) *model.AgentGenerationBatch {
	t.Helper()
	batch, inserted, err := repo.EnsureAgentGenerationBatchTask(model.AgentGenerationBatch{
		ID: "batch-" + sourceRun, UserID: "user", CanvasID: "canvas", ConversationID: "conversation", SourceRunID: sourceRun,
	}, model.AgentGenerationBatchTask{TaskID: taskID, UserID: "user", NodeID: "node-" + taskID, Mode: "video", Title: taskID})
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatalf("task %s was not registered", taskID)
	}
	return batch
}

func TestAgentGenerationBatchUsesActualDynamicTaskCount(t *testing.T) {
	repo, _ := agentGenerationBatchRepository(t)
	for _, taskID := range []string{"one", "two", "three"} {
		batch := registerAgentGenerationTask(t, repo, taskID)
		if batch.ExpectedTasks == 0 {
			t.Fatalf("expected task count must follow registrations: %+v", batch)
		}
	}
	batch, err := repo.AgentGenerationBatchBySourceRun("user", "source-run")
	if err != nil {
		t.Fatal(err)
	}
	if batch.ExpectedTasks != 3 || batch.TerminalTasks != 0 || batch.Status != "collecting" {
		t.Fatalf("dynamic batch count mismatch: %+v", batch)
	}
	// Duplicate registration of the same task is idempotent and never inflates the barrier.
	again, inserted, err := repo.EnsureAgentGenerationBatchTask(model.AgentGenerationBatch{
		ID: "ignored", UserID: "user", CanvasID: "canvas", ConversationID: "conversation", SourceRunID: "source-run",
	}, model.AgentGenerationBatchTask{TaskID: "two", UserID: "user", NodeID: "node-two", Mode: "video", Title: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if inserted || again.ExpectedTasks != 3 {
		t.Fatalf("duplicate registration changed the barrier: inserted=%v expected=%d", inserted, again.ExpectedTasks)
	}
}

func TestAgentGenerationBatchWaitsForEveryRegisteredTask(t *testing.T) {
	repo, _ := agentGenerationBatchRepository(t)
	registerAgentGenerationTask(t, repo, "one")
	registerAgentGenerationTask(t, repo, "two")
	registerAgentGenerationTask(t, repo, "three")
	batch, err := repo.ArmAgentGenerationBatch("user", "batch-source-run", "all_generations_terminal", "continue", `{"prompt":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != "armed" {
		t.Fatalf("arm status = %s", batch.Status)
	}
	for index, taskID := range []string{"one", "two"} {
		batch, changed, err := repo.SettleAgentGenerationBatchTask("user", batch.ID, taskID, map[string]any{"taskId": taskID, "status": "succeeded"})
		if err != nil {
			t.Fatal(err)
		}
		if !changed || batch.TerminalTasks != index+1 || batch.Status != "armed" {
			t.Fatalf("partial settlement advanced the barrier: %+v changed=%v", batch, changed)
		}
	}
	// Duplicate settlement is idempotent and must not consume another slot.
	batch, changed, err := repo.SettleAgentGenerationBatchTask("user", batch.ID, "one", map[string]any{"taskId": "one", "status": "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	if changed || batch.TerminalTasks != 2 {
		t.Fatalf("duplicate settlement changed the batch: %+v changed=%v", batch, changed)
	}
	batch, changed, err = repo.SettleAgentGenerationBatchTask("user", batch.ID, "three", map[string]any{"taskId": "three", "status": "failed", "error": "upstream"})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || batch.TerminalTasks != 3 || batch.Status != "ready" {
		t.Fatalf("last failure must reach the same barrier: %+v changed=%v", batch, changed)
	}
	var results map[string]json.RawMessage
	if err := json.Unmarshal([]byte(batch.ResultsJSON), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || results["three"] == nil {
		t.Fatalf("aggregate results lost an outcome: %s", batch.ResultsJSON)
	}
}

func TestAgentGenerationBatchSingleTaskAndArmAfterSettlement(t *testing.T) {
	repo, _ := agentGenerationBatchRepository(t)
	batch := registerAgentGenerationTask(t, repo, "only")
	if batch.ExpectedTasks != 1 {
		t.Fatalf("single task batch expected=%d", batch.ExpectedTasks)
	}
	if _, _, err := repo.SettleAgentGenerationBatchTask("user", batch.ID, "only", map[string]any{"taskId": "only", "status": "succeeded"}); err != nil {
		t.Fatal(err)
	}
	armed, err := repo.ArmAgentGenerationBatch("user", batch.ID, "all_generations_terminal", "continue", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if armed.Status != "ready" {
		t.Fatalf("arming after the last terminal task must become ready: %+v", armed)
	}
}

func TestAgentGenerationBatchDetachedAndExactlyOnceQueue(t *testing.T) {
	repo, _ := agentGenerationBatchRepository(t)
	registerAgentGenerationTaskForRun(t, repo, "detached-run", "one")
	if err := repo.DetachAgentGenerationBatch("user", "batch-detached-run"); err != nil {
		t.Fatal(err)
	}
	batch, err := repo.AgentGenerationBatch("user", "batch-detached-run")
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != "detached" {
		t.Fatalf("detach status = %s", batch.Status)
	}
	registerAgentGenerationTaskForRun(t, repo, "queued-run", "two")
	registerAgentGenerationTaskForRun(t, repo, "queued-run", "three")
	registerAgentGenerationTaskForRun(t, repo, "queued-run", "four")
	batch, err = repo.ArmAgentGenerationBatch("user", "batch-queued-run", "all_generations_terminal", "continue", "{}")
	if err != nil {
		t.Fatal(err)
	}
	for _, taskID := range []string{"two", "three", "four"} {
		if _, _, err := repo.SettleAgentGenerationBatchTask("user", batch.ID, taskID, map[string]any{"taskId": taskID, "status": "succeeded"}); err != nil {
			t.Fatal(err)
		}
	}
	batch, err = repo.QueueReadyAgentGenerationBatch("user", batch.ID, "message-1")
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != "queued" || batch.QueuedMessageID != "message-1" {
		t.Fatalf("queue transition = %+v", batch)
	}
	// A repeated ready scan must observe queued, not enqueue a second message.
	queued, err := repo.ReadyAgentGenerationBatches(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("queued batch remained ready: %+v", queued)
	}
	if err := repo.ConsumeAgentGenerationBatchByMessage("user", "message-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	batch, err = repo.AgentGenerationBatch("user", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != "consumed" || batch.ContinuationRunID != "run-1" {
		t.Fatalf("consume transition = %+v", batch)
	}
}

func TestAgentGenerationBatchRejectsRegistrationAfterArm(t *testing.T) {
	repo, _ := agentGenerationBatchRepository(t)
	registerAgentGenerationTask(t, repo, "one")
	if _, err := repo.ArmAgentGenerationBatch("user", "batch-source-run", "all_generations_terminal", "continue", "{}"); err != nil {
		t.Fatal(err)
	}
	_, _, err := repo.EnsureAgentGenerationBatchTask(model.AgentGenerationBatch{
		ID: "ignored", UserID: "user", CanvasID: "canvas", ConversationID: "conversation", SourceRunID: "source-run",
	}, model.AgentGenerationBatchTask{TaskID: "late", UserID: "user", NodeID: "late", Mode: "video", Title: "late"})
	if err == nil {
		t.Fatal("a closed batch accepted a late task")
	}
}
