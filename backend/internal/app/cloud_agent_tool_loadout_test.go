package app

import (
	"encoding/json"
	"testing"
)

func tierAutoRequest() CloudAgentRequest {
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, SkillIDs: []string{"skill"}, VisionEnabled: true}
	req.Budget.MaxGenerationTasks = 1
	return req
}

func tierPayload(nodes ...map[string]any) string {
	raw, err := json.Marshal(map[string]any{"nodes": nodes, "connections": []any{}})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func tierNode(id, nodeType string, metadata map[string]any) map[string]any {
	node := map[string]any{"id": id, "type": nodeType, "title": id}
	if metadata != nil {
		node["metadata"] = metadata
	}
	return node
}

// 有画布时一轮下发完整的合格目录：批量表、分镜、角色卡和素材工具不再依赖提示词关键词或画布内容。
func TestCloudAgentLoadoutAdvertisesTheCompleteEligibleCatalog(t *testing.T) {
	req := tierAutoRequest()
	eligible := cloudAgentToolNames(cloudAgentTools(req))
	advertised := cloudAgentAdvertisedToolNames(req)
	if len(advertised) != len(eligible) {
		t.Fatalf("advertised must equal eligible: advertised=%v eligible=%v", advertised, eligible)
	}
	for index := range eligible {
		if advertised[index] != eligible[index] {
			t.Fatalf("advertised order must follow the eligible catalog: %v vs %v", advertised, eligible)
		}
	}
	for _, want := range []string{"canvas_read_batch_table", "canvas_edit_batch_table", "storyboard", "canvas_create_character", "canvas_list_assets", "canvas_bind_asset", "canvas_apply_ops", "generate_media"} {
		if !containsToolName(advertised, want) {
			t.Fatalf("canvas run missing %q: %v", want, advertised)
		}
	}
}

func TestCloudAgentLoadoutReadOnlyStillHidesWrites(t *testing.T) {
	req := tierAutoRequest()
	req.PermissionMode = "read_only"
	names := cloudAgentAdvertisedToolNames(req)
	for _, hidden := range []string{"canvas_apply_ops", "canvas_arrange_nodes", "generate_media", "canvas_edit_batch_table"} {
		if containsToolName(names, hidden) {
			t.Fatalf("read_only advertised write/generate tool %q: %v", hidden, names)
		}
	}
	for _, want := range []string{"storyboard", "canvas_read_batch_table", "canvas_get_state"} {
		if !containsToolName(names, want) {
			t.Fatalf("read_only missing read tool %q: %v", want, names)
		}
	}
}

// 每步刷新保持冻结的清单，只剔除当前权限不再允许的工具；用户插话里的关键词不会增减工具。
func TestCloudAgentLoadoutRefreshOnlyDropsDisallowedTools(t *testing.T) {
	req := tierAutoRequest()
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
	state.AdvertisedToolNames = cloudAgentAdvertisedToolNames(req)
	frozen := append([]string(nil), state.AdvertisedToolNames...)
	state.Canonical.Messages = []map[string]any{{"role": "user", "content": "批量创作一组海报", cloudAgentContextSourceKey: "user_interjection"}}
	next := cloudAgentNextAdvertisedToolNames(state)
	if len(next) != len(frozen) {
		t.Fatalf("interjection keywords changed the loadout: %v -> %v", frozen, next)
	}
	state.ActiveTaskID = "inflight"
	state.Request.PermissionMode = "read_only"
	if got := cloudAgentNextAdvertisedToolNames(state); len(got) != len(frozen) {
		t.Fatalf("an in-flight model step must keep its admitted loadout: %v", got)
	}
	state.ActiveTaskID = ""
	afterPermission := cloudAgentNextAdvertisedToolNames(state)
	if containsToolName(afterPermission, "canvas_apply_ops") || containsToolName(afterPermission, "generate_media") {
		t.Fatalf("switching to read_only must drop writes at the next boundary: %v", afterPermission)
	}
	if !containsToolName(afterPermission, "canvas_get_state") {
		t.Fatalf("read tools must survive a permission switch: %v", afterPermission)
	}
	state.Request.PermissionMode = "auto"
	if regained := cloudAgentNextAdvertisedToolNames(&cloudAgentRuntime{Request: state.Request, Canonical: state.Canonical, AdvertisedToolNames: afterPermission}); !containsToolName(regained, "canvas_apply_ops") {
		t.Fatalf("loosening the permission must bring catalog tools back at the next boundary: %v", regained)
	}
}

func TestCloudAgentLoadoutEmptyAdvertisedFallsBackToEligible(t *testing.T) {
	req := tierAutoRequest()
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
	if names := cloudAgentNextAdvertisedToolNames(state); len(names) != len(cloudAgentToolNames(state.Canonical.Tools)) {
		t.Fatalf("legacy runs without a frozen loadout use the eligible catalog: %v", names)
	}
}
