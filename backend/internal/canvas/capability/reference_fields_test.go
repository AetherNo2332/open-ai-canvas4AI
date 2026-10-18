package capability

import "testing"

func TestReferenceFieldsContract(t *testing.T) {
	registry := BuiltinRegistry()
	for _, kind := range []string{"compare", "panorama", "svg", "html", "chart", "colorgrade", "media-conversion"} {
		t.Run(kind, func(t *testing.T) {
			d, _ := registry.Resolve(kind)
			node := map[string]any{"id": "target", "type": kind, "metadata": map[string]any{}}
			binding := map[string]any{"id": "reference:source", "nodeId": "source", "mediaType": "image", "role": "reference", "order": float64(0), "resolution": "latest"}
			if err := d.ApplyPatch(node, map[string]any{"inputBindings": []any{binding}}); err != nil {
				t.Fatalf("cannot declare inputs: %v", err)
			}
			if err := d.ApplyPatch(node, map[string]any{"inputBindings": []any{}}); err != nil {
				t.Fatalf("cannot clear inputs: %v", err)
			}
			if got := node["metadata"].(map[string]any)["inputBindings"].([]any); len(got) != 0 {
				t.Fatal("clear retained previous bindings")
			}
		})
	}
	for _, kind := range []string{"image", "video", "audio", "config"} {
		d, _ := registry.Resolve(kind)
		if _, ok := d.PatchFields["generationSpec"]; !ok {
			t.Fatalf("%s lost generation contract", kind)
		}
		if _, ok := d.PatchFields["inputBindings"]; ok {
			t.Fatalf("%s has two writable input contracts", kind)
		}
	}
	frame, _ := registry.Resolve("frame")
	if _, ok := frame.PatchFields["inputBindings"]; ok {
		t.Fatal("frame gained inputs")
	}
	for _, field := range []string{"imageNodeId", "videoNodeId", "taskId", "status"} {
		if err := ValidateEditableFields(StoryboardRowFields(), map[string]any{field: "forged"}); err == nil {
			t.Fatalf("system field %s accepted", field)
		}
	}
}
