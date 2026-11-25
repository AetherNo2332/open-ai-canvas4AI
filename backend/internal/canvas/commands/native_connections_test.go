package commands

import (
	"encoding/json"
	"testing"
)

func nativeConnectionOp(t *testing.T, raw string) Operation {
	t.Helper()
	var op Operation
	if err := json.Unmarshal([]byte(raw), &op); err != nil {
		t.Fatal(err)
	}
	return op
}
func TestNativeConnectionAnchorsAndExtensions(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"a","type":"text"},{"id":"b","type":"text"}],"connections":[{"id":"edge","fromNodeId":"a","toNodeId":"b","extension":{"keep":true}}]}`)
	op := nativeConnectionOp(t, `{"type":"update_connection","id":"edge","fromNodeId":"a","toNodeId":"b","fromAnchorRatio":0.25,"toAnchorRatio":0.75}`)
	r, err := Plan(d, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	edge := entity(r.Document, "connection:edge")
	if edge["fromAnchorRatio"] != 0.25 || edge["toAnchorRatio"] != 0.75 || edge["extension"] == nil {
		t.Fatalf("native connection lost: %v", edge)
	}
	for _, ratio := range []string{"-0.1", "1.1"} {
		bad := nativeConnectionOp(t, `{"type":"update_connection","id":"edge","fromNodeId":"a","toNodeId":"b","fromAnchorRatio":`+ratio+`}`)
		if _, err := Plan(d, command(bad), ownerContext()); err == nil {
			t.Fatal("invalid anchor accepted")
		}
	}
}
func TestNativeBatchOutputConnectionBindsAndDetachesRow(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"batch","type":"batch-table","metadata":{"batchTable":{"rows":[{"id":"row","prompt":"test"}]}}},{"id":"image","type":"image"}],"connections":[]}`)
	op := nativeConnectionOp(t, `{"type":"connect_nodes","id":"edge","fromNodeId":"batch","toNodeId":"image","relation":"batch-output","storyboardRowId":"row"}`)
	r, err := Plan(d, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	rows := maps(entity(r.Document, "node:batch")["metadata"].(map[string]any)["batchTable"].(map[string]any)["rows"])
	if rows[0]["outputNodeId"] != "image" {
		t.Fatalf("row not bound: %v", rows)
	}
	if !r.ReadSet["node:batch"] || !r.ReadSet["node:image"] {
		t.Fatal("binding reads missing")
	}
	next, err := Plan(r.Document, command(Operation{Type: "delete_connection", ID: "edge"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	rows = maps(entity(next.Document, "node:batch")["metadata"].(map[string]any)["batchTable"].(map[string]any)["rows"])
	if rows[0]["outputNodeId"] != nil {
		t.Fatal("deleted binding survived")
	}
	bad := nativeConnectionOp(t, `{"type":"connect_nodes","id":"edge","fromNodeId":"batch","toNodeId":"image","relation":"batch-output","storyboardRowId":"missing"}`)
	if _, err := Plan(d, command(bad), ownerContext()); err == nil {
		t.Fatal("missing row accepted")
	}
}
func TestNativeStoryboardAssetReferenceRequiresAssociatedOutput(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"board","type":"script","metadata":{"storyboard":{"rows":[{"id":"row","plotDescription":"test"}]}}},{"id":"asset","type":"image"},{"id":"output","type":"image"},{"id":"unrelated","type":"image"}],"connections":[]}`)
	output := nativeConnectionOp(t, `{"type":"connect_nodes","id":"out","fromNodeId":"board","toNodeId":"output","fromHandleId":"row:row","relation":"storyboard-output","storyboardRowId":"row"}`)
	ref := nativeConnectionOp(t, `{"type":"connect_nodes","id":"ref","fromNodeId":"asset","toNodeId":"output","relation":"storyboard-asset-reference","storyboardRowId":"row"}`)
	r, err := Plan(d, command(output, ref), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if entity(r.Document, "connection:out")["relation"] != "storyboard-output" || entity(r.Document, "connection:ref")["storyboardRowId"] != "row" {
		t.Fatal("relation lost")
	}
	ref.ToNodeID = "unrelated"
	if _, err := Plan(r.Document, command(ref), ownerContext()); err == nil {
		t.Fatal("unrelated output accepted")
	}
}
func TestNativeStoryboardAssetReferenceWritesRowBindings(t *testing.T) {
	d := fixture(t, `{"referenceFieldsVersion":1,"nodes":[{"id":"board","type":"script","metadata":{"storyboard":{"rows":[{"id":"row","plotDescription":"test"}]}}},{"id":"asset","type":"image"},{"id":"output","type":"image"}],"connections":[]}`)
	output := nativeConnectionOp(t, `{"type":"connect_nodes","id":"out","fromNodeId":"board","toNodeId":"output","fromHandleId":"row:row","relation":"storyboard-output","storyboardRowId":"row"}`)
	ref := nativeConnectionOp(t, `{"type":"connect_nodes","id":"ref","fromNodeId":"asset","toNodeId":"output","relation":"storyboard-asset-reference","storyboardRowId":"row"}`)
	r, err := Plan(d, command(output, ref), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	bindings := maps(entity(r.Document, "node:board")["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"].([]any)[0].(map[string]any)["assetBindings"])
	if len(bindings) != 1 || bindings[0]["nodeId"] != "asset" || bindings[0]["role"] != "style" || bindings[0]["priority"] != float64(60) {
		t.Fatalf("storyboard asset binding not written: %v", bindings)
	}
	next, err := Plan(r.Document, command(Operation{Type: "delete_connection", ID: "ref"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	row := entity(next.Document, "node:board")["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"].([]any)[0].(map[string]any)
	if len(maps(row["assetBindings"])) != 0 || row["imageNodeId"] != "output" {
		t.Fatalf("deleting asset edge did not preserve output binding: %+v", row)
	}
}
func TestNativeBatchTextColumnConnection(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"text","type":"text"},{"id":"table","type":"batch-table","metadata":{"batchTable":{"textColumns":[{"id":"words","label":"说明"}],"rows":[]}}}],"connections":[]}`)
	op := nativeConnectionOp(t, `{"type":"connect_nodes","id":"edge","fromNodeId":"text","toNodeId":"table","toHandleId":"batch-text:words","relation":"batch-input"}`)
	if _, err := Plan(d, command(op), ownerContext()); err != nil {
		t.Fatal(err)
	}
	op.ToHandleID = "batch-text:missing"
	if _, err := Plan(d, command(op), ownerContext()); err == nil {
		t.Fatal("missing text column accepted")
	}
	op.ToHandleID = "batch-reference:words"
	if _, err := Plan(d, command(op), ownerContext()); err == nil {
		t.Fatal("text bound as image reference")
	}
}
func TestNativeBatchTextColumnWritesRowInputs(t *testing.T) {
	d := fixture(t, `{"referenceFieldsVersion":1,"nodes":[{"id":"text-1","type":"text"},{"id":"text-2","type":"text"},{"id":"text-3","type":"text"},{"id":"table","type":"batch-table","metadata":{"batchTable":{"operation":"creative","concurrency":1,"referenceColumns":[{"id":"ref","label":"参考图"}],"textColumns":[{"id":"words","label":"说明"},{"id":"mood","label":"情绪"}],"rows":[{"id":"row-1","enabled":true,"inputNodeIds":[""],"textNodeIds":["text-3",""]},{"id":"row-2","enabled":true,"inputNodeIds":[""],"textNodeIds":[]}]}}}],"connections":[]}`)
	op := nativeConnectionOp(t, `{"type":"connect_nodes","id":"edge","fromNodeId":"text-1","toNodeId":"table","toHandleId":"batch-text:words","relation":"batch-input"}`)
	check := func(doc map[string]any, want [][]string) {
		t.Helper()
		table := entity(doc, "node:table")["metadata"].(map[string]any)["batchTable"].(map[string]any)
		rows := maps(table["rows"])
		for index, row := range rows {
			got := make([]string, len(row["textNodeIds"].([]any)))
			for i, value := range row["textNodeIds"].([]any) {
				got[i], _ = value.(string)
			}
			if len(got) != len(want[index]) {
				t.Fatalf("row %d textNodeIds = %v, want %v", index, got, want[index])
			}
			for i := range got {
				if got[i] != want[index][i] {
					t.Fatalf("row %d textNodeIds = %v, want %v", index, got, want[index])
				}
			}
		}
	}
	r, err := Plan(d, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	check(r.Document, [][]string{{"text-1", ""}, {"text-1", ""}})
	op.Type, op.FromNodeID = "update_connection", "text-2"
	r, err = Plan(r.Document, command(op), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	check(r.Document, [][]string{{"text-2", ""}, {"text-2", ""}})
	r, err = Plan(r.Document, command(Operation{Type: "delete_connection", ID: "edge"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	check(r.Document, [][]string{{"", ""}, {"", ""}})
}
