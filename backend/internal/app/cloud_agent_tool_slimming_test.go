package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCloudAgentRetiredImageToolsAreNotCallable(t *testing.T) {
	retired := []string{"canvas_read_drawing", "canvas_edit_drawing", "image_layer_split", "image_text_detect", "image_annotation_render", "skill_search", "skill_read_file"}
	for _, permission := range []string{"auto", "request_approval", "read_only"} {
		req := tierAutoRequest()
		req.PermissionMode = permission
		for _, mode := range []string{cloudAgentSkillRuntimeLegacy, cloudAgentSkillRuntimeNative} {
			names := cloudAgentToolNames(compileCloudAgentToolsForRuntime(req, true, mode))
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

func TestCloudAgentToolTierPrevisRequiresRelatedContext(t *testing.T) {
	tools := []string{"previs_scene_read", "previs_scene_create", "previs_apply_patch", "previs_preview", "director_block_scene", "director_design_shot", "director_draw_action", "director_preview_shot", "director_check_continuity", "director_handoff_shot"}
	for _, prompt := range []string{"现在画布有几个节点", "根据参考图生成图片并标注文字", "请写分镜和镜头提示词"} {
		names := cloudAgentAdvertisedToolNames(tierAutoRequest(), cloudAgentCanvasInventory{}, prompt)
		for _, name := range tools {
			if containsToolName(names, name) {
				t.Fatalf("unrelated prompt %q disclosed %s", prompt, name)
			}
		}
	}
	for _, prompt := range []string{"创建一个预演场景", "打开导演台", "create a previs scene", "use director desk"} {
		names := cloudAgentAdvertisedToolNames(tierAutoRequest(), cloudAgentCanvasInventory{}, prompt)
		for _, name := range tools {
			if !containsToolName(names, name) {
				t.Fatalf("explicit prompt %q did not disclose %s", prompt, name)
			}
		}
	}
	inventory := cloudAgentScanCanvasInventory(`{"nodes":[],"previsScenes":[{"id":"scene-1"}]}`)
	req := tierAutoRequest()
	req.PermissionMode = "read_only"
	names := cloudAgentAdvertisedToolNames(req, inventory, "继续当前场景")
	for _, name := range tools {
		want := name == "previs_scene_read" || name == "director_check_continuity" || name == "director_handoff_shot"
		if containsToolName(names, name) != want {
			t.Fatalf("existing scene bypassed permissions or lost read tool %s: %v", name, names)
		}
	}
}

func TestCloudAgentToolTierDirectorIntentCannotComeFromSubagent(t *testing.T) {
	state := &cloudAgentRuntime{Canonical: canonicalAgentRequest{Tools: cloudAgentTools(tierAutoRequest())}}
	cloudAgentRecordDeliveredToolIntent(state, cloudAgentInterjection{Source: "subagent", Text: "打开导演台并做预演"})
	if len(state.DeliveredIntentToolNames) != 0 {
		t.Fatal("subagent text unlocked director tools")
	}
	cloudAgentRecordDeliveredToolIntent(state, cloudAgentInterjection{Source: "user", Text: "打开导演台并做预演"})
	if !containsToolName(state.DeliveredIntentToolNames, "director_design_shot") || !containsToolName(state.DeliveredIntentToolNames, "previs_scene_create") {
		t.Fatalf("delivered user intent did not unlock director tools: %v", state.DeliveredIntentToolNames)
	}
}

func TestNativeSkillReadRemainsAvailable(t *testing.T) {
	names := cloudAgentToolNames(compileCloudAgentToolsForRuntime(tierAutoRequest(), true, cloudAgentSkillRuntimeNative))
	if !containsToolName(names, "read") || containsToolName(names, "skill") {
		t.Fatalf("native Skill must keep the read tool: %v", names)
	}
}
