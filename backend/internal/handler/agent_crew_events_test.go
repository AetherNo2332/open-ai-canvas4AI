package handler

import (
	"infinite-canvas/backend/internal/model"
	"strings"
	"testing"
)

func TestCrewSSECursorSnapshotAndOwnership(t *testing.T) {
	call, svc, db := workspaceHTTPFixture(t)
	workspace, err := svc.GetAgentWorkspace("workspace-user", "own")
	if err != nil {
		t.Fatal(err)
	}
	run := model.AgentCrewRun{ID: "crew-stream", UserID: "workspace-user", WorkspaceID: workspace.WorkspaceID, CanvasID: "own", Status: model.CrewRunCompleted, BudgetJSON: `{"maxConcurrentMembers":2}`, LatestSequence: 2}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{"crew_run_created", "crew_run_completed"} {
		if err := db.Create(&model.AgentCrewEvent{CrewRunID: run.ID, Sequence: int64(i + 1), Type: kind, PayloadJSON: `{"status":"completed"}`}).Error; err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/agent/crew-runs/crew-stream/events"
	if w := call("GET", path+"?after=1", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), "id: 2\n") || strings.Contains(w.Body.String(), "id: 1\n") || !strings.Contains(w.Body.String(), "event: crew_snapshot\n") {
		t.Fatalf("SSE: %d %s", w.Code, w.Body)
	}
	if w := call("GET", path+"?after=2", "", true); w.Code != 200 || strings.Contains(w.Body.String(), "id:") {
		t.Fatal("snapshot advanced cursor")
	}
	if w := call("GET", path+"?after=-1", "", true); w.Code != 400 {
		t.Fatalf("cursor: %d", w.Code)
	}
	if w := call("GET", path, "", false); w.Code != 401 {
		t.Fatalf("auth: %d", w.Code)
	}
	if err := db.Model(&run).Update("user_id", "other").Error; err != nil {
		t.Fatal(err)
	}
	if w := call("GET", path, "", true); w.Code != 404 {
		t.Fatalf("ownership: %d %s", w.Code, w.Body)
	}
}
