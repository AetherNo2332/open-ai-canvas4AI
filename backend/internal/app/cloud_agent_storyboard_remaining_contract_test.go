package app

import (
	"encoding/json"
	"testing"
)

func TestStoryboardRemainingSchemaContracts(t *testing.T) {
	cases := []struct {
		name  string
		args  map[string]any
		valid bool
	}{
		{"add assets", map[string]any{"action": "add_assets", "rowId": "r", "assets": []any{map[string]any{"ref": "hero", "role": "character"}}}, true},
		{"empty delta", map[string]any{"action": "add_assets", "rowId": "r", "assets": []any{}}, false},
		{"upsert", map[string]any{"action": "upsert_assets", "rowIds": []string{"r"}, "assets": []any{map[string]any{"ref": "hero", "role": "character"}}}, true},
		{"empty shared delta", map[string]any{"action": "remove_references", "references": []string{}}, false},
		{"shared delta", map[string]any{"action": "add_references", "references": []string{"hero"}}, true},
		{"stable operation", map[string]any{"action": "append_rows", "operationId": "task/chunk-1", "rows": []any{map[string]any{"durationSeconds": 5.0, "plotDescription": "new"}}}, true},
		{"scoped scalar", map[string]any{"action": "update", "rowId": "r", "storyboardRevision": "revision", "patch": map[string]any{"dialogue": "new"}}, true},
		{"scoped asset patch", map[string]any{"action": "update", "rowId": "r", "storyboardRevision": "revision", "patch": map[string]any{"assets": []any{}}}, false},
		{"scoped batch assets", map[string]any{"action": "batch_update", "storyboardRevision": "revision", "updates": []any{map[string]any{"rowId": "r", "patch": map[string]any{"assets": []any{}}}}}, false},
		{"scoped append", map[string]any{"action": "append_rows", "storyboardRevision": "revision", "rows": []any{map[string]any{"durationSeconds": 5.0, "plotDescription": "new"}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.args["nodeId"], tc.args["snapshotHash"] = "board", "hash"
			raw, _ := json.Marshal(tc.args)
			err := validateCloudAgentToolArguments(workflowTool(t, "canvas_edit_storyboard"), string(raw))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestStoryboardEmptyOperationQueryRejected(t *testing.T) {
	s, canvas := storyboardFieldReadFixture(t, []map[string]any{{"id": "r", "durationSeconds": 5.0, "plotDescription": "row"}})
	if _, err := cloudAgentReadStoryboard(s.repo, "user", canvas.ID, cloudAgentStoryboardCall(t, "canvas_read_storyboard", "empty-ops", map[string]any{"nodeId": "board", "operationIds": []string{}})); err == nil {
		t.Fatal("explicit empty query silently accepted")
	}
}

func TestStoryboardOperationQueryValidationBeforeRepository(t *testing.T) {
	for name, ids := range map[string][]string{"short": {"short"}, "duplicate": {"task/chunk-1", "task/chunk-1"}, "unicode-short": {"批次一"}} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("invalid query touched repository: %v", recovered)
				}
			}()
			_, err := cloudAgentReadStoryboard(nil, "user", "canvas", cloudAgentStoryboardCall(t, "canvas_read_storyboard", "invalid-operations", map[string]any{"nodeId": "board", "operationIds": ids}))
			if err == nil {
				t.Fatal("invalid operation query accepted")
			}
		})
	}
}

func TestStoryboardScopedConflictFeedbackRefreshesBothVersions(t *testing.T) {
	req := tierAutoRequest()
	state := cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
	call := cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "conflict", map[string]any{"snapshotHash": "old", "storyboardRevision": "old-revision", "operationId": "fill/chunk-1", "nodeId": "board", "action": "update", "rowId": "row", "patch": map[string]any{"dialogue": "keep"}})
	detail := map[string]any{}
	cloudAgentSnapshotFeedback(&state, call, detail)
	next := detail["nextRead"].(map[string]any)
	patch := detail["retryArgumentsPatch"].(map[string]any)
	if next["tool"] != "canvas_read_storyboard" || next["arguments"].(map[string]any)["nodeId"] != "board" || patch["storyboardRevision"] != "<canvas_read_storyboard.storyboardRevision>" {
		t.Fatalf("scoped conflict kept stale revision: %v", detail)
	}
	example := detail["exampleArguments"].(map[string]any)
	if example["operationId"] != "fill/chunk-1" || example["storyboardRevision"] != patch["storyboardRevision"] || example["patch"].(map[string]any)["dialogue"] != "keep" {
		t.Fatal("repair changed batch identity or intended content")
	}
}
