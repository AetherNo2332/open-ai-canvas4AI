package commands

import "testing"

func TestEditorImageToolFieldsAndReferenceValidation(t *testing.T) {
	doc := fixture(t, `{"nodes":[{"id":"source","type":"image"},{"id":"target","type":"image"}],"connections":[]}`)
	patch := map[string]any{"imageToolPlugin": "image-tools", "imageToolKind": "layer-decomposition", "imageToolData": map[string]any{"sourceNodeId": "source", "layerIndex": 1.0}}
	result, err := Plan(doc, command(Operation{Type: "update_node", ID: "target", Patch: patch}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if !result.ReadSet["node:source"] {
		t.Fatal("image tool source not guarded")
	}
	patch["imageToolData"] = map[string]any{"sourceNodeId": "missing", "layerIndex": 1.0}
	if _, err := Plan(doc, command(Operation{Type: "update_node", ID: "target", Patch: patch}), ownerContext()); err == nil {
		t.Fatal("missing tool source accepted")
	}
	patch["imageToolPlugin"] = "untrusted-plugin"
	if _, err := Plan(doc, command(Operation{Type: "update_node", ID: "target", Patch: patch}), ownerContext()); err == nil {
		t.Fatal("arbitrary plugin identity accepted")
	}
}

func TestEditorEmotionRegionsAreValidatedAndSaved(t *testing.T) {
	doc := fixture(t, `{"nodes":[{"id":"source","type":"image"},{"id":"target","type":"image"}],"connections":[]}`)
	region := map[string]any{"x": 0.0, "y": 0.0, "width": 20.0, "height": 20.0}
	emotion := map[string]any{"sourceNodeId": "source", "characterName": "Hero", "presetId": "happy", "intimacy": 1.0, "arousal": 1.0, "label": "Happy", "faceBox": map[string]any{"id": "face", "x": 1.0, "y": 1.0, "width": 20.0, "height": 20.0, "source": "manual"}, "editRegion": region, "sourceWidth": 400.0, "sourceHeight": 300.0, "providerSize": "1024x1024", "editMode": "provider-mask"}
	if _, err := Plan(doc, command(Operation{Type: "update_node", ID: "target", Patch: map[string]any{"emotionEdit": emotion}}), ownerContext()); err != nil {
		t.Fatal(err)
	}
	region["width"] = -1.0
	if _, err := Plan(doc, command(Operation{Type: "update_node", ID: "target", Patch: map[string]any{"emotionEdit": emotion}}), ownerContext()); err == nil {
		t.Fatal("invalid edit region accepted")
	}
}

func TestEditorImageBatchGroupingAndPrimarySelection(t *testing.T) {
	doc := fixture(t, `{"nodes":[{"id":"root","type":"image","width":100,"height":100,"metadata":{}},{"id":"child","type":"image","width":400,"height":300,"metadata":{"content":"/api/resources/output/file","storageKey":"resource:output","bytes":123,"mimeType":"image/png","status":"success"}}],"connections":[]}`)
	ctx := ownerContext()
	ctx.ResourceAllowed = func(id string) bool { return id == "output" }
	result, err := Plan(doc, command(Operation{Type: "edit_image_batch", ID: "root", Action: "set_children", NodeIDs: []string{"child"}}, Operation{Type: "edit_image_batch", ID: "root", Action: "select_primary", SourceNodeID: "child"}), ctx)
	if err != nil {
		t.Fatal(err)
	}
	root := entity(result.Document, "node:root")
	metadata := root["metadata"].(map[string]any)
	if metadata["primaryImageId"] != "child" || metadata["storageKey"] != "resource:output" || root["width"] != 400.0 || !result.ReadSet["node:child"] {
		t.Fatalf("primary not selected safely: %#v", root)
	}
	for _, op := range []Operation{{Type: "edit_image_batch", ID: "root", Action: "set_children", NodeIDs: []string{"root"}}, {Type: "edit_image_batch", ID: "root", Action: "select_primary", SourceNodeID: "missing"}} {
		if _, err := Plan(result.Document, command(op), ctx); err == nil {
			t.Fatal("unsafe batch operation accepted")
		}
	}
}
