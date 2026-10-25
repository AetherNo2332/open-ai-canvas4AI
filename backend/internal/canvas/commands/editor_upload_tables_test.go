package commands

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestEditorCapabilitiesFixture(t *testing.T) {
	capabilities := Capabilities()
	nodes := []any{}
	for _, raw := range capabilities["nodes"].([]any) {
		node := raw.(map[string]any)
		fields := map[string]any{}
		for name, raw := range node["patchFields"].(map[string]any) {
			fields[name] = map[string]any{"path": raw.(map[string]any)["path"]}
		}
		entry := map[string]any{"type": node["type"], "patchFields": fields}
		for _, name := range []string{"baseType", "workflowKind"} {
			if value, ok := node[name]; ok {
				entry[name] = value
			}
		}
		nodes = append(nodes, entry)
	}
	structured := map[string]any{}
	for kind, raw := range capabilities["structuredFields"].(map[string]any) {
		fields := map[string]any{}
		for name := range raw.(map[string]any) {
			fields[name] = map[string]any{}
		}
		structured[kind] = fields
	}
	want := map[string]any{"protocolVersion": 1, "nodes": nodes, "structuredFields": structured}
	path := "../../../../web/test/fixtures/canvas-editor-capabilities.json"
	encoded, _ := json.MarshalIndent(want, "", "    ")
	if os.Getenv("UPDATE_EDITOR_CAPABILITIES") == "1" {
		if err := os.WriteFile(path, append(encoded, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got, normalized map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(encoded, &normalized)
	if !reflect.DeepEqual(got, normalized) {
		t.Fatal("editor fixture differs from host capabilities; regenerate with UPDATE_EDITOR_CAPABILITIES=1")
	}
}

func TestEditorStoryboardDraftCanBeBlankButAgentAppendCannot(t *testing.T) {
	doc := fixture(t, `{"nodes":[],"connections":[]}`)
	add := Operation{Type: "add_node", ID: "board", NodeType: "script", X: num(0), Y: num(0)}
	row := Operation{Type: "edit_storyboard", ID: "board", Action: "append_draft", RowID: "row", Patch: map[string]any{"durationSeconds": 6.0, "plotDescription": "", "videoMotionPrompt": ""}}
	result, err := Plan(doc, command(add, row), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	rows := maps(entity(result.Document, "node:board")["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"])
	if len(rows) != 1 || rows[0]["id"] != "row" || rows[0]["durationSeconds"] != 6.0 {
		t.Fatalf("blank draft not saved: %#v", rows)
	}
	row.Action = "append"
	if _, err := Plan(doc, command(add, row), ownerContext()); err == nil {
		t.Fatal("Agent append accepted an empty shot")
	}
	row.Action = "append_draft"
	row.Patch["durationSeconds"] = 0.0
	if _, err := Plan(doc, command(add, row), ownerContext()); err == nil {
		t.Fatal("draft bypassed duration validation")
	}
}

func TestEditorBatchConfigurationAndOverrides(t *testing.T) {
	doc := fixture(t, `{"nodes":[],"connections":[]}`)
	config := Operation{Type: "edit_batch_table", ID: "batch", Action: "configure", Patch: map[string]any{
		"operation": "creative", "concurrency": 5.0, "referenceMode": "cells", "globalPrompt": "Create",
		"referenceColumns":      []any{map[string]any{"id": "ref", "label": "Reference"}},
		"referenceColumnInputs": map[string]any{"ref": []any{}},
	}}
	result, err := Plan(doc, command(Operation{Type: "add_node", ID: "batch", NodeType: "batch-table", X: num(0), Y: num(0)}, config,
		Operation{Type: "edit_batch_table", ID: "batch", Action: "append", RowID: "row", Patch: map[string]any{"prompt": "", "inputNodeIds": []any{""}, "referenceOverrides": []any{"ref"}}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	table := entity(result.Document, "node:batch")["metadata"].(map[string]any)["batchTable"].(map[string]any)
	if table["concurrency"] != 5.0 || table["operation"] != "creative" || maps(table["rows"])[0]["referenceOverrides"] == nil {
		t.Fatalf("configuration lost: %#v", table)
	}
	for _, patch := range []map[string]any{
		{"concurrency": 3.0}, {"referenceColumns": []any{}}, {"referenceColumnInputs": map[string]any{"ref": []any{"missing"}}}, {"taskId": "forged"},
	} {
		config.Patch = patch
		if _, err := Plan(result.Document, command(config), ownerContext()); err == nil {
			t.Fatalf("unsafe config accepted: %#v", patch)
		}
	}
}

func TestEditorResourceBindingIsAnExplicitOperation(t *testing.T) {
	var op Operation
	if err := json.Unmarshal([]byte(`{"type":"bind_resource","id":"image","resourceId":"owned"}`), &op); err != nil {
		t.Fatal(err)
	}
	doc := fixture(t, `{"nodes":[{"id":"image","type":"image","metadata":{}}],"connections":[]}`)
	// No authorized resource metadata means rejection even for a valid node.
	if _, err := Plan(doc, command(op), ownerContext()); err == nil {
		t.Fatal("resource binding needs host authorization")
	}
}

func TestEditorResourceBindingUsesHostMetadata(t *testing.T) {
	for _, kind := range []string{"image", "video", "audio", "text"} {
		t.Run(kind, func(t *testing.T) {
			op := Operation{Type: "bind_resource", ID: "upload", ResourceID: "owned"}
			ctx := ownerContext()
			resourceKind, mime := kind, kind+"/example"
			if kind == "text" {
				resourceKind, mime = "file", "text/plain"
			}
			ctx.Resources = map[string]ResourceMetadata{"owned": {ID: "owned", Kind: resourceKind, MimeType: mime, Size: 123, Width: 640, Height: 480}}
			ctx.ResourceAllowed = func(id string) bool { return id == "owned" }
			doc := fixture(t, `{"nodes":[{"id":"upload","type":"`+kind+`","metadata":{"content":"typed","prompt":"keep","extension":"keep"}}],"connections":[]}`)
			result, err := Plan(doc, command(op), ctx)
			if err != nil {
				t.Fatal(err)
			}
			meta := entity(result.Document, "node:upload")["metadata"].(map[string]any)
			if meta["storageKey"] != "resource:owned" || meta["bytes"] != int64(123) || meta["mimeType"] != mime || meta["prompt"] != "keep" || meta["extension"] != "keep" {
				t.Fatalf("host metadata not applied: %#v", meta)
			}
			if kind == "text" && meta["content"] != "typed" {
				t.Fatalf("text replaced by locator: %#v", meta)
			}
			if kind != "text" && meta["content"] != "/api/resources/owned/file" {
				t.Fatalf("missing canonical locator: %#v", meta)
			}
			ctx.ResourceAllowed = func(string) bool { return false }
			if _, err := Plan(doc, command(op), ctx); err == nil {
				t.Fatal("resource policy bypassed")
			}
		})
	}
}

func TestEditorResourceBindingRejectsWrongKindAndManagedMedia(t *testing.T) {
	for _, raw := range []string{
		`{"nodes":[{"id":"upload","type":"video","metadata":{}}],"connections":[]}`,
		`{"nodes":[{"id":"upload","type":"image","metadata":{"status":"loading"}}],"connections":[]}`,
		`{"nodes":[{"id":"upload","type":"image","metadata":{"mediaVersion":{"id":"version"},"storageKey":"resource:old","content":"/api/resources/old/file"}}],"connections":[]}`,
	} {
		ctx := ownerContext()
		ctx.ResourceAllowed = func(string) bool { return true }
		ctx.Resources = map[string]ResourceMetadata{"owned": {ID: "owned", Kind: "image", MimeType: "image/png"}}
		if _, err := Plan(fixture(t, raw), command(Operation{Type: "bind_resource", ID: "upload", ResourceID: "owned"}), ctx); err == nil {
			t.Fatalf("unsafe media replacement accepted: %s", raw)
		}
	}
}

func TestEditorBatchColumnRemovalAndRowResizeAreAtomic(t *testing.T) {
	doc := fixture(t, `{"nodes":[{"id":"batch","type":"batch-table","metadata":{"batchTable":{"operation":"creative","concurrency":10,"referenceColumns":[{"id":"a","label":"A"},{"id":"b","label":"B"}],"rows":[{"id":"row","enabled":true,"prompt":"","inputNodeIds":["",""]}]}}}],"connections":[]}`)
	config := Operation{Type: "edit_batch_table", ID: "batch", Action: "configure", Patch: map[string]any{"referenceColumns": []any{map[string]any{"id": "a", "label": "A"}}}}
	row := Operation{Type: "edit_batch_table", ID: "batch", Action: "update", RowID: "row", Patch: map[string]any{"inputNodeIds": []any{""}}}
	if _, err := Plan(doc, command(config, row), ownerContext()); err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(doc, command(config), ownerContext()); err == nil {
		t.Fatal("column removal left oversized row references")
	}
}

func TestEditorBatchColumnSnapshotsGuardSourceVersions(t *testing.T) {
	doc := fixture(t, `{"nodes":[{"id":"image","type":"image"},{"id":"batch","type":"batch-table","metadata":{"batchTable":{"operation":"creative","concurrency":10,"referenceColumns":[{"id":"a","label":"A"}],"rows":[]}}}],"connections":[]}`)
	result, err := Plan(doc, command(Operation{Type: "edit_batch_table", ID: "batch", Action: "configure", Patch: map[string]any{"referenceColumnInputs": map[string]any{"a": []any{"image"}}}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	if !result.ReadSet["node:image"] {
		t.Fatal("column snapshot source missing from read set")
	}
}

func TestEditorLibraryPromptAndTagsUseDeclaredFields(t *testing.T) {
	doc := fixture(t, `{"nodes":[{"id":"image","type":"image","metadata":{}}],"connections":[]}`)
	result, err := Plan(doc, command(Operation{Type: "update_node", ID: "image", Patch: map[string]any{"prompt": "A library scene", "assetTags": []any{"scene"}}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	metadata := entity(result.Document, "node:image")["metadata"].(map[string]any)
	if metadata["prompt"] != "A library scene" || !reflect.DeepEqual(metadata["assetTags"], []any{"scene"}) {
		t.Fatalf("library metadata lost: %#v", metadata)
	}
}

func TestEditorBatchColumnSnapshotsReplaceRemovedColumns(t *testing.T) {
	doc := fixture(t, `{"nodes":[{"id":"batch","type":"batch-table","metadata":{"batchTable":{"operation":"creative","concurrency":10,"referenceColumns":[{"id":"a","label":"A"},{"id":"b","label":"B"}],"referenceColumnInputs":{"a":[],"b":[]},"rows":[]}}}],"connections":[]}`)
	result, err := Plan(doc, command(Operation{Type: "edit_batch_table", ID: "batch", Action: "configure", Patch: map[string]any{"referenceColumns": []any{map[string]any{"id": "a", "label": "A"}}, "referenceColumnInputs": map[string]any{"a": []any{}}}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	inputs := entity(result.Document, "node:batch")["metadata"].(map[string]any)["batchTable"].(map[string]any)["referenceColumnInputs"].(map[string]any)
	if _, exists := inputs["b"]; exists {
		t.Fatal("removed column snapshot retained")
	}
}

func TestEditorCharacterBindingUsesAuthorizedSnapshot(t *testing.T) {
	doc := fixture(t, `{"nodes":[],"connections":[]}`)
	ctx := ownerContext()
	meta := map[string]any{"workflowKind": "character", "characterAssetId": "hero", "assetId": "hero", "characterVersionId": "v1", "characterVersionPolicy": "current", "characterName": "Hero", "characterDefinition": map[string]any{"appearance": "Authoritative"}, "status": "success"}
	meta["characterCoverUrl"] = "/api/resources/hero-image/file"
	ctx.ResourceAllowed = func(id string) bool { return id == "hero-image" }
	ctx.CharacterBindings = map[string]map[string]any{"hero\x00v1\x00current": meta}
	result, err := Plan(doc, command(Operation{Type: "add_node", ID: "character", NodeType: "text", X: num(0), Y: num(0)}, Operation{Type: "bind_character", ID: "character", AssetID: "hero", VersionID: "v1", Action: "current"}), ctx)
	if err != nil {
		t.Fatal(err)
	}
	node := entity(result.Document, "node:character")
	if node["title"] != "Hero" || !reflect.DeepEqual(node["metadata"].(map[string]any)["characterDefinition"], meta["characterDefinition"]) {
		t.Fatalf("character snapshot lost: %#v", node)
	}
	if _, err := Plan(result.Document, command(Operation{Type: "bind_character", ID: "character", AssetID: "foreign", VersionID: "v1", Action: "current"}), ctx); err == nil {
		t.Fatal("foreign character accepted")
	}
	ctx.ResourceAllowed = func(string) bool { return false }
	if _, err := Plan(result.Document, command(Operation{Type: "bind_character", ID: "character", AssetID: "hero", VersionID: "v1", Action: "current"}), ctx); err == nil {
		t.Fatal("character cover bypassed resource policy")
	}
}

func TestEditorFolderTimestampIsAnAuthoredField(t *testing.T) {
	doc := fixture(t, `{"nodes":[],"connections":[]}`)
	result, err := Plan(doc, command(Operation{Type: "add_node", ID: "folder", NodeType: "frame", X: num(0), Y: num(0)}, Operation{Type: "update_node", ID: "folder", Patch: map[string]any{"folderStyle": "glass", "folderTheme": "aurora", "folderCreatedAt": "2026-10-25T12:00:00Z"}}), ownerContext())
	if err != nil {
		t.Fatal(err)
	}
	folder := entity(result.Document, "node:folder")["metadata"].(map[string]any)["folder"].(map[string]any)
	if folder["createdAt"] != "2026-10-25T12:00:00Z" {
		t.Fatal("folder date lost")
	}
}

func TestEditorLinkedFolderRequiresAuthorizedProjectFolder(t *testing.T) {
	doc := fixture(t, `{"nodes":[],"connections":[]}`)
	ops := []Operation{{Type: "add_node", ID: "folder", NodeType: "frame", X: num(0), Y: num(0)}, {Type: "update_node", ID: "folder", Patch: map[string]any{"folderProjectId": "project", "folderAssetFolderId": "asset-folder"}}}
	ctx := ownerContext()
	ctx.FolderBindings = map[string]bool{"project\x00asset-folder": true}
	if _, err := Plan(doc, command(ops...), ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(doc, command(ops...), ownerContext()); err == nil {
		t.Fatal("foreign folder accepted")
	}
}
