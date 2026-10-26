package capability

import "testing"

func TestReferenceComposerDoesNotOverwriteAssetPrompt(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("video")
	spec := map[string]any{"version": 1.0, "mode": "video", "prompt": "参考 @图片1\n推进", "options": map[string]any{}, "referenceBindings": []any{}, "textInputMode": "prompt-only"}
	patch := map[string]any{"generationSpec": spec, "prompt": "推进", "content": "参考 @图片1\n推进"}
	if err := d.ValidatePatch(patch); err != nil {
		t.Fatal(err)
	}
	patch["content"] = "conflict"
	if err := d.ValidatePatch(patch); err == nil {
		t.Fatal("accepted conflicting generation composer")
	}
}

func TestEditorOptionalFieldReset(t *testing.T) {
	for _, kind := range []string{"image", "video", "audio", "text", "config"} {
		t.Run(kind, func(t *testing.T) {
			d, _ := BuiltinRegistry().Resolve(kind)
			meta := d.Metadata("old")
			meta["model"], meta["count"], meta["size"] = "old", float64(2), "16:9"
			node := map[string]any{"type": kind, "metadata": meta}
			patch := map[string]any{"content": nil, "model": nil, "count": nil, "size": nil, "generationType": nil}
			if err := d.ApplyPatch(node, patch); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"model", "count", "size", "generationType"} {
				if _, exists := meta[key]; exists {
					t.Fatalf("reset retained %s: %#v", key, meta)
				}
			}
			for _, alias := range []string{"width", "height", "title", "generationSpec"} {
				if err := d.ValidatePatch(map[string]any{alias: nil}); err == nil {
					t.Fatalf("accepted protected reset %s", alias)
				}
			}
		})
	}
}

func TestEditorRichTextResetKeepsNewBody(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("text")
	meta := map[string]any{"content": "old", "richText": map[string]any{"type": "doc"}}
	if err := d.ApplyPatch(map[string]any{"metadata": meta}, map[string]any{"content": "new", "richText": nil}); err != nil {
		t.Fatal(err)
	}
	if meta["content"] != "new" {
		t.Fatal(meta)
	}
	if _, exists := meta["richText"]; exists {
		t.Fatal("rich text was not removed")
	}
}

func TestEditorWorkflowAndTemplateFields(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("config")
	node := map[string]any{"metadata": d.Metadata("")}
	patch := map[string]any{"workflowProvider": "runninghub", "workflowTitle": "Workflow", "workflowParameters": map[string]any{"seed": float64(42), "random": true, "source:prompt": "hello"}, "promptTemplateVariables": map[string]any{"shot": "wide"}}
	if err := d.ApplyPatch(node, patch); err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyPatch(node, map[string]any{"workflowTitle": nil, "workflowParameters": map[string]any{}, "promptTemplateVariables": nil}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []any{map[string]any{"__proto__": "bad"}, map[string]any{"nested": map[string]any{"secret": "bad"}}} {
		if err := d.ValidatePatch(map[string]any{"workflowParameters": invalid}); err == nil {
			t.Fatal("accepted invalid workflow parameters")
		}
	}
}

func TestEditorResetUpdatesGenerationContract(t *testing.T) {
	d, _ := BuiltinRegistry().Resolve("image")
	node := map[string]any{"metadata": d.Metadata("")}
	spec := map[string]any{"version": 1.0, "mode": "image", "prompt": "draft", "modelSelection": map[string]any{"kind": "logical", "logicalModelId": "old"}, "options": map[string]any{"count": 2.0, "size": "16:9"}, "referenceBindings": []any{}, "textInputMode": "prompt-only"}
	if err := d.ApplyPatch(node, map[string]any{"generationSpec": spec}); err != nil {
		t.Fatal(err)
	}
	if err := d.ApplyPatch(node, map[string]any{"content": nil, "model": nil, "count": nil, "size": nil}); err != nil {
		t.Fatal(err)
	}
	meta := node["metadata"].(map[string]any)
	if meta["composerContent"] != "" {
		t.Fatalf("prompt mirror was not cleared: %#v", meta)
	}
	for _, key := range []string{"count", "size", "model", "logicalModelId"} {
		if _, exists := meta[key]; exists {
			t.Fatalf("contract resurrected cleared %s", key)
		}
	}
}
