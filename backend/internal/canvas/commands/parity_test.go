package commands

import (
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/contract"
	"testing"
)

func TestDrawingNativeEditAndAtomicRejection(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"d","type":"drawing","metadata":{"drawingEngine":"excalidraw"}}],"connections":[]}`)
	op := Operation{Type: "edit_drawing", ID: "d", Engine: "excalidraw", DrawingOperations: []DrawingOperation{{Type: "upsert", ID: "rect", Record: map[string]any{"id": "rect", "type": "rectangle", "x": 1.0, "y": 2.0, "width": 100.0, "height": 80.0}}}}
	result, err := Plan(d, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	wrapper := entity(result.Document, "node:d")["metadata"].(map[string]any)["drawingDocument"].(map[string]any)
	if wrapper["shapeCount"] != 1 || wrapper["revision"] != 1 {
		t.Fatalf("native drawing not saved: %v", wrapper)
	}
	before, _ := json.Marshal(result.Document)
	op.DrawingOperations = append(op.DrawingOperations, DrawingOperation{Type: "upsert", ID: "bad", Record: map[string]any{"id": "bad", "type": "image", "fileId": "missing"}})
	_, err = Plan(result.Document, command(op), ownerContext())
	after, _ := json.Marshal(result.Document)
	if err == nil || string(before) != string(after) {
		t.Fatal("unbound image was accepted or changed the input")
	}
}
func TestGenerationDraftRejectsDanglingReferences(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"target","type":"image","metadata":{}}],"connections":[]}`)
	spec := contract.GenerationSpec{Version: 1, Mode: "image", Prompt: "draft", TextInputMode: "append-sources", ReferenceBindings: []contract.ReferenceBinding{{ID: "source1", NodeID: "missing", MediaType: "text", Role: "source-text", Order: 0, Resolution: "latest"}}}
	encoded, _ := json.Marshal(spec)
	var patch map[string]any
	json.Unmarshal(encoded, &patch)
	if _, err := Plan(d, command(Operation{Type: "update_node", ID: "target", Patch: map[string]any{"generationSpec": patch}}), ownerContext()); err == nil {
		t.Fatal("dangling generation reference accepted")
	}
}
func TestStoryboardAndBatchRowsPreserveExtensions(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"s","type":"script","metadata":{"storyboard":{"rows":[]}}},{"id":"b","type":"batch-table","metadata":{"batchTable":{"operation":"creative","concurrency":1,"referenceColumns":[{"id":"ref","label":"参考图"}],"rows":[]}}}],"connections":[]}`)
	result, err := Plan(d, command(Operation{Type: "edit_storyboard", ID: "s", Action: "append", RowID: "shot1", Patch: map[string]any{"durationSeconds": 5.0, "plotDescription": "场景"}}, Operation{Type: "edit_batch_table", ID: "b", Action: "append", RowID: "row1", Patch: map[string]any{"prompt": "创作图像"}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	node := entity(result.Document, "node:s")
	rows := maps(node["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"])
	rows[0]["extension"] = map[string]any{"preserve": true}
	result, err = Plan(result.Document, command(Operation{Type: "edit_storyboard", ID: "s", Action: "update", RowID: "shot1", Patch: map[string]any{"plotDescription": "修改场景"}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	rows = maps(entity(result.Document, "node:s")["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"])
	if rows[0]["extension"] == nil || rows[0]["id"] != "shot1" || rows[0]["plotDescription"] != "修改场景" {
		t.Fatalf("row identity or extension lost: %v", rows)
	}
}
func TestPrevisCreateAndPatch(t *testing.T) {
	result, err := Plan(fixture(t, `{"nodes":[],"connections":[]}`), command(Operation{Type: "create_previs_scene", ID: "scene1", TemplateID: "empty", Title: ptr("场景")}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	result, err = Plan(result.Document, command(Operation{Type: "edit_previs_scene", ID: "scene1", PrevisOperations: []PrevisOperation{{Type: "scene_update", ID: "scene1", Title: ptr("新场景")}}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	scenes := maps(result.Document["previsScenes"])
	if len(scenes) != 1 || scenes[0]["title"] != "新场景" {
		t.Fatalf("previs patch missing: %v", scenes)
	}
}
func ptr(s string) *string { return &s }
func TestResourcesRequireOwnership(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"a","type":"image","metadata":{"storageKey":"resource:secret-resource"}}],"connections":[]}`)
	if _, err := Plan(d, command(Operation{Type: "update_node", ID: "a", Patch: map[string]any{"title": "new"}}), ownerContext()); err == nil {
		t.Fatal("unowned resource accepted")
	}
}
func TestProseIsNotAResourceReference(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"a","type":"text","metadata":{"content":"resource:example"}}],"connections":[]}`)
	if _, err := Plan(d, command(Operation{Type: "update_node", ID: "a", Patch: map[string]any{"title": "tutorial"}}), ownerContext()); err != nil {
		t.Fatal(err)
	}
}
func TestModelReferenceCapacityIsChecked(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"i1","type":"image","metadata":{}},{"id":"i2","type":"image","metadata":{}},{"id":"v","type":"video","metadata":{"generationMode":"video"}}],"connections":[{"id":"e1","fromNodeId":"i1","toNodeId":"v"}]}`)
	ctx := ownerContext()
	ctx.ModelInputLimits = map[string]map[string]int{"video": {"image": 1, "video": 0, "audio": 0}}
	if _, err := Plan(d, command(Operation{Type: "connect_nodes", ID: "e2", FromNodeID: "i2", ToNodeID: "v"}), ctx); err == nil {
		t.Fatal("model capacity ignored")
	}
}
