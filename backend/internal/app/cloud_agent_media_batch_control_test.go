package app

import (
	"context"
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentMediaBatchCanaryRetryKeepsRemainingItemsPausedAcrossRestart(t *testing.T) {
	s, db := agentMediaBatchFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks = 6
	req.Budget.MaxVideoSeconds = 60
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	args := agentMediaBatchArgs("v1", "v2")
	args["canary"], args["maxRetriesPerItem"] = true, 1
	raw, _ := json.Marshal(args)
	call := piAgentTestCall("canary-batch", "generate_media_batch", string(raw))
	run, state := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{call})
	if receipt, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, call.ID); err != nil || !receipt.Pending {
		t.Fatalf("batch approval receipt = %+v, %v", receipt, err)
	}
	_, waiting := agentMediaBatchState(t, s, run.ID)
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, call.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.stepCloudAgentMediaBatch(context.Background(), "user", run.ID); err != nil {
		t.Fatal(err)
	}
	_, submitted := agentMediaBatchState(t, s, run.ID)
	firstTaskID := submitted.MediaBatch.Items[0].taskID()
	if firstTaskID == "" || submitted.MediaBatch.Items[1].taskID() != "" {
		t.Fatalf("canary submitted unexpected items: %+v", submitted.MediaBatch.Items)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", firstTaskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游超时"}).Error; err != nil {
		t.Fatal(err)
	}
	finishAgentMediaBatchPhase(t, s, run.ID)
	if err := agentPiExecuteCallForTest(t, s, run.ID, call.ID); err != nil {
		t.Fatal(err)
	}
	retry := piAgentTestCall("retry-canary", "media_batch_control", `{"action":"retry","items":[{"index":0,"prompt":"英雄抬头看向雨夜"}]}`)
	run, state = agentStagePiOutputForTest(t, s, db, run.ID, "", []cloudAgentCall{retry})
	if _, err := s.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, retry.ID); err != nil {
		t.Fatal(err)
	}

	// Reconstruct the host using only persisted state, as after a backend restart.
	restarted := New(s.repo, s.dataDir)
	restarted.mediaBatchClosed = true
	if receipt, err := restarted.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, retry.ID); err != nil || !receipt.Pending {
		t.Fatalf("recovered retry receipt = %+v, %v", receipt, err)
	}
	for step := 0; step < 2; step++ {
		if _, err := restarted.stepCloudAgentMediaBatch(context.Background(), "user", run.ID); err != nil {
			t.Fatal(err)
		}
	}
	_, retried := agentMediaBatchState(t, restarted, run.ID)
	if retried.MediaBatch.Phase != cloudAgentMediaBatchPhaseCanary || retried.MediaBatch.Items[1].Status != cloudAgentMediaBatchItemPending {
		t.Fatalf("retry released unapproved canary remainder: phase=%s items=%+v", retried.MediaBatch.Phase, retried.MediaBatch.Items)
	}
	if len(retried.MediaBatch.Items[0].TaskIDs) != 2 || retried.MediaBatch.Items[0].taskID() == firstTaskID {
		t.Fatalf("retry did not retain distinct task history: %+v", retried.MediaBatch.Items[0])
	}
	if err := db.Model(&model.Task{}).Where("id = ?", retried.MediaBatch.Items[0].taskID()).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游超时"}).Error; err != nil {
		t.Fatal(err)
	}
	finishAgentMediaBatchPhase(t, restarted, run.ID)
	if err := agentPiExecuteCallForTest(t, restarted, run.ID, retry.ID); err != nil {
		t.Fatal(err)
	}
	resume := piAgentTestCall("continue-batch", "media_batch_control", `{"action":"continue"}`)
	run, state = agentStagePiOutputForTest(t, restarted, db, run.ID, "", []cloudAgentCall{resume})
	if _, err := restarted.PiToolAdvance("user", run.ID, run.LeaseOwner, state.PiToolBatchTaskID, resume.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.stepCloudAgentMediaBatch(context.Background(), "user", run.ID); err != nil {
		t.Fatal(err)
	}
	_, continued := agentMediaBatchState(t, restarted, run.ID)
	if continued.MediaBatch.Items[1].Status != cloudAgentMediaBatchItemSubmitted {
		t.Fatalf("explicit continue did not release remainder: %+v", continued.MediaBatch.Items[1])
	}
}

func finishAgentMediaBatchPhase(t *testing.T, s *Service, runID string) {
	t.Helper()
	for step := 0; step < 5; step++ {
		done, err := s.stepCloudAgentMediaBatch(context.Background(), "user", runID)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			return
		}
	}
	t.Fatal("batch phase did not settle")
}
