package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestAgentParityCanvasDeltaCoversDeletionAndOrder(t *testing.T) {
	objects := func(ids ...string) []any {
		items := []any{}
		for _, id := range ids {
			items = append(items, map[string]any{"id": id})
		}
		return items
	}
	before := map[string]any{"nodes": objects("a", "b", "c"), "connections": objects("e1", "e2"), "title": "canvas"}
	for name, after := range map[string]map[string]any{
		"delete":  {"nodes": objects("a", "b"), "connections": objects("e2"), "title": "canvas"},
		"reorder": {"nodes": objects("c", "b", "a"), "connections": objects("e2", "e1"), "title": "canvas"},
		"mixed":   {"nodes": objects("c", "new", "a"), "connections": objects("new-edge", "e1"), "title": "canvas"},
	} {
		t.Run(name, func(t *testing.T) {
			if !cloudAgentPatchCoversDocument(before, after) {
				t.Fatal("complete keyed deletion/order delta was rejected")
			}
		})
	}
	for name, invalid := range map[string]any{
		"duplicate": objects("a", "a"), "missing": []any{map[string]any{}}, "non-string": []any{map[string]any{"id": 1}},
		"non-object": []any{"node"}, "non-array": map[string]any{"id": "a"}, "space": objects(" a"),
	} {
		for _, key := range []string{"nodes", "connections"} {
			t.Run(name+"/"+key, func(t *testing.T) {
				bad := map[string]any{"nodes": objects("a"), "connections": objects("e1")}
				bad[key] = invalid
				if cloudAgentPatchCoversDocument(bad, bad) {
					t.Fatal("malformed ID collection acknowledged as a complete delta")
				}
			})
		}
	}
	after := map[string]any{"nodes": objects("c", "a"), "connections": objects("e1"), "title": "changed outside the delta"}
	if cloudAgentPatchCoversDocument(before, after) {
		t.Fatal("top-level change omitted from the delta was acknowledged")
	}
	after["title"] = "canvas"
	after["unknownDocumentField"] = true
	if cloudAgentPatchCoversDocument(before, after) {
		t.Fatal("unknown top-level field omitted from the delta was acknowledged")
	}
}

func TestAgentParityStoryboardMixedMembershipAndOrderKeepsFullRows(t *testing.T) {
	node := func(ids ...string) map[string]any {
		rows := []map[string]any{}
		for _, id := range ids {
			rows = append(rows, map[string]any{"id": id, "plotDescription": "body " + id})
		}
		return map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": rows}}}
	}
	for name, pair := range map[string][2]map[string]any{
		"append-and-reorder":  {node("a", "b"), node("b", "a", "new")},
		"remove-and-reorder":  {node("a", "b", "c"), node("c", "a")},
		"replace-and-reorder": {node("a", "b", "c"), node("c", "new", "a")},
		"restore-middle-row":  {node("a", "c"), node("a", "b", "c")},
		"restore-first-row":   {node("b", "c"), node("a", "b", "c")},
	} {
		t.Run(name, func(t *testing.T) {
			before, after := cloudAgentShrinkChange(pair[0], pair[1])
			if !reflect.DeepEqual(rowsOfNode(t, before), rowsOfNode(t, pair[0])) || !reflect.DeepEqual(rowsOfNode(t, after), rowsOfNode(t, pair[1])) {
				t.Fatal("sparse row delta lost retained-row ordering")
			}
		})
	}
	before, after := cloudAgentShrinkChange(node("a", "b"), node("a", "b", "new"))
	if len(rowsOfNode(t, before)) != 0 || len(rowsOfNode(t, after)) != 1 || rowsOfNode(t, after)[0]["id"] != "new" {
		t.Fatal("ordinary append unnecessarily lost the sparse-row delta")
	}
}

func TestAgentParityDatabaseCanvasDeletionAndReorderEmitCompleteDelta(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	canvas := model.CanvasProject{ID: "parity-canvas", UserID: "user", PayloadJSON: `{"nodes":[{"id":"a","type":"text"},{"id":"b","type":"text"},{"id":"c","type":"text"}],"connections":[{"id":"gone-edge","fromNodeId":"a","toNodeId":"b"},{"id":"keep-edge","fromNodeId":"a","toNodeId":"c"}]}`}
	if err := db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}
	before := canvas.PayloadJSON
	doc, _ := creationDocument(before)
	nodes := creationMaps(doc["nodes"])
	doc["nodes"] = []map[string]any{nodes[2], nodes[0]}
	doc["connections"] = creationMaps(doc["connections"])[1:]
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveCloudAgentDocument(s.repo, &canvas, doc, policy); err != nil {
		t.Fatal(err)
	}
	state := cloudAgentRuntime{}
	if err := emitCloudAgentCanvasChange(s.repo, "run", &state, cloudAgentMutationInput{UserID: "user", CanvasID: canvas.ID, BeforeJSON: before}); err != nil {
		t.Fatal(err)
	}
	if len(state.Events) != 1 || state.Events[0].Payload["requiresRefresh"] == true {
		t.Fatal("complete deletion/reorder delta was replaced with a refresh")
	}
	patch, ok := state.Events[0].Payload["canvasPatch"].(map[string]any)
	if !ok {
		t.Fatal("missing canvas delta")
	}
	for key, id := range map[string]string{"nodes": "b", "connections": "gone-edge"} {
		changes := creationMaps(patch[key])
		if len(changes) != 1 || changes[0]["after"] != nil || stringValue(changes[0]["before"].(map[string]any)["id"]) != id {
			t.Fatalf("missing %s deletion tombstone", key)
		}
	}
	if !reflect.DeepEqual(cloudAgentStrings(patch["nodeOrder"]), []string{"c", "a"}) || !reflect.DeepEqual(cloudAgentStrings(patch["previousNodeOrder"]), []string{"a", "b", "c"}) {
		t.Fatal("node-order delta lost its baseline or final order")
	}
	if !reflect.DeepEqual(cloudAgentStrings(patch["connectionOrder"]), []string{"keep-edge"}) {
		t.Fatal("connection-order delta lost its surviving edge")
	}
	// The wire representation must use JSON null for removed objects.
	raw, err := json.Marshal(patch)
	if err != nil || strings.Count(string(raw), `"after":null`) != 2 {
		t.Fatal("invalid SSE canvas patch")
	}
}
