package app

import (
	"encoding/json"
	"testing"
)

func TestCloudAgentPatchSchemaSupportsResettableTypeUnions(t *testing.T) {
	schema := cloudAgentPatchSchema()
	properties := schema["properties"].(map[string]any)
	for _, name := range []string{"workflowKind", "workflowTitle", "conversionState", "videoStartFrameNodeId"} {
		if properties[name] == nil {
			t.Fatalf("missing node feature field %s", name)
		}
	}
	// workflowKind combines a required string with resettable [string,null]
	// variants. Interface equality on those arrays used to panic at runtime.
	kind := properties["workflowKind"].(map[string]any)
	if kind["anyOf"] == nil || kind["type"] != nil {
		t.Fatalf("mixed types lost their union: %#v", kind)
	}
	if _, err := json.Marshal(schema); err != nil {
		t.Fatal(err)
	}
}
