package commands

import "testing"

func TestNativeVariantCopyAndPrimarySelection(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[{"id":"source","type":"text","title":"Text","position":{"x":0,"y":0},"metadata":{"content":"saved","taskId":"running","taskStatus":"running","extension":"retain"}}],"connections":[]}`)
	result, err := Plan(d, command(Operation{Type: "duplicate_node", ID: "variant", SourceNodeID: "source", Action: "variant"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	source := entity(result.Document, "node:source")
	copy := entity(result.Document, "node:variant")
	m := copy["metadata"].(map[string]any)
	if m["versionLabel"] != "B" || m["versionOfNodeId"] != "source" || m["extension"] != "retain" || m["taskId"] != nil {
		t.Fatal("variant identity not isolated", copy)
	}
	if source["metadata"].(map[string]any)["versionPrimary"] != true {
		t.Fatal("source primary not initialized")
	}
	next, err := Plan(result.Document, command(Operation{Type: "select_node_version", ID: "variant"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if entity(next.Document, "node:source")["metadata"].(map[string]any)["versionPrimary"] != false || entity(next.Document, "node:variant")["metadata"].(map[string]any)["versionPrimary"] != true {
		t.Fatal("multiple primary versions")
	}
	if !next.ReadSet["node:source"] || !next.ReadSet["node:variant"] {
		t.Fatal("missing family read versions")
	}
}
