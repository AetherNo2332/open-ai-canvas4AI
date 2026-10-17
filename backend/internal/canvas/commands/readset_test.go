package commands

import "testing"

func TestTextEditDoesNotReadUnrelatedGraphNode(t *testing.T) {
	d := fixture(t, `{"nodes":[{"id":"a","type":"text","metadata":{"content":"a"}},{"id":"b","type":"text","metadata":{"content":"b"}}],"connections":[{"id":"e","fromNodeId":"a","toNodeId":"b"}]}`)
	result, err := Plan(d, command(Operation{Type: "update_node", ID: "a", Patch: map[string]any{"content": "changed"}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if result.ReadSet["node:b"] || result.ReadSet["connection:e"] {
		t.Fatalf("unrelated edit requires unrelated graph versions: %v", result.ReadSet)
	}
}
