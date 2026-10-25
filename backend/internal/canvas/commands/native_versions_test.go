package commands

import "testing"

func TestAddNodeCarriesCopyProvenance(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[{"id":"source","type":"text","title":"Text","position":{"x":0,"y":0},"metadata":{"content":"answer","prompt":"question resource","composerContent":"question @图片1","model":"gpt","generationMode":"text","status":"success","taskId":"completed-task","extension":{"keep":true}}}],"connections":[]}`)
	content := "answer"
	x, y := 36.0, 36.0
	result, err := Plan(d, command(Operation{Type: "add_node", ID: "copy", NodeType: "text", Content: &content, CopiedFromNodeID: "source", X: &x, Y: &y}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	metadata := entity(result.Document, "node:copy")["metadata"].(map[string]any)
	if metadata["copiedFromNodeId"] != "source" {
		t.Fatalf("copy provenance was not persisted: %#v", metadata)
	}
	if metadata["prompt"] != "question resource" || metadata["composerContent"] != "question @图片1" || metadata["model"] != "gpt" || metadata["generationMode"] != "text" {
		t.Fatalf("copied text generation metadata was not preserved: %#v", metadata)
	}
	if metadata["content"] != "answer" || metadata["status"] != "success" || metadata["taskId"] != nil {
		t.Fatalf("copied text metadata was not isolated: %#v", metadata)
	}
	if extension, ok := metadata["extension"].(map[string]any); !ok || extension["keep"] != true {
		t.Fatalf("copied text extension was not preserved: %#v", metadata)
	}
	if !result.ReadSet["node:source"] {
		t.Fatalf("copy source is absent from the read set: %#v", result.ReadSet)
	}
	changed := "changed after copying"
	if _, err := Plan(d, command(Operation{Type: "add_node", ID: "altered-copy", NodeType: "text", Content: &changed, CopiedFromNodeID: "source"}), ownerContext()); err == nil {
		t.Fatal("text copy provenance must not accept altered content")
	}
}

func TestAddNodeCarriesMediaCopyProvenance(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[{"id":"source","type":"image","title":"Image","position":{"x":0,"y":0},"metadata":{"content":"resource:image","status":"success"}}],"connections":[]}`)
	content := "resource:image"
	ctx := ownerContext()
	ctx.ResourceAllowed = func(id string) bool { return id == "image" }
	result, err := Plan(d, command(Operation{Type: "add_node", ID: "copy", NodeType: "image", Content: &content, CopiedFromNodeID: "source"}), ctx)
	if err != nil {
		t.Fatal(err)
	}
	metadata := entity(result.Document, "node:copy")["metadata"].(map[string]any)
	if metadata["content"] != content || metadata["copiedFromNodeId"] != "source" || metadata["generationResultPlacement"] != "replace-node" {
		t.Fatalf("media copy was not isolated: %#v", metadata)
	}
	if _, err := Plan(d, command(Operation{Type: "add_node", ID: "copy", NodeType: "video", CopiedFromNodeID: "source"}), ownerContext()); err == nil {
		t.Fatal("copy accepted a mismatched source type")
	}
	if _, err := Plan(d, command(Operation{Type: "add_node", ID: "copy", NodeType: "image", CopiedFromNodeID: "source"}), ownerContext()); err == nil {
		t.Fatal("copy accepted an unauthorized resource")
	}
}

func TestAddNodeCopyProvenanceRemapsSelfReferencesPerCopy(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[{"id":"source","type":"text","title":"Text","position":{"x":0,"y":0},"metadata":{"content":"saved","referenceSetId":"source"}}],"connections":[]}`)
	content := "saved"
	result, err := Plan(d, command(
		Operation{Type: "add_node", ID: "copy-a", NodeType: "text", Content: &content, CopiedFromNodeID: "source"},
		Operation{Type: "add_node", ID: "copy-b", NodeType: "text", Content: &content, CopiedFromNodeID: "source"},
	), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"copy-a", "copy-b"} {
		metadata := entity(result.Document, "node:"+id)["metadata"].(map[string]any)
		if metadata["referenceSetId"] != id {
			t.Fatalf("%s self reference was not remapped to its own copy: %#v", id, metadata)
		}
	}
}

func TestAddNodeCopyProvenanceRemapsReferencesAcrossBatch(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[{"id":"source-a","type":"text","title":"A","position":{"x":0,"y":0},"metadata":{"content":"A","referenceSetId":"source-b","pluginNodeId":"source-a","extension":{"peerNodeId":"source-a"},"videoFrameSourceNodeId":"source-b","batchInputNodeIds":["source-a","source-b","external"],"generationSpec":{"referenceBindings":[{"nodeId":"source-b"}]},"inputBindings":[{"nodeId":"source-b"}]}},{"id":"source-b","type":"text","title":"B","position":{"x":100,"y":0},"metadata":{"content":"B"}}],"connections":[]}`)
	contentA, contentB := "A", "B"
	result, err := Plan(d, command(
		Operation{Type: "add_node", ID: "copy-a", NodeType: "text", Content: &contentA, CopiedFromNodeID: "source-a"},
		Operation{Type: "add_node", ID: "copy-b", NodeType: "text", Content: &contentB, CopiedFromNodeID: "source-b"},
	), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	metadata := entity(result.Document, "node:copy-a")["metadata"].(map[string]any)
	if metadata["referenceSetId"] != "copy-b" {
		t.Fatalf("batch reference was not remapped to the copied peer: %#v", metadata)
	}
	if metadata["pluginNodeId"] != "source-a" || metadata["extension"].(map[string]any)["peerNodeId"] != "source-a" {
		t.Fatalf("opaque NodeId extension was rewritten: %#v", metadata)
	}
	if metadata["videoFrameSourceNodeId"] != "copy-b" {
		t.Fatalf("declared frame source was not remapped: %#v", metadata)
	}
	inputIDs := metadata["batchInputNodeIds"].([]any)
	if len(inputIDs) != 3 || inputIDs[0] != "copy-a" || inputIDs[1] != "copy-b" || inputIDs[2] != "external" {
		t.Fatalf("declared batch inputs were not remapped: %#v", metadata)
	}
	for _, field := range []string{"generationSpec", "inputBindings"} {
		container := metadata[field]
		if field == "generationSpec" {
			container = container.(map[string]any)["referenceBindings"]
		}
		if maps(container)[0]["nodeId"] != "copy-b" {
			t.Fatalf("%s binding was not remapped: %#v", field, metadata)
		}
	}
}

func TestAddNodeCopyProvenanceUsesFrozenSourceAcrossSameCommandDelete(t *testing.T) {
	d := fixture(t, `{"id":"canvas","nodes":[{"id":"source","type":"text","title":"Text","position":{"x":0,"y":0},"metadata":{"content":"saved","prompt":"keep"}}],"connections":[]}`)
	content := "saved"
	result, err := Plan(d, command(
		Operation{Type: "delete_node", ID: "source"},
		Operation{Type: "add_node", ID: "copy", NodeType: "text", Content: &content, CopiedFromNodeID: "source"},
	), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if entity(result.Document, "node:source") != nil {
		t.Fatal("source node was not deleted")
	}
	metadata := entity(result.Document, "node:copy")["metadata"].(map[string]any)
	if metadata["content"] != "saved" || metadata["prompt"] != "keep" || metadata["copiedFromNodeId"] != "source" {
		t.Fatalf("copy did not use the command-start source snapshot: %#v", metadata)
	}
}

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
