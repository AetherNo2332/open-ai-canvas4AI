package app

import (
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestCloudAgentModelFailureRedactsProviderDetails(t *testing.T) {
	for _, test := range []struct{ raw, reason string }{
		{"connection reset by peer", "model_connection_reset"},
		{"context deadline exceeded", "model_request_timeout"},
		{"connection refused", "model_connection_refused"},
		{"unknown provider error", "model_task_failed"},
	} {
		text, reason := cloudAgentModelFailure(&model.Task{ID: "task-id", Error: `Post "https://private.example?key=secret": ` + test.raw})
		if reason != test.reason || !strings.Contains(text, "task-id") || strings.Contains(text, "private") || strings.Contains(text, "secret") {
			t.Fatalf("unsafe or incorrect error: %q, %q", text, reason)
		}
	}
}

func TestCloudAgentFailedModelEmitsSafeReason(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	_, stepState := agentStartPiModelStep(t, s, run.ID)
	modelTaskID := stepState.ActiveTaskID
	if modelTaskID == "" || modelTaskID == run.ID {
		t.Fatalf("Pi model task was not admitted independently of the holding reservation: %q", modelTaskID)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", modelTaskID).Updates(map[string]any{"status": model.TaskStatusFailed, "error": "private-url connection reset by peer"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := advancePiAgentForTest(t, s, run.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(stored)
	if err != nil {
		t.Fatal(err)
	}
	event := state.Events[len(state.Events)-1]
	if stored.Status != "failed" || event.Type != "run_failed" || event.Payload["reason"] != "model_connection_reset" || event.Payload["taskId"] != modelTaskID || strings.Contains(event.Payload["text"].(string), "private-url") {
		t.Fatalf("incorrect model failure event: %+v", event)
	}
}
