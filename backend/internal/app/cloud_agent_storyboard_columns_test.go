package app

import (
	"reflect"
	"testing"

	"infinite-canvas/backend/internal/canvas/capability"
)

func storyboardDefaultColumnValues() []any {
	return capability.StoryboardDefaultColumnValues()
}

func storyboardColumnValues(columns ...string) []any {
	values := make([]any, len(columns))
	for index, column := range columns {
		values[index] = column
	}
	return values
}

func TestCloudAgentStoryboardReadExposesVisibleColumns(t *testing.T) {
	legacy := map[string]any{
		"visibleColumns": storyboardColumnValues("shotNumber", "durationSeconds", "videoMotionPrompt", "dialogue", "assets"),
		"rows":           []any{map[string]any{"id": "shot-1", "shotNumber": 1.0, "durationSeconds": 3.0, "plotDescription": "画面"}},
	}
	for _, mode := range []cloudAgentProjectionMode{cloudAgentProjectionIndex, cloudAgentProjectionPage, cloudAgentProjectionDetail} {
		state := cloudAgentStoryboardState(legacy, 0, mode)
		if got := state["visibleColumns"]; !reflect.DeepEqual(got, storyboardDefaultColumnValues()) {
			t.Fatalf("mode %d exposed legacy columns instead of the current default: %v", mode, got)
		}
	}

	manual := map[string]any{
		"visibleColumns": storyboardColumnValues("shotNumber", "dialogue"),
		"rows":           []any{},
	}
	if got := cloudAgentStoryboardState(manual, 0, cloudAgentProjectionDetail)["visibleColumns"]; !reflect.DeepEqual(got, storyboardColumnValues("shotNumber", "dialogue")) {
		t.Fatalf("manual columns were rewritten: %v", got)
	}

	missing := map[string]any{"rows": []any{}}
	if got := cloudAgentStoryboardState(missing, 0, cloudAgentProjectionDetail)["visibleColumns"]; !reflect.DeepEqual(got, storyboardDefaultColumnValues()) {
		t.Fatalf("missing columns did not fall back to the current default: %v", got)
	}

	// canvas_read_storyboard wraps the projected storyboard state, so the tool
	// result must carry the same normalized columns.
	view := map[string]any{"snapshotHash": "hash", "nodes": []any{map[string]any{"title": "分镜", "storyboard": cloudAgentStoryboardState(legacy, 0, cloudAgentProjectionDetail)}}}
	result, err := cloudAgentStoryboardReadResult(view, "storyboard-1")
	if err != nil {
		t.Fatal(err)
	}
	storyboard := result["storyboard"].(map[string]any)
	if got := storyboard["visibleColumns"]; !reflect.DeepEqual(got, storyboardDefaultColumnValues()) {
		t.Fatalf("canvas_read_storyboard result lost normalized columns: %v", got)
	}
}

func TestCloudAgentStoryboardWritesUseDefaultColumns(t *testing.T) {
	doc := map[string]any{"nodes": []any{}, "connections": []any{}}
	if _, err := applyCloudAgentCanvasPlan(doc, []agentCanvasOp{
		{Type: "add_node", ID: "script-1", NodeType: "script", Title: ptr("分镜")},
		{Type: "add_node", ID: "image-1", NodeType: "image", Title: ptr("参考图")},
	}); err != nil {
		t.Fatal(err)
	}
	node := creationMaps(doc["nodes"])[0]
	storyboard := node["metadata"].(map[string]any)["storyboard"].(map[string]any)
	if got := storyboard["visibleColumns"]; !reflect.DeepEqual(got, storyboardDefaultColumnValues()) {
		t.Fatalf("add_node script metadata = %v, want current default", got)
	}

	// Older canvases have storyboard nodes without visibleColumns; attaching a
	// row asset must seed the current default instead of the retired one.
	nodes := []map[string]any{
		{"id": "script-1", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{"id": "row-1"}}}}},
		{"id": "image-1", "type": "image"},
	}
	if notes := attachCloudAgentConnection(nodes, agentCanvasOp{Type: "connect_nodes", FromNodeID: "image-1", ToNodeID: "script-1", ToHandleID: "row:row-1"}); len(notes) == 0 {
		t.Fatal("connection did not attach the row asset")
	}
	seeded := nodes[0]["metadata"].(map[string]any)["storyboard"].(map[string]any)
	if got := seeded["visibleColumns"]; !reflect.DeepEqual(got, storyboardDefaultColumnValues()) {
		t.Fatalf("connection seeding used stale columns: %v", got)
	}
}

func TestCloudAgentCreateStoryboardUsesDefaultColumns(t *testing.T) {
	s, _, canvas := cloudAgentStoryboardFixture(t)
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentStoryboardCall(t, "canvas_create_storyboard", "create-columns", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc),
		"nodeId":       "storyboard-columns",
		"title":        "默认列分镜",
		"rows":         []map[string]any{{"durationSeconds": 3.0, "plotDescription": "雨夜开场"}},
	})
	plan, err := prepareCloudAgentStoryboardCreate(s.repo, "user", canvas.ID, call)
	if err != nil {
		t.Fatal(err)
	}
	var created map[string]any
	for _, node := range creationMaps(plan.Document["nodes"]) {
		if stringValue(node["id"]) == "storyboard-columns" {
			created = node
			break
		}
	}
	if created == nil {
		t.Fatal("create plan lost the storyboard node")
	}
	storyboard := created["metadata"].(map[string]any)["storyboard"].(map[string]any)
	if got := storyboard["visibleColumns"]; !reflect.DeepEqual(got, storyboardDefaultColumnValues()) {
		t.Fatalf("canvas_create_storyboard columns = %v, want current default", got)
	}
}
