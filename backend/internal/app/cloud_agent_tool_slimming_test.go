package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCloudAgentRetiredImageToolsAreNotCallable(t *testing.T) {
	retired := []string{"canvas_read_drawing", "canvas_edit_drawing", "image_layer_split", "image_text_detect", "image_annotation_render", "skill_search", "skill_read_file", "agent_profile_read"}
	for _, permission := range []string{"auto", "request_approval", "read_only"} {
		req := tierAutoRequest()
		req.PermissionMode = permission
		for _, mode := range []string{cloudAgentSkillRuntimeLegacy, cloudAgentSkillRuntimeNative} {
			names := cloudAgentToolNames(compileCloudAgentToolsForRuntime(req, mode))
			for _, name := range retired {
				if containsToolName(names, name) || cloudAgentToolAllowed(req, name) {
					t.Fatalf("retired tool %s remains callable in %s/%s", name, permission, mode)
				}
			}
		}
	}
	for _, name := range retired {
		if containsToolName(CloudAgentSupportedToolNames(), name) || containsToolName(cloudAgentToolNames(CloudAgentToolSchemaArtifact().Tools), name) {
			t.Fatalf("retired tool %s remains in the shared catalog", name)
		}
	}
	capabilities, err := cloudAgentNodeTypesFiltered("drawing")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(capabilities)
	for _, name := range retired {
		if strings.Contains(string(raw), name) {
			t.Fatalf("drawing capabilities still recommend retired tool %s", name)
		}
	}
}

func TestCloudAgentRetiredDirectorToolsAreNotCallable(t *testing.T) {
	tools := []string{"previs_scene_read", "previs_scene_create", "previs_apply_patch", "previs_preview", "director_block_scene", "director_design_shot", "director_draw_action", "director_preview_shot", "director_check_continuity", "director_handoff_shot"}
	inventory := cloudAgentScanCanvasInventory(`{"nodes":[{"id":"legacy","type":"video","metadata":{"previsSceneId":"scene-1"}}],"previsScenes":[{"id":"scene-1"}]}`)
	for _, permission := range []string{"auto", "request_approval", "read_only"} {
		req := tierAutoRequest()
		req.PermissionMode = permission
		for _, runtimeMode := range []string{cloudAgentSkillRuntimeLegacy, cloudAgentSkillRuntimeNative} {
			names := cloudAgentToolNames(compileCloudAgentToolsForRuntime(req, runtimeMode))
			for _, name := range tools {
				if containsToolName(names, name) || cloudAgentToolAllowed(req, name) {
					t.Fatalf("retired tool %s remains callable in %s/%s", name, permission, runtimeMode)
				}
				// A saved pre-removal loadout must not override the current server gate.
				state := &cloudAgentRuntime{
					Request: req, SkillRuntimeMode: runtimeMode,
					Canonical:         canonicalAgentRequest{Tools: []map[string]any{{"function": map[string]any{"name": name, "parameters": map[string]any{"type": "object"}}}}},
					DisclosureVersion: cloudAgentToolDisclosureVersion, AdvertisedToolNames: []string{name},
				}
				admission := cloudAgentPreflightBatch(state, []cloudAgentCall{categoryCall(name)})[0]
				if admission.Allowed || admission.Issue != cloudAgentAdmissionPermission {
					t.Fatalf("frozen legacy loadout bypassed retirement of %s: %+v", name, admission)
				}
			}
		}
		for _, prompt := range []string{"现在画布有几个节点", "打开导演台", "创建一个预演场景", "use director desk", "继续当前场景"} {
			names := cloudAgentAdvertisedToolNames(req, inventory, prompt)
			for _, name := range tools {
				if containsToolName(names, name) {
					t.Fatalf("retired tool %s was redisclosed for %s/%q", name, permission, prompt)
				}
			}
		}
		if !cloudAgentToolAllowed(req, "canvas_get_state") || permission != "read_only" && !cloudAgentToolAllowed(req, "generate_media") {
			t.Fatal("ordinary canvas reading or media generation was removed")
		}
	}
	for _, name := range tools {
		if containsToolName(CloudAgentSupportedToolNames(), name) || containsToolName(cloudAgentToolNames(CloudAgentToolSchemaArtifact().Tools), name) {
			t.Fatalf("retired tool %s remains in the shared catalog", name)
		}
	}
}

func TestCloudAgentRetiredDirectorIntentCannotUnlockTools(t *testing.T) {
	state := &cloudAgentRuntime{Canonical: canonicalAgentRequest{Tools: cloudAgentTools(tierAutoRequest())}}
	cloudAgentRecordDeliveredToolIntent(state, cloudAgentInterjection{Source: "subagent", Text: "打开导演台并做预演"})
	if len(state.DeliveredIntentToolNames) != 0 {
		t.Fatal("subagent text unlocked director tools")
	}
	cloudAgentRecordDeliveredToolIntent(state, cloudAgentInterjection{Source: "user", Text: "打开导演台并做预演"})
	if len(state.DeliveredIntentToolNames) != 0 {
		t.Fatalf("delivered user intent unlocked retired director tools: %v", state.DeliveredIntentToolNames)
	}
}

func TestNativeSkillReadRemainsAvailable(t *testing.T) {
	names := cloudAgentToolNames(compileCloudAgentToolsForRuntime(tierAutoRequest(), cloudAgentSkillRuntimeNative))
	if !containsToolName(names, "read") || containsToolName(names, "skill") {
		t.Fatalf("native Skill must keep the read tool: %v", names)
	}
}
