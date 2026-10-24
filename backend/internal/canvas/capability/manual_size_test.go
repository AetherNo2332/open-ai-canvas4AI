package capability

import (
	"encoding/json"
	"testing"
)

func TestManualSizeIsTypedEditableIntentForEveryNode(t *testing.T) {
	for _, descriptor := range BuiltinRegistry().List() {
		t.Run(descriptor.Type, func(t *testing.T) {
			field, exists := descriptor.PatchFields["manualSize"]
			if !exists || field.Path != "metadata.manualSize" || field.JSONSchema()["type"] != "boolean" {
				t.Fatalf("manual resize has no typed editor path: %+v", field)
			}
			node := map[string]any{"metadata": map[string]any{"untouched": "retained"}}
			for _, value := range []bool{true, false} {
				if err := descriptor.ApplyPatch(node, map[string]any{"width": 500.0, "height": 320.0, "manualSize": value}); err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(node)
				if err != nil {
					t.Fatal(err)
				}
				var restored map[string]any
				if err := json.Unmarshal(encoded, &restored); err != nil {
					t.Fatal(err)
				}
				metadata := restored["metadata"].(map[string]any)
				if metadata["manualSize"] != value || metadata["untouched"] != "retained" || restored["width"] != 500.0 || restored["height"] != 320.0 {
					t.Fatalf("manual resize was not preserved: %+v", restored)
				}
			}
			for _, invalid := range []any{nil, "true", 1.0, []any{}, map[string]any{}} {
				if err := descriptor.ValidatePatch(map[string]any{"manualSize": invalid}); err == nil {
					t.Fatalf("invalid manual size intent accepted: %#v", invalid)
				}
			}
		})
	}
}
