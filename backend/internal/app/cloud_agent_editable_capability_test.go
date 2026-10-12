package app

import (
	"fmt"
	"testing"
)

func TestCloudAgentStructuredEditableRows(t *testing.T) {
	row := map[string]any{"durationSeconds": 5.0, "plotDescription": "Editorial shot", "mustHave": []any{"costume"}, "optionalDetails": []any{"smoke"}, "characters": []any{map[string]any{"characterName": "Alice", "characterImageNodeId": "image-1"}}, "assetBindings": []any{map[string]any{"nodeId": "image-1", "role": "character", "priority": 1.0}}, "imagePromptTemplateVariables": map[string]any{"subject": "Alice"}}
	if err := validateCloudAgentStoryboardRow(row, true); err != nil {
		t.Fatal(err)
	}
	properties := cloudAgentStoryboardPatchSchema()["properties"].(map[string]any)
	if properties["mustHave"] == nil || properties["characters"] == nil {
		t.Fatal("structured storyboard fields absent from schema")
	}
	doc := map[string]any{"nodes": []any{map[string]any{"id": "image-1", "type": "image"}, map[string]any{"id": "text-1", "type": "text"}}, "connections": []any{}}
	patch, _, err := validateCloudAgentBatchRowPatch(doc, map[string]any{"textNodeIds": []any{"text-1"}, "cells": map[string]any{"col-1": "Narrative"}}, 3)
	if err != nil || patch["cells"] == nil {
		t.Fatal("batch editable fields rejected", err)
	}
	if _, _, err := validateCloudAgentBatchRowPatch(doc, map[string]any{"textNodeIds": []any{"image-1"}}, 3); err == nil {
		t.Fatal("image accepted as text source")
	}
	if _, _, err := validateCloudAgentBatchRowPatch(doc, map[string]any{"cells": map[string]any{"col-1": map[string]any{"taskId": "secret"}}}, 3); err == nil {
		t.Fatal("opaque batch cell accepted")
	}
}

func TestCloudAgentPreciseEditableProjectionPrunesServiceFields(t *testing.T) {
	doc := map[string]any{"nodes": []any{map[string]any{"id": "p1", "type": "panorama", "title": "Panorama", "metadata": map[string]any{"panoramaConfig": map[string]any{"projection": "spherical", "sourceMode": "image", "smartBase": true, "directImageUrl": "https://secret.invalid"}}}}, "connections": []any{}}
	view, err := cloudAgentCanvasState(nil, "user", "canvas", doc, 0, []string{"p1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	node := view.(map[string]any)["nodes"].([]any)[0].(map[string]any)
	fields := node["editable"].(map[string]any)["fields"].(map[string]any)
	config := fields["panoramaConfig"].(map[string]any)
	if config["projection"] != "spherical" || config["directImageUrl"] != nil {
		t.Fatalf("unsafe editable read: %#v", fields)
	}
}

func TestCloudAgentStructuredReadIncludesEditableCollections(t *testing.T) {
	row := map[string]any{"id": "row-1", "shotNumber": 1.0, "durationSeconds": 5.0, "plotDescription": "Shot", "mustHave": []any{"costume"}, "optionalDetails": []any{"smoke"}, "characters": []any{map[string]any{"characterName": "Alice", "characterDescription": "Hero"}}, "imagePromptTemplateVariables": map[string]any{"subject": "Alice"}, "sourceStartMs": 0.0, "sourceEndMs": 1000.0}
	view := cloudAgentStoryboardState(map[string]any{"rows": []any{row}}, 0, cloudAgentProjectionDetail)
	read := view["rows"].([]any)[0].(map[string]any)
	if read["mustHave"] == nil || read["imagePromptTemplateVariables"] == nil || read["sourceEndMs"] != 1000.0 {
		t.Fatalf("editorial fields missing from precise read: %#v", read)
	}
	if read["characters"].([]any)[0].(map[string]any)["characterDescription"] != "Hero" {
		t.Fatal("character description absent")
	}
	columns := []any{}
	ids := []any{}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("c-%d", i)
		columns = append(columns, map[string]any{"id": id, "label": id})
		ids = append(ids, fmt.Sprintf("image-%d", i))
	}
	table := map[string]any{"operation": "creative", "concurrency": 1.0, "referenceColumns": columns, "textColumns": []any{map[string]any{"id": "text-col", "label": "Text"}}, "rows": []any{map[string]any{"id": "b1", "enabled": true, "prompt": "A", "inputNodeIds": ids, "textNodeIds": []any{"text-1"}, "cells": map[string]any{"text-col": "Data"}}}}
	batch := cloudAgentBatchTableState(table, 0, cloudAgentProjectionDetail)
	batchRow := batch["rows"].([]any)[0].(map[string]any)
	if len(batch["referenceColumns"].([]any)) != 10 || len(batchRow["inputNodeIds"].([]any)) != 10 || batchRow["textNodeIds"] == nil || batchRow["cells"] == nil || batch["textColumns"] == nil {
		t.Fatalf("batch precision incomplete: %#v", batch)
	}
}

func TestCloudAgentStructuredRowReferenceSemantics(t *testing.T) {
	doc := map[string]any{"nodes": []any{map[string]any{"id": "image-1", "type": "image"}, map[string]any{"id": "audio-1", "type": "audio"}, map[string]any{"id": "text-1", "type": "text"}}}
	for _, patch := range []map[string]any{{"characters": []any{map[string]any{"characterName": "Alice", "characterImageNodeId": "text-1"}}}, {"assetBindings": []any{map[string]any{"nodeId": "audio-1", "role": "environment", "priority": 1.0}}}, {"assetBindings": []any{map[string]any{"nodeId": "missing", "role": "character", "priority": 1.0}}}, {"sourceStartMs": 100.0, "sourceEndMs": 10.0}} {
		if err := validateCloudAgentStoryboardReferences(doc, patch); err == nil {
			t.Fatalf("invalid reference accepted: %#v", patch)
		}
	}
	if err := validateCloudAgentStoryboardReferences(doc, map[string]any{"assetBindings": []any{map[string]any{"nodeId": "image-1", "role": "character", "priority": 1.0}}}); err != nil {
		t.Fatal(err)
	}
	table := map[string]any{"textColumns": []any{map[string]any{"id": "col-1", "label": "Text"}}}
	if _, _, err := validateCloudAgentBatchRowPatch(doc, map[string]any{"cells": map[string]any{"col-1": "Text"}}, 3, table); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateCloudAgentBatchRowPatch(doc, map[string]any{"cells": map[string]any{"unknown": "Text"}}, 3, table); err == nil {
		t.Fatal("unknown cell column accepted")
	}
}
