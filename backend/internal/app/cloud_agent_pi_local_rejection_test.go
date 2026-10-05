package app

import (
	"encoding/json"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func TestPiCheckpointSettlesLocallyRejectedToolArguments(t *testing.T) {
	for _, tc := range []struct {
		name, args, resultID, resultName string
		wantIndex                        int
	}{
		{"invalid options", `{"question":"选择方向","options":"[]"}`, "call-a", "ask_user", 1},
		{"valid call cannot be skipped", `{"question":"选择方向","options":[{"label":"A"},{"label":"B"}]}`, "call-a", "ask_user", 0},
		{"wrong call cannot be skipped", `{"question":"选择方向","options":"[]"}`, "other-call", "ask_user", 0},
		{"wrong tool cannot be skipped", `{"question":"选择方向","options":"[]"}`, "call-a", "canvas_get_state", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			call := piAgentTestCall("call-a", "ask_user", tc.args)
			output, _ := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{call}})
			const taskID = "pi-local-invalid-args"
			if err := db.Create(&model.Task{ID: taskID, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(output)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				state, err := cloudAgentDecode(current)
				if err != nil {
					return err
				}
				state.LastStepTaskID = taskID
				return cloudAgentSave(current, &state)
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: taskID, Calls: []cloudAgentCall{call}}); err != nil {
				t.Fatal(err)
			}
			message, _ := json.Marshal(map[string]any{"role": "toolResult", "toolCallId": tc.resultID, "toolName": tc.resultName, "isError": true, "content": []map[string]any{{"type": "text", "text": "Pi argument validation rejected options"}}})
			checkpoint := PiMessageCheckpoint{Sequence: 1, Message: message}
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint); err != nil {
				t.Fatal(err)
			}
			before, state := reloadPiRun(t, s, run.ID)
			if state.CallIndex != tc.wantIndex {
				t.Fatalf("locally rejected call remained pending: got %d want %d", state.CallIndex, tc.wantIndex)
			}
			if tc.wantIndex == 1 {
				receipt := piToolReceipt(state, taskID, "call-a")
				if receipt == nil || !receipt.IsError {
					t.Fatalf("missing durable error receipt: %+v", receipt)
				}
			}
			if err := s.PiCheckpointMessage("user", run.ID, run.LeaseOwner, checkpoint); err != nil {
				t.Fatal(err)
			}
			after, replay := reloadPiRun(t, s, run.ID)
			if replay.CallIndex != state.CallIndex || len(replay.Events) != len(state.Events) || len(replay.Canonical.Messages) != len(state.Canonical.Messages) {
				t.Fatal("checkpoint replay settled a tool twice")
			}
			if len(before.Transcript) != len(after.Transcript) {
				t.Fatal("checkpoint replay duplicated the message")
			}
			if tc.wantIndex == 1 {
				corrected := piAgentTestCall("call-b", "ask_user", `{"question":"选择方向","options":[{"label":"A"},{"label":"B"}]}`)
				output, _ := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{corrected}})
				const nextTask = "pi-corrected-args"
				if err := db.Create(&model.Task{ID: nextTask, UserID: "user", ProjectID: run.CanvasID, Type: "canvas_text", Status: model.TaskStatusSucceeded, ResultJSON: string(output)}).Error; err != nil {
					t.Fatal(err)
				}
				if err := s.repo.MutateCloudAgent("user", run.ID, after.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
					state, err := cloudAgentDecode(current)
					if err != nil {
						return err
					}
					state.LastStepTaskID = nextTask
					return cloudAgentSave(current, &state)
				}); err != nil {
					t.Fatal(err)
				}
				if err := s.PiToolBatch("user", run.ID, run.LeaseOwner, PiToolBatchRequest{TaskID: nextTask, Calls: []cloudAgentCall{corrected}}); err != nil {
					t.Fatalf("corrected batch rejected: %v", err)
				}
			}
		})
	}
}
