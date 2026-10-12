package commands

import (
	"encoding/json"
	"infinite-canvas/backend/internal/identity"
	"reflect"
	"testing"
)

func fixture(t *testing.T, raw string) Document {
	t.Helper()
	var d Document
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	return d
}
func ownerContext() ValidationContext {
	return ValidationContext{Principal: identity.Principal{ID: "owner", OwnerID: "owner", Role: "owner", Scopes: identity.OwnerScopes()}}
}
func command(ops ...Operation) Command {
	return Command{Protocol: 1, CanvasID: "canvas", DocumentEpoch: "epoch", OperationID: "op", ClientID: "client", Operations: ops}
}
func TestBatchRejectedAtomically(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"a","type":"text","metadata":{"content":"before","extension":{"kept":true}}}],"connections":[]}`)
	before, _ := json.Marshal(d)
	result, err := Plan(d, command(Operation{Type: "update_node", ID: "a", Patch: map[string]any{"content": "changed"}}, Operation{Type: "delete_node", ID: "missing"}), ownerContext())
	after, _ := json.Marshal(d)
	if err == nil || string(before) != string(after) || !reflect.DeepEqual(result.Document, d) {
		t.Fatalf("batch was not rejected atomically: err=%v before=%s after=%s result=%+v", err, before, after, result)
	}
}
func TestDeleteNodeCleansReferences(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"a","type":"image","metadata":{"batchChildIds":["child"]}},{"id":"child","type":"image","metadata":{"batchRootId":"a"}},{"id":"b","type":"text","parentId":"a","metadata":{"extension":{"kept":true},"referenceNodeIds":["a","other"],"storyboard":{"rows":[{"id":"r","videoNodeId":"child","assetBindings":[{"nodeId":"a"},{"nodeId":"other"}]}]},"drawing":{"assetBindings":[{"nodeId":"a"}]}}}],"connections":[{"id":"e","fromNodeId":"a","toNodeId":"b"}]}`)
	result, err := Plan(d, command(Operation{Type: "delete_node", ID: "a"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result.Document)
	var want Document
	json.Unmarshal([]byte(`{"nodes":[{"id":"b","type":"text","metadata":{"extension":{"kept":true},"referenceNodeIds":["other"],"storyboard":{"rows":[{"id":"r","assetBindings":[{"nodeId":"other"}]}]},"drawing":{"assetBindings":[]}}}],"connections":[]}`), &want)
	actual, _ := json.Marshal(want)
	if string(encoded) != string(actual) {
		t.Fatalf("dangling references: %s", encoded)
	}
	if !reflect.DeepEqual(d["nodes"], fixture(t, `{"nodes":[{"id":"a","type":"image","metadata":{"batchChildIds":["child"]}},{"id":"child","type":"image","metadata":{"batchRootId":"a"}},{"id":"b","type":"text","parentId":"a","metadata":{"extension":{"kept":true},"referenceNodeIds":["a","other"],"storyboard":{"rows":[{"id":"r","videoNodeId":"child","assetBindings":[{"nodeId":"a"},{"nodeId":"other"}]}]},"drawing":{"assetBindings":[{"nodeId":"a"}]}}}]}`)["nodes"]) {
		t.Fatal("planner mutated caller")
	}
}
func TestReadSetIncludesRelatedNodes(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"a","type":"text","metadata":{"content":"hello"}},{"id":"b","type":"text","metadata":{"content":"world"}},{"id":"p","type":"frame","metadata":{}}],"connections":[]}`)
	result, err := Plan(d, command(Operation{Type: "connect_nodes", ID: "edge", FromNodeID: "a", ToNodeID: "b"}, Operation{Type: "set_parent", ID: "b", ParentID: "p"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"node:a", "node:b", "node:p", "connection:edge"} {
		if _, ok := result.ReadSet[key]; !ok {
			t.Errorf("read set missing %s: %v", key, result.ReadSet)
		}
	}
}
func TestInversePreservesUnknownMetadata(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"a","type":"text","metadata":{"content":"before","extension":{"nested":[1,2]}}}],"connections":[]}`)
	result, err := Plan(d, command(Operation{Type: "replace_text", ID: "a", Match: "before", Replacement: "after"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ApplyChanges(result.Document, result.Inverse)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(d)
	after, _ := json.Marshal(restored)
	if string(before) != string(after) {
		t.Fatalf("inverse lost data: %s", after)
	}
}
func TestViewerCannotPlanMutation(t *testing.T) {
	ctx := ownerContext()
	ctx.Principal.Role = "viewer"
	ctx.Principal.Scopes = []string{"canvas.read"}
	if _, err := Plan(fixture(t, `{"nodes":[],"connections":[]}`), command(Operation{Type: "add_node", ID: "a", NodeType: "text"}), ctx); err == nil {
		t.Fatal("viewer wrote document")
	}
}
