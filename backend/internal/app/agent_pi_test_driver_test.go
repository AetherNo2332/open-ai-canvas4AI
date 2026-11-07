package app

import (
	"encoding/json"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

// driveTestPiRuns is the deterministic in-process equivalent of the Node Pi
// worker used by queue-based app tests. It still calls the production Pi
// admission, checkpoint, tool receipt and completion APIs; only the network
// worker loop is collapsed so legacy Go tests can exercise the migrated path.
func driveTestPiRuns(t *testing.T, svc *Service, text *structuredFixture, userScopes ...string) {
	t.Helper()
	for pass := 0; pass < 32; pass++ {
		progressed := false
		for _, userID := range userScopes {
			worker := "test-pi-worker-" + userID
			snapshot, err := svc.ClaimPiAgentForWorker(worker, userID)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot == nil {
				continue
			}
			progressed = true
			driveTestPiRun(t, svc, text, userID, worker, snapshot)
		}
		if !progressed {
			return
		}
	}
}

func driveTestPiRun(t *testing.T, svc *Service, text *structuredFixture, userID, worker string, snapshot *PiAgentSnapshot) {
	t.Helper()
	for stepCount := 0; stepCount < 16; stepCount++ {
		if cloudAgentRunTerminal(snapshot.Status) {
			return
		}
		owner := worker + "@" + itoa64(snapshot.PiSessionLeaseEpoch)
		taskID := snapshot.ActiveTask
		if taskID == "" {
			step, err := svc.PiModelStep(userID, snapshot.RunID, owner, PiModelStepRequest{Canonical: snapshot.Canonical})
			if err != nil {
				t.Fatal(err)
			}
			taskID = step.TaskID
			if taskID == "" {
				return
			}
		}
		drainTestPiTasks(t, svc)
		view, err := svc.PiModelStepView(userID, snapshot.RunID, owner, taskID)
		if err != nil {
			t.Fatal(err)
		}
		if view.Status != string(model.TaskStatusSucceeded) {
			if view.Status == string(model.TaskStatusFailed) {
				_, _ = svc.PiFailModelStepResult(userID, snapshot.RunID, owner, taskID)
			}
			return
		}
		output := struct {
			Text       string           `json:"text"`
			ToolCalls  []cloudAgentCall `json:"toolCalls"`
			Legacy     []cloudAgentCall `json:"tool_calls"`
			StopReason string           `json:"stopReason"`
		}{}
		if err := json.Unmarshal(view.Result, &output); err != nil {
			t.Fatal(err)
		}
		calls := output.ToolCalls
		if len(calls) == 0 {
			calls = output.Legacy
		}
		latest, err := svc.PiAgentSnapshot(userID, snapshot.RunID, owner)
		if err != nil {
			t.Fatal(err)
		}
		message := map[string]any{"role": "assistant", "content": output.Text, "stopReason": output.StopReason}
		if message["stopReason"] == "" {
			message["stopReason"] = "stop"
		}
		raw, _ := json.Marshal(message)
		if err := svc.PiCheckpointMessage(userID, snapshot.RunID, owner, PiMessageCheckpoint{
			Sequence: len(latest.PiMessages) + 1, TaskID: taskID, Message: raw,
		}); err != nil {
			t.Fatal(err)
		}
		if len(calls) == 0 {
			if _, err := svc.PiNoToolTurn(userID, snapshot.RunID, owner, taskID); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := svc.PiToolBatch(userID, snapshot.RunID, owner, PiToolBatchRequest{TaskID: taskID, Calls: calls}); err != nil {
			t.Fatal(err)
		}
		for _, call := range calls {
			receipt, err := svc.PiToolAdvance(userID, snapshot.RunID, owner, taskID, call.ID)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Pending {
				return
			}
		}
		snapshot, err = svc.PiAgentSnapshot(userID, snapshot.RunID, owner)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func drainTestPiTasks(t *testing.T, svc *Service) {
	t.Helper()
	for range 16 {
		task, err := svc.repo.ClaimNextTask("test-pi-model", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if task == nil {
			return
		}
		if err := svc.taskWorker().processClaimedTask(task, nil); err != nil {
			t.Fatal(err)
		}
	}
}
