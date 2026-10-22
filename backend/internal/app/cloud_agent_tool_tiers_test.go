package app

import (
	"encoding/json"
	"fmt"
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

func TestCloudAgentToolTierEmptyCanvasKeepsL0AndHidesHeavyL1(t *testing.T) {
	req := tierAutoRequest()
	inventory := cloudAgentScanCanvasInventory(tierPayload())
	names := cloudAgentAdvertisedToolNames(req, inventory, "现在画布有几个节点")
	for _, want := range []string{"canvas_get_state", "canvas_apply_ops", "generate_media"} {
		if !containsToolName(names, want) {
			t.Fatalf("empty canvas Q&A missing L0 tool %q: %v", want, names)
		}
	}
	for _, hidden := range []string{"canvas_edit_storyboard", "canvas_edit_batch_table", "previs_scene_create", "director_design_shot"} {
		if containsToolName(names, hidden) {
			t.Fatalf("empty canvas Q&A advertised heavy L1 tool %q: %v", hidden, names)
		}
	}
}

func TestCloudAgentToolTierInventoryUnlocksMatchingL1(t *testing.T) {
	req := tierAutoRequest()
	cases := []struct {
		name    string
		payload string
		want    []string
	}{
		{"script", tierPayload(tierNode("board", "script", map[string]any{"workflowKind": "script"})), []string{"canvas_read_storyboard", "canvas_create_storyboard", "canvas_edit_storyboard"}},
		{"batch-table", tierPayload(tierNode("table", "batch-table", nil)), []string{"canvas_read_batch_table", "canvas_edit_batch_table"}},
		{"character", tierPayload(tierNode("hero", "text", map[string]any{"workflowKind": "character"})), []string{"canvas_create_character"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inventory := cloudAgentScanCanvasInventory(tc.payload)
			names := cloudAgentAdvertisedToolNames(req, inventory, "继续当前画布工作")
			for _, want := range tc.want {
				if !containsToolName(names, want) {
					t.Fatalf("inventory %s missing %q: %v", tc.name, want, names)
				}
			}
		})
	}
}

func TestCloudAgentToolTierPromptIntentUnlocksStoryboardOnEmptyCanvas(t *testing.T) {
	req := tierAutoRequest()
	inventory := cloudAgentScanCanvasInventory(tierPayload())
	names := cloudAgentAdvertisedToolNames(req, inventory, "帮我写分镜")
	for _, want := range []string{"canvas_read_storyboard", "canvas_create_storyboard", "canvas_edit_storyboard"} {
		if !containsToolName(names, want) {
			t.Fatalf("storyboard intent missing %q: %v", want, names)
		}
	}
}

func TestCloudAgentToolTierReadOnlyHidesWritesAndKeepsStoryboardRead(t *testing.T) {
	req := tierAutoRequest()
	req.PermissionMode = "read_only"
	inventory := cloudAgentScanCanvasInventory(tierPayload(tierNode("board", "script", nil)))
	names := cloudAgentAdvertisedToolNames(req, inventory, "请分析这份分镜")
	for _, hidden := range []string{"canvas_apply_ops", "generate_media", "canvas_create_storyboard", "canvas_edit_storyboard"} {
		if containsToolName(names, hidden) {
			t.Fatalf("read_only advertised write/generate tool %q: %v", hidden, names)
		}
	}
	if !containsToolName(names, "canvas_read_storyboard") {
		t.Fatalf("read_only with script node missing canvas_read_storyboard: %v", names)
	}
}

func TestCloudAgentToolTierInventoryScansFullPayloadPastCatalogTruncation(t *testing.T) {
	nodes := make([]map[string]any, 0, cloudAgentCanvasSummaryMaxNodes+2)
	for i := 0; i < cloudAgentCanvasSummaryMaxNodes; i++ {
		nodes = append(nodes, tierNode(fmt.Sprintf("text-%d", i), "text", nil))
	}
	nodes = append(nodes, tierNode("hidden-board", "script", nil))
	payload := tierPayload(nodes...)
	inventory := cloudAgentScanCanvasInventory(payload)
	if inventory.TotalNodes != cloudAgentCanvasSummaryMaxNodes+1 {
		t.Fatalf("inventory total=%d want %d", inventory.TotalNodes, cloudAgentCanvasSummaryMaxNodes+1)
	}
	if !cloudAgentInventoryHasType(inventory, "script") {
		t.Fatalf("truncated catalog must not hide trailing script node: %+v", inventory)
	}
	names := cloudAgentAdvertisedToolNames(tierAutoRequest(), inventory, "现在画布有几个节点")
	if !containsToolName(names, "canvas_read_storyboard") {
		t.Fatalf("script past catalog limit did not unlock storyboard read: %v", names)
	}
}

func TestCloudAgentToolTierAdvertisedIsSubsetOfEligibleCatalog(t *testing.T) {
	req := tierAutoRequest()
	eligible := cloudAgentToolNames(cloudAgentTools(req))
	for _, want := range []string{"canvas_edit_storyboard", "canvas_edit_batch_table", "previs_scene_create", "director_design_shot"} {
		if !containsToolName(eligible, want) {
			t.Fatalf("eligible catalog missing L1 tool %q: %v", want, eligible)
		}
	}
	advertised := cloudAgentAdvertisedToolNames(req, cloudAgentScanCanvasInventory(tierPayload()), "现在画布有几个节点")
	if len(advertised) == 0 || len(advertised) >= len(eligible) {
		t.Fatalf("advertised should be a strict subset of eligible: advertised=%d eligible=%d names=%v", len(advertised), len(eligible), advertised)
	}
	eligibleSet := map[string]bool{}
	for _, name := range eligible {
		eligibleSet[name] = true
	}
	for _, name := range advertised {
		if !eligibleSet[name] {
			t.Fatalf("advertised tool %q is not in eligible catalog", name)
		}
	}
}

func TestCloudAgentPromptIntentFlagsUseClosedLexicon(t *testing.T) {
	empty := cloudAgentPromptIntentFlags("现在画布有几个节点")
	if empty.Storyboard || empty.BatchTable || empty.Previs || empty.Arrange {
		t.Fatalf("plain node-count prompt must not set L1 intent: %+v", empty)
	}
	storyboard := cloudAgentPromptIntentFlags("帮我写分镜")
	if !storyboard.Storyboard {
		t.Fatal("「帮我写分镜」must set storyboard intent")
	}
}

func TestCloudAgentScanCanvasInventoryCharacterAndTypes(t *testing.T) {
	inventory := cloudAgentScanCanvasInventory(tierPayload(
		tierNode("draw", "drawing", nil),
		tierNode("hero", "text", map[string]any{"workflowKind": "character"}),
	))
	if inventory.TotalNodes != 2 || !inventory.HasCharacter || !cloudAgentInventoryHasType(inventory, "drawing") {
		t.Fatalf("inventory=%+v", inventory)
	}
	broken := cloudAgentScanCanvasInventory("{")
	if broken.TotalNodes != 0 || broken.HasCharacter || len(broken.NodeTypes) != 0 {
		t.Fatalf("invalid payload should yield empty inventory: %+v", broken)
	}
}
