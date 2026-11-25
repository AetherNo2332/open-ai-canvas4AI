package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestCloudAgentGenerationBatchRegistrationFollowsSubmittedTasks(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 3, 36
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	second := args
	second.NodeID, second.Title = "video-shot-2", "镜头2视频"
	callOne, callTwo := agentMediaCall(args), agentMediaCall(second)
	callTwo.ID = "media-call-2"
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{callOne})
	if err := agentPiExecuteCallForTest(t, s, run.ID, callOne.ID); err != nil {
		t.Fatal(err)
	}
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	second.SnapshotHash = cloudAgentCanvasHash(doc)
	callTwo = agentMediaCall(second)
	callTwo.ID = "media-call-2"
	run, _ = agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{callTwo})
	if err := agentPiExecuteCallForTest(t, s, run.ID, callTwo.ID); err != nil {
		t.Fatal(err)
	}
	_, state := agentInterjectionState(t, s, run.ID)
	if len(state.PendingGenerations) != 2 {
		t.Logf("second receipt: %s", cloudAgentToolMessageForTest(t, state, callTwo.ID))
		t.Fatalf("pending generations = %d callIndex=%d calls=%d status=%+v", len(state.PendingGenerations), state.CallIndex, len(state.Calls), state.PendingGenerations)
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.ExpectedTasks != 2 || batch.TerminalTasks != 0 || batch.Status != model.AgentGenerationBatchCollecting {
		t.Fatalf("batch did not follow actual submissions: %+v", batch)
	}
	if state.PendingGenerations[0].BatchID != batch.ID || state.PendingGenerations[1].BatchID != batch.ID {
		t.Fatalf("pending items lost their batch identity: %+v", state.PendingGenerations)
	}
	receipt := cloudAgentToolMessageForTest(t, state, callOne.ID)
	if !strings.Contains(receipt, batch.ID) || !strings.Contains(receipt, `"batchTaskCount":1`) {
		t.Fatalf("submission receipt must expose the batch identity and dynamic count: %s", receipt)
	}
}

func TestCloudAgentGenerationBatchRejectedSubmissionRegistersNothing(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 1, 12
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{agentMediaCall(args)})
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < policy.Task.ActiveTaskLimit; index++ {
		if err := db.Create(&model.Task{ID: "batch-full-" + string(rune('a'+index)), UserID: "user", Type: "canvas_image", Operation: "text_to_image", Status: model.TaskStatusRunning}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID); err == nil {
		t.Fatal("a rejected submission registered a generation batch")
	}
}

func TestCloudAgentGenerationBatchSettlementPreservesEveryOutcome(t *testing.T) {
	s, db, args := agentMediaFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 3, 36
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	second := args
	second.NodeID, second.Title = "video-shot-2", "镜头2视频"
	callOne, callTwo := agentMediaCall(args), agentMediaCall(second)
	callTwo.ID = "media-call-2"
	run, _ := agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{callOne})
	if err := agentPiExecuteCallForTest(t, s, run.ID, callOne.ID); err != nil {
		t.Fatal(err)
	}
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	second.SnapshotHash = cloudAgentCanvasHash(doc)
	callTwo = agentMediaCall(second)
	callTwo.ID = "media-call-2"
	run, _ = agentStagePiOutputForTest(t, s, db, root.ID, "", []cloudAgentCall{callTwo})
	if err := agentPiExecuteCallForTest(t, s, run.ID, callTwo.ID); err != nil {
		t.Fatal(err)
	}
	_, state := agentInterjectionState(t, s, run.ID)
	first, secondTask := state.PendingGenerations[0].TaskID, state.PendingGenerations[1].TaskID
	if err := db.Model(&model.Task{}).Where("id = ?", first).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "上游拒绝"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", secondTask).Updates(map[string]any{"status": model.TaskStatusCancelled}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePendingCloudAgentGenerations(); err != nil {
		t.Fatal(err)
	}
	batch, err := s.repo.AgentGenerationBatchBySourceRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.TerminalTasks != 2 || batch.ExpectedTasks != 2 {
		t.Fatalf("terminal outcomes were not persisted: %+v", batch)
	}
	results := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(batch.ResultsJSON), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("aggregate results = %s", batch.ResultsJSON)
	}
	for _, taskID := range []string{first, secondTask} {
		var outcome map[string]any
		if err := json.Unmarshal(results[taskID], &outcome); err != nil {
			t.Fatal(err)
		}
		if outcome["taskId"] != taskID || outcome["summary"] == "" {
			t.Fatalf("task %s outcome lost: %+v", taskID, outcome)
		}
	}
}
