package commands

import "testing"

func TestReferenceCopyRemapsOwnedOutputs(t *testing.T) {
	doc := fixture(t, `{"referenceFieldsVersion":1,"nodes":[{"id":"frame","type":"frame","position":{"x":0,"y":0}},{"id":"asset","type":"image","parentId":"frame","metadata":{}},{"id":"board","type":"script","parentId":"frame","metadata":{"storyboard":{"referenceNodeIds":["asset"],"rows":[{"id":"row","imageNodeId":"output","assetBindings":[{"nodeId":"asset","role":"style","priority":60}],"taskId":"old-task","taskStatus":"running","status":"loading"}]}}},{"id":"output","type":"image","parentId":"frame","metadata":{}}],"connections":[]}`)
	result, err := Plan(doc, command(Operation{Type: "duplicate_node", ID: "copy", SourceNodeID: "frame", Action: "copy"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	var board, output map[string]any
	for _, node := range maps(result.Document["nodes"]) {
		m, _ := node["metadata"].(map[string]any)
		switch m["copiedFromNodeId"] {
		case "board":
			board = node
		case "output":
			output = node
		}
	}
	row := maps(board["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"])[0]
	if row["imageNodeId"] != output["id"] || row["taskId"] != nil || row["taskStatus"] != nil || row["id"] == "row" || row["status"] != "idle" {
		t.Fatalf("copy kept ownership: %+v", row)
	}
}
func TestReferenceConcurrentSourceDelete(t *testing.T) {
	doc := fixture(t, `{"referenceFieldsVersion":1,"nodes":[{"id":"asset","type":"image"},{"id":"compare","type":"compare","metadata":{"inputBindings":[{"id":"ref","nodeId":"asset","mediaType":"image","role":"reference","order":0,"resolution":"latest"}]}}],"connections":[]}`)
	result, err := Plan(doc, command(Operation{Type: "delete_node", ID: "asset"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if len(maps(entity(result.Document, "node:compare")["metadata"].(map[string]any)["inputBindings"])) != 0 {
		t.Fatal("deleted source left input binding")
	}
}
