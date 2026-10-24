package commands

import (
	"encoding/json"
	"testing"
)

func TestManualSizeResizePlansGeometryAndIntentTogether(t *testing.T) {
	document := fixture(t, `{"nodes":[{"id":"a","type":"text","width":340,"height":240,"metadata":{"content":"retain text"}}],"connections":[]}`)
	for _, manual := range []bool{true, false} {
		plan, err := Plan(document, command(Operation{Type: "update_node", ID: "a", Patch: map[string]any{"width": 500.0, "height": 320.0, "manualSize": manual}}), ownerContext())
		if err != nil {
			t.Fatal(err)
		}
		node := entity(plan.Document, "node:a")
		metadata := node["metadata"].(map[string]any)
		if node["width"] != 500.0 || node["height"] != 320.0 || metadata["manualSize"] != manual || metadata["content"] != "retain text" {
			t.Fatalf("resize lost geometry, intent or text: %#v", node)
		}
		if len(plan.Changes.Entities) != 1 || plan.Changes.Entities[0].Key != "node:a" {
			t.Fatalf("resize delta does not identify the changed node: %+v", plan.Changes)
		}
		document = plan.Document
	}
}

func TestManualSizeInvalidFlagRejectsWholeResize(t *testing.T) {
	document := fixture(t, `{"nodes":[{"id":"a","type":"text","width":340,"height":240,"metadata":{"manualSize":false,"content":"retain text"}}],"connections":[]}`)
	before, _ := json.Marshal(document)
	for _, invalid := range []any{nil, "true", 1.0, []any{}, map[string]any{}} {
		if _, err := Plan(document, command(Operation{Type: "update_node", ID: "a", Patch: map[string]any{"width": 500.0, "height": 320.0, "manualSize": invalid}}), ownerContext()); err == nil {
			t.Fatalf("invalid resize accepted: %#v", invalid)
		}
		after, _ := json.Marshal(document)
		if string(before) != string(after) {
			t.Fatal("rejected resize changed the caller document")
		}
	}
}
