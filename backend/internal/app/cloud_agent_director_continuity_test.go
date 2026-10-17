package app

import "testing"

// status.ok=false is data carried inside the continuity result; the Go call
// still returns a nil error. The workflow must therefore key the "checked"
// phase off the result, not off the absence of an error, or a scene with
// unresolved cut-continuity issues would wrongly unblock preview/handoff.
func TestCloudAgentImageDirectorContinuityFailureDoesNotAdvanceToChecked(t *testing.T) {
	state := &cloudAgentRuntime{Request: CloudAgentRequest{Workflow: "image_to_director"}}
	state.DirectorWorkflowPhase = "designed"
	failed := map[string]any{
		"sceneId": "scene-1",
		"shotId":  "shot-1",
		"status":  map[string]any{"ok": false, "issues": []any{"镜头缺少有效摄影机"}},
	}
	result := cloudAgentDirectorReceipt("run-1", "check-call", failed)
	cloudAgentAdvanceDirectorWorkflow(state, "director_check_continuity", "check-call", result, nil)
	if state.DirectorWorkflowPhase != "designed" {
		t.Fatalf("failed continuity check advanced phase to %q; want designed", state.DirectorWorkflowPhase)
	}
	preview := cloudAgentCall{}
	preview.Function.Name = "director_preview_shot"
	if err := cloudAgentDirectorWorkflowError(state, preview); err == nil {
		t.Fatal("preview must remain blocked after a failed continuity check")
	}
}

// A clean per-shot continuity check is the only thing that may advance the
// image-to-director workflow to "checked" and unblock preview/handoff.
func TestCloudAgentImageDirectorContinuityPassAdvancesToChecked(t *testing.T) {
	state := &cloudAgentRuntime{Request: CloudAgentRequest{Workflow: "image_to_director"}}
	state.DirectorWorkflowPhase = "designed"
	passed := map[string]any{
		"sceneId": "scene-1",
		"shotId":  "shot-1",
		"status":  map[string]any{"ok": true, "issues": []any{}},
	}
	result := cloudAgentDirectorReceipt("run-1", "check-call", passed)
	cloudAgentAdvanceDirectorWorkflow(state, "director_check_continuity", "check-call", result, nil)
	if state.DirectorWorkflowPhase != "checked" {
		t.Fatalf("clean continuity check did not advance phase to checked; got %q", state.DirectorWorkflowPhase)
	}
	preview := cloudAgentCall{}
	preview.Function.Name = "director_preview_shot"
	if err := cloudAgentDirectorWorkflowError(state, preview); err != nil {
		t.Fatalf("preview must be allowed after a clean continuity check: %v", err)
	}
}
