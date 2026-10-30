package handler

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

// Undo logic (three-way restore, conflicts, revision) is covered in
// internal/app/cloud_agent_undo_test.go; this checks the HTTP contract.
func TestAgentRunUndoHTTPEnvelope(t *testing.T) {
	call, _, db := workspaceHTTPFixture(t)
	for _, row := range []any{
		&model.CloudAgentExecution{ID: "run-live", UserID: "workspace-user", ConversationID: "run-live", CanvasID: "own", Status: "running", Revision: 1},
		&model.CloudAgentExecution{ID: "run-foreign", UserID: "other", ConversationID: "run-foreign", CanvasID: "foreign", Status: "completed", Revision: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if w := call("POST", "/api/agent/runs/run-live/undo", `{}`, false); w.Code != 401 {
		t.Fatalf("authentication: %d %s", w.Code, w.Body)
	}
	if w := call("POST", "/api/agent/runs/run-live/undo", `{"expectedSnapshotHash":"x"}`, true); w.Code != 400 {
		t.Fatalf("unknown fields must be rejected: %d %s", w.Code, w.Body)
	}
	if w := call("POST", "/api/agent/runs/run-foreign/undo", ``, true); w.Code != 404 {
		t.Fatalf("foreign run must be not found: %d %s", w.Code, w.Body)
	}
	w := call("POST", "/api/agent/runs/run-live/undo", ``, true)
	var failure struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &failure)
	if w.Code != 409 || failure.Code != 409 {
		t.Fatalf("running run: HTTP status and business code must both be 409: %d %s", w.Code, w.Body)
	}
}
