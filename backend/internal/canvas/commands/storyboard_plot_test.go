package commands

import "testing"

func TestStoryboardPlotCreatedAndEditedThroughCommands(t *testing.T) {
	doc := fixture(t, `{"nodes":[],"connections":[]}`)
	plot := "雨夜，她推门。"
	result, err := Plan(doc, command(Operation{Type: "add_node", ID: "board", NodeType: "script", Content: &plot}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if entity(result.Document, "node:board")["metadata"].(map[string]any)["composerContent"] != plot {
		t.Fatal("command discarded script plot")
	}
	result, err = Plan(result.Document, command(Operation{Type: "update_node", ID: "board", Patch: map[string]any{"composerContent": "她读信。"}}, Operation{Type: "replace_text", ID: "board", Match: "读信", Replacement: "回头"}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if entity(result.Document, "node:board")["metadata"].(map[string]any)["composerContent"] != "她回头。" {
		t.Fatal("command edits lost plot")
	}
}
