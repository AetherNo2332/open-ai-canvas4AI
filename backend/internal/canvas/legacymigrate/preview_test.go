package legacymigrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func legacyFile(t *testing.T, parts ...string) []byte {
	t.Helper()
	base := []string{"..", "..", "..", "..", "testdata", "shortplay-canvas-port"}
	path := filepath.Join(append(base, parts...)...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return raw
}

func resourceLookup(t *testing.T) func(kind, id string) bool {
	t.Helper()
	var inventory struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
		Assets []struct {
			ID string `json:"id"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(legacyFile(t, "legacy", "resources.json"), &inventory); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, item := range inventory.Resources {
		known["resource\x00"+item.ID] = true
	}
	for _, item := range inventory.Assets {
		known["asset\x00"+item.ID] = true
	}
	return func(kind, id string) bool { return known[kind+"\x00"+id] }
}

func findNode(doc map[string]any, id string) map[string]any {
	for _, node := range list(doc["nodes"]) {
		if text(node["id"]) == id {
			return node
		}
	}
	return nil
}

func containsResource(resources []Resource, kind, id string) bool {
	for _, resource := range resources {
		if resource.Kind == kind && resource.ID == id {
			return true
		}
	}
	return false
}

func hasPath(diffs []Diff, path string) bool {
	for _, diff := range diffs {
		if diff.Path == path || strings.HasPrefix(diff.Path, path+".") || strings.HasPrefix(diff.Path, path+"[") {
			return true
		}
	}
	return false
}

func TestPreviewLegacyDatabasePayload(t *testing.T) {
	raw := legacyFile(t, "legacy", "payload-with-extensions.json")
	var original map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	initial, err := PreviewPayload(string(raw), Options{ResourceExists: resourceLookup(t)})
	if err != nil {
		t.Fatal(err)
	}
	if initial.Source != SourceDatabase || !initial.Required || initial.Ready || len(initial.Conflicts) != 1 {
		t.Fatalf("preview should expose one conflict: source=%s required=%v ready=%v blocking=%+v conflicts=%+v", initial.Source, initial.Required, initial.Ready, initial.Blocking, initial.Conflicts)
	}
	choices := map[string]string{}
	for _, conflict := range initial.Conflicts {
		choices[conflict.ScopeKey] = "fields"
	}
	preview, err := PreviewPayload(string(raw), Options{ResourceExists: resourceLookup(t), Choices: choices})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Blocking) != 0 || len(preview.Conflicts) != 0 || preview.Candidate == nil {
		t.Fatalf("preview should be ready: blocking=%+v conflicts=%+v candidate=%v", preview.Blocking, preview.Conflicts, preview.Candidate != nil)
	}
	if number(preview.Candidate["referenceFieldsVersion"]) != 1 {
		t.Fatalf("candidate missing referenceFieldsVersion: %v", preview.Candidate["referenceFieldsVersion"])
	}
	if len(preview.Identity.Rows) != 4 || len(preview.Identity.Columns) != 3 {
		t.Fatalf("identity rows/columns wrong: %+v", preview.Identity)
	}
	if len(preview.Explicit.RowOutputs) != 3 || len(preview.Explicit.BatchColumns) != 2 || len(preview.Explicit.BatchOutputs) != 1 {
		t.Fatalf("explicit legacy handles wrong: %+v", preview.Explicit)
	}
	if len(preview.Explicit.EmptyCells) != 1 || preview.Explicit.EmptyCells[0].ColumnID != "col-description" || preview.Explicit.EmptyCells[0].RowID != "batch-row-2" {
		t.Fatalf("explicit empty cell lost: %+v", preview.Explicit.EmptyCells)
	}
	for _, want := range []struct{ kind, id string }{{"resource", "frame-1"}, {"resource", "frame-2"}, {"resource", "video-1"}, {"resource", "character-1"}, {"resource", "batch-output"}, {"asset", "asset-character"}, {"asset", "asset-batch-output"}} {
		if !containsResource(preview.Resources, want.kind, want.id) {
			t.Fatalf("resource inventory missing %s/%s: %+v", want.kind, want.id, preview.Resources)
		}
	}
	for _, resource := range preview.Resources {
		if resource.Exists != nil && !*resource.Exists {
			t.Fatalf("resource should resolve: %+v", resource)
		}
	}
	script := findNode(preview.Candidate, "script-1")
	row1 := list(object(object(script["metadata"])["storyboard"])["rows"])[0]
	row2 := list(object(object(script["metadata"])["storyboard"])["rows"])[1]
	if text(row1["imageNodeId"]) != "image-frame-1" || text(row1["videoNodeId"]) != "video-existing" || text(row2["imageNodeId"]) != "image-frame-2" {
		t.Fatalf("storyboard outputs not preserved: row1=%+v row2=%+v", row1, row2)
	}
	table := findNode(preview.Candidate, "batch-table-1")
	batchRow := list(object(object(table["metadata"])["batchTable"])["rows"])[0]
	if text(batchRow["outputNodeId"]) != "batch-output-image" {
		t.Fatalf("batch output field not materialized: %+v", batchRow)
	}
	emptyRow := list(object(object(table["metadata"])["batchTable"])["rows"])[1]
	if cells := object(emptyRow["cells"]); cells["col-description"] != "" {
		t.Fatalf("explicit empty cell changed: %+v", cells)
	}
	video := findNode(preview.Candidate, "video-existing")
	if metadata := object(video["metadata"]); text(metadata["taskId"]) != "task-video-1" || text(metadata["storageKey"]) != "resource:video-1" {
		t.Fatalf("existing video metadata lost: %+v", metadata)
	}
	for _, edge := range list(preview.Candidate["connections"]) {
		if text(edge["id"]) == "conn-batch-output" {
			t.Fatalf("consumed batch-output edge survived: %+v", edge)
		}
	}
	if !hasPath(preview.Diff, "referenceFieldsVersion") || !hasPath(preview.Diff, "connections") {
		t.Fatalf("diff missing reference/version changes: %+v", preview.Diff)
	}
	var after map[string]any
	if err := json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, after) {
		t.Fatal("preview mutated the original payload")
	}
	repeat, err := PreviewPayload(string(raw), Options{ResourceExists: resourceLookup(t), Choices: choices})
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(preview)
	secondJSON, _ := json.Marshal(repeat)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("repeated preview is not deterministic")
	}
}

func TestPreviewZipDrawingAndArchive(t *testing.T) {
	for _, version := range []string{"v3", "v4"} {
		t.Run(version, func(t *testing.T) {
			raw := legacyFile(t, "zip", "projects-"+version+".json")
			preview, err := PreviewZipProjects(raw, "project-import-1", Options{})
			if err != nil {
				t.Fatal(err)
			}
			if preview.Source != SourceZip || !preview.Required || !preview.Ready || preview.Archive == nil {
				t.Fatalf("zip preview state wrong: %+v", preview)
			}
			if preview.Archive.ProjectID != "project-import-1" || len(preview.Archive.Files) != 1 || len(preview.Archive.Drawings) != 1 {
				t.Fatalf("archive inventory wrong: %+v", preview.Archive)
			}
			wantVersion := int64(1)
			if version == "v4" {
				wantVersion = 2
			}
			drawing := preview.Archive.Drawings[0]
			if int64(drawing.Version) != wantVersion || drawing.DrawingID != "drawing:1" || drawing.Engine == "" {
				t.Fatalf("drawing metadata wrong: %+v", drawing)
			}
			if version == "v4" && drawing.GenerationPath == "" {
				t.Fatalf("v4 generation render lost: %+v", drawing)
			}
			if !containsResource(preview.Resources, "storage", "image:frame-1") || !containsResource(preview.Resources, "drawing", "drawing:1") {
				t.Fatalf("archive resources missing: %+v", preview.Resources)
			}
			node := findNode(preview.Candidate, "drawing-1")
			metadata := object(node["metadata"])
			if text(metadata["drawingId"]) != "drawing:1" || text(metadata["drawingEngine"]) != "tldraw" || number(metadata["drawingRevision"]) != 3 {
				t.Fatalf("drawing node metadata lost: %+v", metadata)
			}
			again, err := PreviewZipProjects(raw, "project-import-1", Options{})
			if err != nil {
				t.Fatal(err)
			}
			left, _ := json.Marshal(preview)
			right, _ := json.Marshal(again)
			if string(left) != string(right) {
				t.Fatal("repeated zip preview is not deterministic")
			}
		})
	}
}

func TestPreviewCrossShotFrameAndExplicitEmptyColumn(t *testing.T) {
	doc := map[string]any{
		"id": "cross-shot",
		"nodes": []any{
			map[string]any{"id": "image-1", "type": "image", "metadata": map[string]any{"storageKey": "resource:frame-1"}},
			map[string]any{"id": "image-2", "type": "image", "metadata": map[string]any{"storageKey": "resource:frame-2"}},
			map[string]any{"id": "video-2", "type": "video", "metadata": map[string]any{
				"status": "idle", "videoStartFrameNodeId": "image-1", "referenceNodeIds": []any{"image-1"},
			}},
			map[string]any{"id": "script", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{
				"rows": []any{
					map[string]any{"id": "row-1", "imageNodeId": "image-1"},
					map[string]any{"id": "row-2", "imageNodeId": "image-2", "videoNodeId": "video-2"},
				},
				"referenceNodeIds": []any{},
			}}},
			map[string]any{"id": "batch", "type": "batch-table", "metadata": map[string]any{"batchTable": map[string]any{
				"textColumns": []any{map[string]any{"id": "col-description", "type": "text"}},
				"rows": []any{
					map[string]any{"id": "batch-row-1", "cells": map[string]any{"col-description": ""}},
				},
			}}},
			map[string]any{"id": "batch-output", "type": "image", "metadata": map[string]any{"status": "success"}},
			map[string]any{"id": "text-1", "type": "text", "metadata": map[string]any{"content": "提示"}},
		},
		"connections": []any{
			map[string]any{"id": "e-row-1-image", "fromNodeId": "script", "fromHandleId": "row:row-1", "toNodeId": "image-1"},
			map[string]any{"id": "e-row-2-image", "fromNodeId": "script", "fromHandleId": "row:row-2", "toNodeId": "image-2"},
			map[string]any{"id": "e-row-2-video", "fromNodeId": "script", "fromHandleId": "row:row-2", "toNodeId": "video-2"},
			map[string]any{"id": "e-batch-output", "fromNodeId": "batch", "toNodeId": "batch-output", "relation": "batch-output", "storyboardRowId": "batch-row-1"},
			map[string]any{"id": "e-batch-text", "fromNodeId": "text-1", "toNodeId": "batch", "toHandleId": "batch-text:col-description"},
		},
	}
	preview, err := PreviewDocument(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Ready || len(preview.Blocking) != 0 {
		t.Fatalf("cross-shot preview not ready: blocking=%+v", preview.Blocking)
	}
	foundFrame := false
	for _, frame := range preview.Explicit.Frames {
		if frame.NodeID == "video-2" && frame.Kind == "first-frame" && frame.RefID == "image-1" {
			foundFrame = true
		}
	}
	if !foundFrame {
		t.Fatalf("cross-shot first frame not recorded: %+v", preview.Explicit.Frames)
	}
	video := findNode(preview.Candidate, "video-2")
	if text(object(video["metadata"])["videoStartFrameNodeId"]) != "image-1" {
		t.Fatalf("cross-shot first frame field lost: %+v", video["metadata"])
	}
	table := findNode(preview.Candidate, "batch")
	row := list(object(object(table["metadata"])["batchTable"])["rows"])[0]
	if object(row["cells"])["col-description"] != "" {
		t.Fatalf("explicit empty column changed: %+v", row)
	}
	if text(row["outputNodeId"]) != "batch-output" {
		t.Fatalf("batch output not materialized: %+v", row)
	}
	if len(preview.Explicit.EmptyCells) != 1 || preview.Explicit.EmptyCells[0].ColumnID != "col-description" {
		t.Fatalf("empty cell not recorded: %+v", preview.Explicit.EmptyCells)
	}
}

func TestPreviewConflictRequiresChoice(t *testing.T) {
	doc := map[string]any{
		"nodes": []any{
			map[string]any{"id": "image-1", "type": "image", "metadata": map[string]any{"storageKey": "resource:frame-1"}},
			map[string]any{"id": "image-2", "type": "image", "metadata": map[string]any{"storageKey": "resource:frame-2"}},
			map[string]any{"id": "video-1", "type": "video", "metadata": map[string]any{
				"referenceNodeIds": []any{"image-1"},
				"generationSpec": map[string]any{
					"version": 1, "mode": "video", "prompt": "p", "options": map[string]any{}, "textInputMode": "prompt-only",
					"referenceBindings": []any{map[string]any{"id": "node:image-1:reference", "nodeId": "image-1", "mediaType": "image", "role": "reference", "order": 0, "resolution": "latest"}},
				},
			}},
		},
		"connections": []any{
			map[string]any{"id": "e-1", "fromNodeId": "image-2", "toNodeId": "video-1"},
		},
	}
	preview, err := PreviewDocument(doc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Ready || len(preview.Conflicts) == 0 || preview.Candidate != nil {
		t.Fatalf("conflict should block candidate: ready=%v conflicts=%+v candidate=%v", preview.Ready, preview.Conflicts, preview.Candidate != nil)
	}
	choice := preview.Conflicts[0].ScopeKey
	resolved, err := PreviewDocument(doc, Options{Choices: map[string]string{choice: "fields"}})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Ready || resolved.Candidate == nil || len(resolved.Conflicts) != 0 {
		t.Fatalf("explicit fields choice should resolve: ready=%v conflicts=%+v", resolved.Ready, resolved.Conflicts)
	}
}

func TestApplyBatchOutputFieldsConsumesEdge(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(legacyFile(t, "legacy", "payload-with-extensions.json"), &doc); err != nil {
		t.Fatal(err)
	}
	consumed, warnings := applyBatchOutputFields(doc)
	if len(warnings) != 0 {
		t.Fatalf("unexpected batch output warnings: %+v", warnings)
	}
	if len(consumed) != 1 || consumed[0] != "conn-batch-output" {
		t.Fatalf("batch output edge not consumed: %+v", consumed)
	}
	table := findNode(doc, "batch-table-1")
	row := list(object(object(table["metadata"])["batchTable"])["rows"])[0]
	if text(row["outputNodeId"]) != "batch-output-image" {
		t.Fatalf("batch output not written: %+v", row)
	}
}
