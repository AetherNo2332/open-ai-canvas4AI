package commands

import "testing"

func TestEditorFeatureRelationships(t *testing.T) {
	doc := fixture(t, `{"id":"canvas","nodes":[{"id":"image","type":"image","metadata":{}},{"id":"video","type":"video","metadata":{}},{"id":"frame","type":"frame","metadata":{}},{"id":"batch","type":"batch-table","metadata":{"batchTable":{"operation":"creative","concurrency":5,"referenceColumns":[{"id":"ref","label":"Reference"}],"rows":[{"id":"row","enabled":true,"prompt":"Create","inputNodeIds":["image"]}]}}}],"connections":[],"referenceFieldsVersion":1}`)
	for _, op := range []Operation{
		{Type: "update_node", ID: "image", Patch: map[string]any{"workflowKind": "shot", "workflowTitle": "镜头 1", "shotIndex": 1.0, "actionBoardRows": 4.0, "actionBoardColumns": 3.0}},
		{Type: "update_node", ID: "video", Patch: map[string]any{"videoEditOperation": "image_to_video", "videoStartFrameNodeId": "image"}},
		{Type: "update_node", ID: "image", Patch: map[string]any{"referenceSetId": "frame"}},
	} {
		if _, err := Plan(doc, command(op), ownerContext()); err != nil {
			t.Fatalf("feature %v: %v", op.Patch, err)
		}
	}
	for _, patch := range []map[string]any{
		{"workflowKind": "character"}, {"videoStartFrameNodeId": "missing"}, {"videoStartFrameNodeId": "video"},
		{"batchSourceNodeId": "image"}, {"batchSourceNodeId": "batch", "batchRowId": "missing"}, {"referenceSetId": "missing"},
	} {
		if _, err := Plan(doc, command(Operation{Type: "update_node", ID: "video", Patch: patch}), ownerContext()); err == nil {
			t.Fatalf("unsafe relationship accepted: %v", patch)
		}
	}
}

func TestEditorClipboardCopiesEveryBuiltinNode(t *testing.T) {
	for _, kind := range []string{"image", "text", "drawing", "script", "skill", "config", "video", "audio", "frame", "markdown", "svg", "html", "panorama", "compare", "chart", "colorgrade", "media-conversion", "batch-table"} {
		t.Run(kind, func(t *testing.T) {
			doc := fixture(t, `{"nodes":[{"id":"source","type":"`+kind+`","metadata":{}}],"connections":[]}`)
			result, err := Plan(doc, command(Operation{Type: "add_node", ID: "copy", NodeType: kind, CopiedFromNodeID: "source"}), ownerContext())
			if err != nil {
				t.Fatal(err)
			}
			if entity(result.Document, "node:copy")["type"] != kind {
				t.Fatal("copy type changed")
			}
		})
	}
}

func TestEditorFeatureBindingsRejectForgedIdentities(t *testing.T) {
	doc := fixture(t, `{"nodes":[{"id":"source","type":"image","metadata":{}},{"id":"other","type":"image","metadata":{}},{"id":"output","type":"image","metadata":{}},{"id":"conversion","type":"media-conversion","metadata":{}},{"id":"batch","type":"batch-table","metadata":{"batchTable":{"operation":"creative","concurrency":5,"referenceColumns":[{"id":"r","label":"R"}],"rows":[{"id":"row","enabled":true,"prompt":"Create","inputNodeIds":["source"]}]}}}],"connections":[]}`)
	valid := Operation{Type: "update_node", ID: "output", Patch: map[string]any{"batchSourceNodeId": "batch", "batchRowId": "row", "batchOperation": "creative", "batchInputNodeIds": []any{"source"}}}
	result, err := Plan(doc, command(valid), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if !result.ReadSet["node:batch"] || !result.ReadSet["node:source"] {
		t.Fatal("feature source is missing from concurrency read set")
	}
	for _, patch := range []map[string]any{{"batchInputNodeIds": []any{"missing"}}, {"batchRowId": "row"}, {"batchSourceNodeId": "batch", "batchRowId": "row", "batchOperation": "creative", "batchInputNodeIds": []any{"other"}}} {
		if _, err := Plan(doc, command(Operation{Type: "update_node", ID: "output", Patch: patch}), ownerContext()); err == nil {
			t.Fatalf("forged origin accepted: %v", patch)
		}
	}
	for _, state := range []map[string]any{
		{"schemaVersion": 1.0, "operation": "grayscale", "status": "completed"},
		{"schemaVersion": 1.0, "operation": "grayscale", "status": "completed", "resultStorageKey": "resource:foreign", "outputKind": "image"},
		{"schemaVersion": 1.0, "operation": "grayscale", "status": "processing", "sourceNodeId": "missing"},
	} {
		if _, err := Plan(doc, command(Operation{Type: "update_node", ID: "conversion", Patch: map[string]any{"conversionState": state}}), ownerContext()); err == nil {
			t.Fatal("forged conversion accepted", state)
		}
	}
}
