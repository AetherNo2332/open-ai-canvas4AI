package app

import (
	"encoding/json"
	"testing"
)

func TestCloudAgentDirectorToolsAreAdvertisedWithControlledContracts(t *testing.T) {
	req := CloudAgentRequest{ContextScope: []string{"canvas"}, PermissionMode: "edit", VisionEnabled: true}
	tools := compileCloudAgentTools(req)
	seen := map[string]bool{}
	for _, tool := range tools {
		fn, _ := tool["function"].(map[string]any)
		seen[stringField(fn, "name")] = true
	}
	for _, name := range []string{"director_block_scene", "director_design_shot", "director_draw_action", "director_check_continuity", "director_preview_shot", "director_handoff_shot"} {
		if !seen[name] {
			t.Fatalf("missing director tool %s", name)
		}
	}
	if !cloudAgentWrite("director_block_scene") || !cloudAgentWrite("director_design_shot") || !cloudAgentWrite("director_draw_action") || !cloudAgentWrite("director_preview_shot") {
		t.Fatal("director write tools must use the approval path")
	}
}

func TestCloudAgentDirectorDesignShotCompilesToPrevisPatch(t *testing.T) {
	call := cloudAgentCall{ID: "call-1"}
	call.Function.Name = "director_design_shot"
	call.Function.Arguments = `{"snapshotHash":"scene-hash","sceneId":"scene-1","shotId":"shot-1","shotSize":"close_up","cameraMove":"push_in","prompt":"慢慢推进"}`
	normalized, err := cloudAgentDirectorUnderlyingCall(call)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Function.Name != "previs_apply_patch" {
		t.Fatalf("normalized tool = %s", normalized.Function.Name)
	}
	var args struct {
		Operations []map[string]any `json:"operations"`
	}
	if err := json.Unmarshal([]byte(normalized.Function.Arguments), &args); err != nil || len(args.Operations) != 1 || args.Operations[0]["type"] != "shot_update" {
		t.Fatalf("unexpected normalized args: %s", normalized.Function.Arguments)
	}
}

func TestCloudAgentDirectorReceiptCarriesRunCorrelationAndSceneRevision(t *testing.T) {
	result := cloudAgentDirectorReceipt("run-1", "call-1", map[string]any{"snapshotHash": "scene-hash", "committed": true})
	fields, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("receipt type = %T", result)
	}
	if fields["requestId"] != "run-1" || fields["callId"] != "call-1" || fields["revision"] != "scene-hash" {
		t.Fatalf("unexpected director receipt: %#v", fields)
	}
}

func TestCloudAgentImageDirectorWorkflowRequiresInspectionBeforeWrites(t *testing.T) {
	state := &cloudAgentRuntime{Request: CloudAgentRequest{Workflow: "image_to_director"}}
	call := cloudAgentCall{}
	call.Function.Name = "director_block_scene"
	if err := cloudAgentDirectorWorkflowError(state, call); err == nil {
		t.Fatal("director_block_scene must require a successful image inspection")
	}
	state.DirectorWorkflowPhase = "inspected"
	if err := cloudAgentDirectorWorkflowError(state, call); err != nil {
		t.Fatalf("inspected phase rejected block_scene: %v", err)
	}
	cloudAgentAdvanceDirectorWorkflow(state, "director_block_scene", "block-call", nil, nil)
	call.Function.Name = "director_design_shot"
	if err := cloudAgentDirectorWorkflowError(state, call); err != nil {
		t.Fatalf("blocked phase rejected design_shot: %v", err)
	}
}

func TestCloudAgentImageDirectorRollbackOnlyAppliesBeforeContinuityPasses(t *testing.T) {
	state := &cloudAgentRuntime{Request: CloudAgentRequest{Workflow: "image_to_director"}}
	state.DirectorWorkflowPhase = "blocked"
	if !directorWorkflowRollbackEligible(state) {
		t.Fatal("blocked image workflow should be rollback eligible")
	}
	state.DirectorWorkflowPhase = "checked"
	if directorWorkflowRollbackEligible(state) {
		t.Fatal("continuity-checked workflow must not be silently rolled back")
	}
}
