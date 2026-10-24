package app

import (
	"encoding/json"
	"testing"
)

func TestPiSnapshotRetainsBatchForMissingWorkerCheckpoint(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "completed"}[completed], func(t *testing.T) {
			s, db, run := piAgentTestLeasedFixture(t)
			_, state := reloadPiRun(t, s, run.ID)
			call := piAgentTestCall("historical-call", "previs_preview", `{"sceneId":"scene"}`)
			state.Calls = []cloudAgentCall{call}
			state.CallIndex = 0
			if completed {
				state.CallIndex = 1
			}
			state.CallAdmissions = []cloudAgentCallAdmission{{CallID: call.ID, Allowed: true}}
			state.PiToolBatchTaskID, state.LastStepTaskID = "original-task", "original-task"
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(run).Update("state_json", string(raw)).Error; err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.PiAgentSnapshot("user", run.ID, run.LeaseOwner)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.PendingToolBatch == nil || snapshot.PendingToolBatch.TaskID != "original-task" || len(snapshot.PendingToolBatch.Calls) != 1 || snapshot.PendingToolBatch.Calls[0].ID != call.ID {
				t.Fatalf("lost recovery identity: %#v", snapshot.PendingToolBatch)
			}
			for _, tool := range snapshot.Tools {
				if tool.Name == "previs_preview" && tool.Allowed {
					t.Fatal("retired tool became callable")
				}
			}
		})
	}
}

func TestPiRetiredToolLeavesLoadoutOnlyAfterBatchSettles(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	declareTestChannelWindow(t, db, 64000, 8192)
	_, state := reloadPiRun(t, s, run.ID)
	state.Canonical.Tools = append(state.Canonical.Tools, map[string]any{"type": "function", "function": map[string]any{"name": "previs_preview", "parameters": map[string]any{"type": "object"}}})
	state.AdvertisedToolNames = cloudAgentToolNames(state.Canonical.Tools)
	call := piAgentTestCall("old-render", "previs_preview", `{"sceneId":"scene"}`)
	state.Calls = []cloudAgentCall{call}
	state.PiToolBatchTaskID, state.LastStepTaskID = "original-task", "original-task"
	state.CallAdmissions = []cloudAgentCallAdmission{{CallID: call.ID, Allowed: true}}
	request := state.Canonical
	request.Tools = nil
	for _, tool := range state.Canonical.Tools {
		if stringField(tool["function"].(map[string]any), "name") != "previs_preview" {
			request.Tools = append(request.Tools, tool)
		}
	}
	for _, settled := range []bool{false, true} {
		if settled {
			state.CallIndex = 1
		}
		raw, err := json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(run).Update("state_json", string(raw)).Error; err != nil {
			t.Fatal(err)
		}
		_, err = s.PiModelPreflight("user", run.ID, run.LeaseOwner, PiModelStepRequest{Canonical: request})
		if settled && err != nil {
			t.Fatalf("settled retired loadout blocked: %v", err)
		}
		if !settled && err == nil {
			t.Fatal("pending batch bypassed")
		}
	}
}
