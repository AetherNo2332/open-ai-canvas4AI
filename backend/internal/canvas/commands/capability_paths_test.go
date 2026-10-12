package commands

import (
	"infinite-canvas/backend/internal/canvas/capability"
	"testing"
)

func TestCapabilitiesExposeCanonicalEditorPathsAndStructuredFields(t *testing.T) {
	out := Capabilities()
	nodes := out["nodes"].([]any)
	descriptors := map[string]capability.Descriptor{}
	for _, d := range capability.BuiltinRegistry().List() {
		descriptors[d.Type] = d
	}
	for _, raw := range nodes {
		node := raw.(map[string]any)
		d, ok := descriptors[node["type"].(string)]
		if !ok {
			t.Fatal("missing descriptor")
		}
		fields := node["patchFields"].(map[string]any)
		for key, field := range d.PatchFields {
			schema := fields[key].(map[string]any)
			if schema["path"] != field.Path {
				t.Fatalf("%s %s missing canonical editor path", d.Type, key)
			}
		}
	}
	structured, ok := out["structuredFields"].(map[string]any)
	if !ok {
		t.Fatal("structured fields missing")
	}
	if _, ok = structured["storyboard"].(map[string]any)["dialogue"]; !ok {
		t.Fatal("storyboard contract missing")
	}
}
