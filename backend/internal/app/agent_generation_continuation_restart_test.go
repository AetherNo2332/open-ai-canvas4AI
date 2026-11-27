package app

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

func TestGenerationContinuationRestartArmedReadyQueuedAndDuplicateSettlement(t *testing.T) {
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
	if len(state.PendingGenerations) != 1 {
		t.Fatalf("submissions=%d", len(state.PendingGenerations))
	}
	taskID := state.PendingGenerations[0].TaskID
	run, _ = agentSettleToolStep(t, s, db, source.ID, "finish-before-terminal", "finish_run", `{"summary":"已提交；全部终态后检查","continuation":{"when":"all_generations_terminal","instruction":"检查结果；失败不自动生成"}}`)
	if run.Status != "completed" {
		t.Fatalf("source status=%s", run.Status)
	}
	s, db = reopenGenerationContinuationService(t, s, db)
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchArmed {
		t.Fatalf("armed restart: %+v", batch)
	}
	if n, err := s.DispatchAgentMessages(20); err != nil || n != 0 {
		t.Fatalf("pending task started continuation: %d %v", n, err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "expected restart failure"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePendingCloudAgentGenerations(); err != nil {
		t.Fatal(err)
	}
	s, db = reopenGenerationContinuationService(t, s, db)
	batch, err = s.repo.AgentGenerationBatch("user", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.AgentGenerationBatchReady || batch.TerminalTasks != 1 {
		t.Fatalf("ready restart: %+v", batch)
	}
	var outcomes map[string]json.RawMessage
	if err := json.Unmarshal([]byte(batch.ResultsJSON), &outcomes); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		var outcome any
		if err := json.Unmarshal(outcomes[taskID], &outcome); err != nil {
			t.Fatal(err)
		}
		if _, changed, err := s.repo.SettleAgentGenerationBatchTask("user", batch.ID, taskID, outcome); err != nil || changed {
			t.Fatalf("duplicate terminal callback changed batch: %v %v", changed, err)
		}
	}
	if n, err := s.QueueReadyGenerationContinuations(20); err != nil || n != 1 {
		t.Fatalf("queue after restart: %d %v", n, err)
	}
	s, db = reopenGenerationContinuationService(t, s, db)
	if n, err := s.QueueReadyGenerationContinuations(20); err != nil || n != 0 {
		t.Fatalf("queued restart reinserted message: %d %v", n, err)
	}
	if n, err := s.DispatchAgentMessages(20); err != nil || n != 1 {
		t.Fatalf("queued restart dispatch: %d %v", n, err)
	}
	before, err := s.repo.CreditAccount("user")
	if err != nil {
		t.Fatal(err)
	}
	s, db = reopenGenerationContinuationService(t, s, db)
	for range 3 {
		if _, err := s.DispatchAgentMessages(20); err != nil {
			t.Fatal(err)
		}
	}
	after, err := s.repo.CreditAccount("user")
	if err != nil {
		t.Fatal(err)
	}
	if before.ReservedMicrocredits != after.ReservedMicrocredits || before.AvailableMicrocredits != after.AvailableMicrocredits {
		t.Fatalf("restart replay billed again: %+v %+v", before, after)
	}
	var messages, runs, tasks int64
	if err := db.Model(&model.AgentQueuedMessage{}).Where("kind = ?", model.AgentQueuedMessageGenerationContinuation).Count(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CloudAgentExecution{}).Count(&runs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	if messages != 1 || runs != 2 || tasks != 1 {
		t.Fatalf("restarted workflow duplicated: messages=%d runs=%d media=%d", messages, runs, tasks)
	}
}
