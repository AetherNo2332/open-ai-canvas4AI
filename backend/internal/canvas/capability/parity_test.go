package capability

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestRegistryGraphPermissionChangesInvalidateContract(t *testing.T) {
	first := testDescriptor()
	baseline, err := NewRegistry([]Descriptor{first})
	if err != nil {
		t.Fatal(err)
	}
	first.Connection.CanGraphSource = true
	changed, err := NewRegistry([]Descriptor{first})
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Hash() == changed.Hash() {
		t.Fatal("graph source permission did not invalidate contract")
	}
	first.Connection.CanGraphTarget = true
	second, err := NewRegistry([]Descriptor{first})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Hash() == second.Hash() {
		t.Fatal("graph target permission did not invalidate contract")
	}
}

func TestBuiltinEditableParity(t *testing.T) {
	registry := BuiltinRegistry()
	types := []string{"image", "text", "drawing", "script", "skill", "config", "video", "audio", "frame", "markdown", "svg", "html", "panorama", "compare", "chart", "colorgrade", "media-conversion", "batch-table"}
	if len(registry.Types()) != len(types) {
		t.Fatalf("createable builtins = %v", registry.Types())
	}
	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			d, ok := registry.Resolve(typ)
			if !ok {
				t.Fatalf("builtin %s is unavailable", typ)
			}
			node := map[string]any{"type": typ, "metadata": d.Metadata("draft")}
			if err := d.ApplyPatch(node, map[string]any{"title": "Edited", "x": 0.0, "y": -100.0, "width": 500.0, "height": 300.0, "locked": true}); err != nil {
				t.Fatal(err)
			}
			if node["width"] != 500.0 || node["metadata"].(map[string]any)["locked"] != true {
				t.Fatalf("editable fields did not persist: %#v", node)
			}
			for _, key := range []string{"status", "taskId", "taskStatus", "storageKey", "assetId", "apiKey", "metadata", "pluginData", "producedModel"} {
				if err := d.ValidatePatch(map[string]any{key: "forged"}); err == nil {
					t.Fatalf("protected field %s accepted", key)
				}
			}
			for _, patch := range []map[string]any{{"width": 0.0}, {"height": -1.0}, {"x": math.Inf(1)}, {"width": 1e9}} {
				if err := d.ValidatePatch(patch); err == nil {
					t.Fatalf("invalid geometry accepted: %#v", patch)
				}
			}
		})
	}
}

func TestBuiltinTypedEditableConfiguration(t *testing.T) {
	fixtures := []struct {
		typ   string
		patch map[string]any
	}{
		{"text", map[string]any{"listMode": true, "fontSize": 18.0}},
		{"frame", map[string]any{"frame": map[string]any{"collapsed": true, "expandedWidth": 760.0, "expandedHeight": 520.0}}},
		{"config", map[string]any{"generationMode": "video", "model": "logical-model", "size": "16:9", "count": 2.0}},
		{"chart", map[string]any{"chartKind": "line", "content": "[{\"name\":\"a\",\"value\":1}]"}},
		{"panorama", map[string]any{"panoramaConfig": map[string]any{"projection": "cylindrical", "sourceMode": "image", "smartBase": false}}},
		{"colorgrade", map[string]any{"colorGrade": map[string]any{"brightness": 110.0, "contrast": 90.0, "saturate": 100.0, "hueRotate": -20.0}}},
		{"media-conversion", map[string]any{"conversionOperation": "depth"}},
		{"script", map[string]any{"visibleColumns": []any{"shotNumber", "dialogue"}, "storyboardShotDuration": "5"}},
	}
	registry := BuiltinRegistry()
	for _, fixture := range fixtures {
		t.Run(fixture.typ, func(t *testing.T) {
			d, ok := registry.Resolve(fixture.typ)
			if !ok {
				t.Fatal("missing builtin")
			}
			node := map[string]any{"metadata": d.Metadata("")}
			if err := d.ApplyPatch(node, fixture.patch); err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(node)
			if len(data) == 0 {
				t.Fatal("empty node")
			}
		})
	}
}

func TestStructuredPatchRejectsForgedResultsAndUnknownFields(t *testing.T) {
	registry := BuiltinRegistry()
	for _, fixture := range []struct {
		typ   string
		patch map[string]any
	}{
		{"frame", map[string]any{"frame": map[string]any{"collapsed": true, "expandedWidth": -1.0, "expandedHeight": 520.0}}},
		{"panorama", map[string]any{"panoramaConfig": map[string]any{"projection": "spherical", "sourceMode": "image", "smartBase": false, "directImageUrl": "https://forged"}}},
		{"colorgrade", map[string]any{"colorGrade": map[string]any{"brightness": 100.0, "contrast": 100.0, "saturate": 100.0, "hueRotate": 0.0, "taskStatus": "succeeded"}}},
		{"config", map[string]any{"generationMode": "secret-mode"}},
		{"script", map[string]any{"visibleColumns": []any{"taskId"}}},
	} {
		d, _ := registry.Resolve(fixture.typ)
		node := map[string]any{"metadata": map[string]any{"sentinel": true}}
		before, _ := json.Marshal(node)
		if err := d.ApplyPatch(node, fixture.patch); err == nil {
			t.Fatalf("unsafe %s configuration accepted", fixture.typ)
		}
		after, _ := json.Marshal(node)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("invalid patch partially applied")
		}
	}
}

func TestStructuredPatchOwnsPersistedValue(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("frame")
	frame := map[string]any{"collapsed": false, "expandedWidth": 760.0, "expandedHeight": 520.0}
	node := map[string]any{}
	if err := d.ApplyPatch(node, map[string]any{"frame": frame}); err != nil {
		t.Fatal(err)
	}
	frame["taskId"] = "forged-after-validation"
	if _, exists := node["metadata"].(map[string]any)["frame"].(map[string]any)["taskId"]; exists {
		t.Fatal("caller mutated validated persisted structure")
	}
}

func TestSchemaMatchesNestedValidationAndIsProtected(t *testing.T) {
	registry := BuiltinRegistry()
	d, _ := registry.Resolve("panorama")
	field := d.PatchFields["panoramaConfig"]
	schema := field.JSONSchema()
	properties := schema["properties"].(map[string]any)
	if schema["additionalProperties"] != false || properties["directImageUrl"] != nil || !reflect.DeepEqual(properties["projection"].(map[string]any)["enum"], []string{"spherical", "cylindrical"}) {
		t.Fatalf("unsafe schema: %#v", schema)
	}
	child := field.Properties["projection"]
	child.Enum[0] = "secret-mode"
	fresh, _ := registry.Resolve("panorama")
	if err := fresh.ValidatePatch(map[string]any{"panoramaConfig": map[string]any{"projection": "spherical", "sourceMode": "ai", "smartBase": true}}); err != nil {
		t.Fatal("registry contract mutated", err)
	}
	config, _ := registry.Resolve("config")
	if err := config.ValidatePatch(map[string]any{"count": 1.5}); err == nil {
		t.Fatal("fractional generation count accepted")
	}
	if schema := config.PatchFields["count"].JSONSchema(); schema["multipleOf"] != 1 || schema["minimum"] != 1.0 || schema["maximum"] != 100.0 {
		t.Fatalf("count schema: %#v", schema)
	}
}

func TestBuiltinContractHashDeterministicAndTracksConstraints(t *testing.T) {
	want := BuiltinRegistry().Hash()
	for i := 0; i < 20; i++ {
		if BuiltinRegistry().Hash() != want {
			t.Fatal("builtin contract nondeterministic")
		}
	}
	d := testDescriptor()
	a, _ := NewRegistry([]Descriptor{d})
	field := d.PatchFields["weight"]
	field.Limit = 12
	d.PatchFields["weight"] = field
	b, _ := NewRegistry([]Descriptor{d})
	if a.Hash() == b.Hash() {
		t.Fatal("number constraint absent from contract hash")
	}
}

func TestEditableReadPrunesUnknownNestedFieldsAndMarksTruncation(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("panorama")
	values, truncated := d.EditableValues(map[string]any{"title": "A long title", "metadata": map[string]any{"panoramaConfig": map[string]any{"projection": "spherical", "sourceMode": "image", "smartBase": true, "directImageUrl": "https://private"}, "apiKey": "secret"}}, 3)
	if values["title"] != "A l" || !truncated["title"] {
		t.Fatalf("truncation absent: %v %v", values, truncated)
	}
	config, ok := values["panoramaConfig"].(map[string]any)
	if !ok || config["projection"] != "spherical" || config["directImageUrl"] != nil || values["apiKey"] != nil {
		t.Fatalf("unsafe or incomplete read: %#v", values)
	}
	script, _ := BuiltinRegistry().Resolve("script")
	values, _ = script.EditableValues(map[string]any{"metadata": map[string]any{"storyboard": map[string]any{"visibleColumns": []any{"dialogue", "taskId"}}}}, 100)
	if !reflect.DeepEqual(values["visibleColumns"], []any{"dialogue"}) {
		t.Fatalf("nested field unreadable or unsafe: %#v", values)
	}
}

func TestTypedRichTextAndSubtitles(t *testing.T) {
	text, _ := BuiltinRegistry().Resolve("text")
	rich := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "attrs": map[string]any{"textAlign": "center"}, "content": []any{map[string]any{"type": "text", "text": "Styled text", "marks": []any{map[string]any{"type": "bold"}, map[string]any{"type": "textStyle", "attrs": map[string]any{"color": "#ffffff"}}}}}}}}
	node := map[string]any{"metadata": map[string]any{}}
	if err := text.ApplyPatch(node, map[string]any{"richText": rich}); err != nil {
		t.Fatal(err)
	}
	if node["metadata"].(map[string]any)["content"] != "Styled text" {
		t.Fatal("rich text plain content was not synchronized")
	}
	for _, doc := range []map[string]any{{"type": "doc", "content": []any{map[string]any{"type": "paragraph"}}}, {"type": "doc", "content": []any{map[string]any{"type": "horizontalRule"}}}} {
		if err := text.ApplyPatch(node, map[string]any{"richText": doc}); err != nil {
			t.Fatal(err)
		}
	}
	video, _ := BuiltinRegistry().Resolve("video")
	if err := video.ValidatePatch(map[string]any{"subtitleHighlights": []any{map[string]any{"entryIndex": 1.0, "start": 0.0, "end": 3.0, "highlightText": "😀a", "sourceText": "😀a"}}}); err != nil {
		t.Fatal("UTF-16 subtitle offsets rejected", err)
	}
	if err := video.ValidatePatch(map[string]any{"subtitleEntries": []any{map[string]any{"index": 1.0, "startMs": 0.0, "endMs": 2000.0, "text": "Hello"}}, "subtitleHighlights": []any{map[string]any{"entryIndex": 1.0, "start": 0.0, "end": 5.0, "highlightText": "Hello", "sourceText": "Hello"}}, "subtitleStyle": map[string]any{"fontSize": 18.0, "color": "#ffffff", "position": "bottom", "highlightEnabled": true, "highlightBackgroundColor": "#111111", "highlightTextColor": "#ffd54a", "highlightPaddingX": 6.0, "highlightPaddingY": 2.0, "highlightRadius": 4.0, "highlightAnimation": "pop", "maxCharsPerEntry": 35.0, "autoResegment": true}}); err != nil {
		t.Fatal(err)
	}
	for _, patch := range []map[string]any{
		{"subtitleEntries": []any{map[string]any{"index": 1.0, "startMs": 2000.0, "endMs": 1000.0, "text": "Backwards"}}},
		{"subtitleHighlights": []any{map[string]any{"entryIndex": 1.0, "start": 5.0, "end": 3.0, "highlightText": "a", "sourceText": "abc"}}},
		{"subtitleStyle": map[string]any{"fontSize": 18.0, "color": "url(https://private)"}},
	} {
		if err := video.ValidatePatch(patch); err == nil {
			t.Fatalf("invalid subtitles accepted: %#v", patch)
		}
	}
	unsafe := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "click", "marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"href": "javascript:alert(1)"}}}}}}}}
	if err := text.ValidatePatch(map[string]any{"richText": unsafe}); err == nil {
		t.Fatal("unsafe rich text link accepted")
	}
}

func TestGenerationSpecUsesCredentialFreeSharedContract(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("image")
	spec := map[string]any{"version": 1.0, "mode": "image", "prompt": "draft", "modelSelection": map[string]any{"kind": "logical", "logicalModelId": "model-1"}, "options": map[string]any{"count": 2.0, "transparentBackground": true}, "referenceBindings": []any{}, "textInputMode": "prompt-only"}
	if err := d.ValidatePatch(map[string]any{"generationSpec": spec}); err != nil {
		t.Fatal(err)
	}
	node := map[string]any{"metadata": map[string]any{"content": "resource:result", "prompt": "submitted", "status": "success"}}
	if err := d.ApplyPatch(node, map[string]any{"generationSpec": spec}); err != nil {
		t.Fatal(err)
	}
	meta := node["metadata"].(map[string]any)
	if meta["composerContent"] != "draft" || meta["count"] != float64(2) || meta["transparentBackground"] != "true" || meta["prompt"] != "submitted" || meta["content"] != "resource:result" {
		t.Fatalf("spec options not mirrored safely: %#v", meta)
	}
	spec["apiKey"] = "secret"
	if err := d.ValidatePatch(map[string]any{"generationSpec": spec}); err == nil {
		t.Fatal("credential in generation spec accepted")
	}
	delete(spec, "apiKey")
	spec["mode"] = "audio"
	if err := d.ValidatePatch(map[string]any{"generationSpec": spec}); err == nil {
		t.Fatal("mode-incompatible generation option accepted")
	}
}

func TestStructuredRowsValidateEditorialFieldsAndRejectResultForgery(t *testing.T) {
	fields := StoryboardRowFields()
	valid := map[string]any{"durationSeconds": 5.0, "mustHave": []any{"identity"}, "characters": []any{map[string]any{"characterName": "Alice", "characterDescription": "Hero", "characterImageNodeId": "image-1"}}, "assetBindings": []any{map[string]any{"nodeId": "image-1", "role": "character", "priority": 1.0}}, "imagePromptTemplateVariables": map[string]any{"subject": "Alice"}, "sourceStartMs": 0.0, "sourceEndMs": 1000.0}
	if err := ValidateEditableFields(fields, valid); err != nil {
		t.Fatal(err)
	}
	for _, patch := range []map[string]any{{"status": "success"}, {"imageNodeId": "forged-result"}, {"characters": []any{map[string]any{"characterName": "Alice", "characterAssetId": "foreign"}}}, {"imagePromptTemplateVariables": map[string]any{"subject": map[string]any{"apiKey": "secret"}}}} {
		if err := ValidateEditableFields(fields, patch); err == nil {
			t.Fatalf("forged storyboard row accepted: %#v", patch)
		}
	}
	batch := BatchRowFields()
	if err := ValidateEditableFields(batch, map[string]any{"enabled": true, "inputNodeIds": []any{"image-1"}, "textNodeIds": []any{"text-1"}, "cells": map[string]any{"col-1": "Value"}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEditableFields(batch, map[string]any{"outputNodeId": "forged-result"}); err == nil {
		t.Fatal("batch output forgery accepted")
	}
}

func TestMediaConfigurationDoesNotSwitchTypeOrLoseConversionFreshness(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("image")
	spec := map[string]any{"version": 1.0, "mode": "video", "prompt": "draft", "options": map[string]any{}, "referenceBindings": []any{}, "textInputMode": "prompt-only"}
	if err := d.ValidatePatch(map[string]any{"generationSpec": spec}); err == nil {
		t.Fatal("image accepted video generation contract")
	}
	conversion, _ := BuiltinRegistry().Resolve("media-conversion")
	node := map[string]any{"metadata": map[string]any{"mediaConversion": map[string]any{"schemaVersion": 1.0, "operation": "grayscale", "status": "completed", "resultStorageKey": "resource:1"}}}
	if err := conversion.ApplyPatch(node, map[string]any{"conversionOperation": "depth"}); err != nil {
		t.Fatal(err)
	}
	state := node["metadata"].(map[string]any)["mediaConversion"].(map[string]any)
	if state["status"] != "stale" || state["resultStorageKey"] != "resource:1" {
		t.Fatal("changed conversion operation retained completed status")
	}
	drawing, _ := BuiltinRegistry().Resolve("drawing")
	if err := drawing.ApplyPatch(map[string]any{"metadata": map[string]any{"drawingId": "doc-1", "drawingEngine": "tldraw"}}, map[string]any{"drawingEngine": "excalidraw"}); err == nil {
		t.Fatal("existing drawing document switched incompatible engine")
	}
}

func TestPlainDocumentLargeContentAndCanonicalAliasConflicts(t *testing.T) {
	text, _ := BuiltinRegistry().Resolve("text")
	if err := text.ValidatePatch(map[string]any{"content": strings.Repeat("a", 1<<20)}); err != nil {
		t.Fatal("full document rejected", err)
	}
	if err := text.ValidatePatch(map[string]any{"content": strings.Repeat("a", (1<<20)+1)}); err == nil {
		t.Fatal("unbounded document accepted")
	}
	rich := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "styled"}}}}}
	if err := text.ValidatePatch(map[string]any{"content": "different", "richText": rich}); err == nil {
		t.Fatal("conflicting rich and plain content accepted")
	}
	image, _ := BuiltinRegistry().Resolve("image")
	spec := map[string]any{"version": 1.0, "mode": "image", "prompt": "draft", "options": map[string]any{}, "referenceBindings": []any{}, "textInputMode": "prompt-only"}
	if err := image.ValidatePatch(map[string]any{"content": "different", "generationSpec": spec}); err == nil {
		t.Fatal("conflicting canonical and composer content accepted")
	}
}

func TestConversionNoopPreservesCompletedResult(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("media-conversion")
	node := map[string]any{"metadata": map[string]any{"mediaConversion": map[string]any{"schemaVersion": 1.0, "operation": "depth", "status": "completed", "resultStorageKey": "resource:1"}}}
	if err := d.ApplyPatch(node, map[string]any{"conversionOperation": "depth"}); err != nil {
		t.Fatal(err)
	}
	if node["metadata"].(map[string]any)["mediaConversion"].(map[string]any)["status"] != "completed" {
		t.Fatal("no-op conversion option invalidated result")
	}
}

func TestLegacyMediaEditsSynchronizeCanonicalSpecAtomically(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("video")
	spec := map[string]any{"version": 1.0, "mode": "video", "prompt": "old", "options": map[string]any{"durationSeconds": 5.0, "generateAudio": true}, "referenceBindings": []any{}, "textInputMode": "prompt-only"}
	node := map[string]any{"metadata": map[string]any{"generationSpec": spec, "content": "resource:video", "prompt": "submitted"}}
	if err := d.ApplyPatch(node, map[string]any{"content": "new", "seconds": "10", "generateAudio": "false"}); err != nil {
		t.Fatal(err)
	}
	meta := node["metadata"].(map[string]any)
	updated := meta["generationSpec"].(map[string]any)
	options := updated["options"].(map[string]any)
	if updated["prompt"] != "new" || options["durationSeconds"] != 10.0 || options["generateAudio"] != false || meta["content"] != "resource:video" || meta["prompt"] != "submitted" {
		t.Fatalf("canonical draft drifted: %#v", meta)
	}
	before, _ := json.Marshal(node)
	if err := d.ApplyPatch(node, map[string]any{"title": "changed", "seconds": "bad"}); err == nil {
		t.Fatal("invalid canonical duration accepted")
	}
	after, _ := json.Marshal(node)
	if string(before) != string(after) {
		t.Fatal("failed canonical update partially persisted")
	}
}
