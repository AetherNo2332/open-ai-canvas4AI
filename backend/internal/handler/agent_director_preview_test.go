package handler

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

// Wait/resume and resource ownership are covered in
// internal/app/cloud_agent_director_tool_test.go; this checks the HTTP contract.
func TestAgentDirectorPreviewHTTPEnvelope(t *testing.T) {
	call, _, db := workspaceHTTPFixture(t)
	for _, row := range []any{
		&model.CloudAgentExecution{ID: "run-own", UserID: "workspace-user", ConversationID: "run-own", CanvasID: "own", Status: "running", Revision: 1, StateJSON: `{"request":{},"calls":[],"callIndex":0,"taskIds":[],"decisions":{}}`},
		&model.CloudAgentExecution{ID: "run-foreign", UserID: "other", ConversationID: "run-foreign", CanvasID: "foreign", Status: "running", Revision: 1},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/agent/runs/run-own/director-preview"
	if w := call("POST", path, `{"requestId":"r","videoResourceId":"v"}`, false); w.Code != 401 {
		t.Fatalf("authentication: %d %s", w.Code, w.Body)
	}
	if w := call("POST", path, `{"requestId":"r","videoResourceId":"v","extra":1}`, true); w.Code != 400 {
		t.Fatalf("unknown fields must be rejected: %d %s", w.Code, w.Body)
	}
	if w := call("POST", path, `{"requestId":"r","videoResourceId":"v","error":"x"}`, true); w.Code != 400 {
		t.Fatalf("result and error together must be rejected: %d %s", w.Code, w.Body)
	}
	for _, target := range []string{path, "/api/agent/runs/run-foreign/director-preview", "/api/agent/runs/missing/director-preview"} {
		w := call("POST", target, `{"requestId":"unknown","videoResourceId":"v"}`, true)
		var failure struct {
			Code   int    `json:"code"`
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &failure)
		if w.Code != 404 || failure.Code != 404 || failure.Reason != "director_preview_not_found" {
			t.Fatalf("%s: unknown request or foreign run must be 404 director_preview_not_found: %d %s", target, w.Code, w.Body)
		}
	}
}
