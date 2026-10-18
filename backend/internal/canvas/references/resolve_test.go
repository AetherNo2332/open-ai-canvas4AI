package references

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"infinite-canvas/backend/internal/canvas/capability"
)

func fixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/reference-fields-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}
func fixtureRow(doc map[string]any) map[string]any {
	return doc["nodes"].([]any)[3].(map[string]any)["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"].([]any)[0].(map[string]any)
}
func TestResolveIgnoresStaleEdges(t *testing.T) {
	doc := fixture(t)
	fixtureRow(doc)["assetBindings"] = []any{}
	got, err := Resolve(doc, Scope{Kind: "storyboard-row", NodeID: "board", RowID: "shot", Mode: "image"}, capability.BuiltinRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bindings) != 0 {
		t.Fatal("empty fields recovered stale edge input")
	}
}
func TestResolveStableRolesAndOrder(t *testing.T) {
	got, err := Resolve(fixture(t), Scope{Kind: "storyboard-row", NodeID: "board", RowID: "shot", Mode: "video"}, capability.BuiltinRegistry())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, b := range got.Bindings {
		ids = append(ids, b.NodeID)
	}
	if !reflect.DeepEqual(ids, []string{"hero", "scene"}) {
		t.Fatalf("reference ordering: %v", ids)
	}
}
func TestResolveKeyframeMode(t *testing.T) {
	doc := fixture(t)
	doc["nodes"].([]any)[3].(map[string]any)["metadata"].(map[string]any)["storyboardVideoInputMode"] = "keyframe"
	got, err := Resolve(doc, Scope{Kind: "storyboard-row", NodeID: "board", RowID: "shot", Mode: "video"}, capability.BuiltinRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Bindings) != 3 || got.Bindings[0].NodeID != "frame" || got.Bindings[0].Role != "first-frame" {
		t.Fatalf("first frame missing: %+v", got.Bindings)
	}
	delete(fixtureRow(doc), "imageNodeId")
	if _, err = Resolve(doc, Scope{Kind: "storyboard-row", NodeID: "board", RowID: "shot", Mode: "video"}, capability.BuiltinRegistry()); err == nil {
		t.Fatal("keyframe mode accepted a missing frame")
	}
}
func TestReferenceInputPolicies(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		limit int
	}{{"compare", 2}, {"panorama", 1}, {"batch-table", 10}} {
		t.Run(tc.kind, func(t *testing.T) {
			for count := tc.limit; count <= tc.limit+1; count++ {
				nodes := []any{}
				bindings := []any{}
				ids := []any{}
				for i := 0; i < count; i++ {
					id := string(rune('a' + i))
					nodes = append(nodes, map[string]any{"id": id, "type": "image"})
					ids = append(ids, id)
					bindings = append(bindings, map[string]any{"id": id, "nodeId": id, "mediaType": "image", "role": "reference", "order": i, "resolution": "latest"})
				}
				meta := map[string]any{"inputBindings": bindings}
				scope := Scope{Kind: "node", NodeID: "target"}
				if tc.kind == "batch-table" {
					meta = map[string]any{"batchTable": map[string]any{"rows": []any{map[string]any{"id": "row", "inputNodeIds": ids}}}}
					scope = Scope{Kind: "batch-row", NodeID: "target", RowID: "row"}
				}
				nodes = append(nodes, map[string]any{"id": "target", "type": tc.kind, "metadata": meta})
				_, err := Resolve(map[string]any{"nodes": nodes}, scope, capability.BuiltinRegistry())
				if (err != nil) != (count > tc.limit) {
					t.Fatalf("%d inputs: %v", count, err)
				}
			}
		})
	}
}
