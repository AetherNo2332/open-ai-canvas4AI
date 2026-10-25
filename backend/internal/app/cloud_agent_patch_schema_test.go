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

func TestCloudAgentPatchSchemaPreflightEnforcesTypeUnions(t *testing.T) {
	schema := cloudAgentPatchSchema()
	for _, test := range []struct {
		name string
		raw  string
		pass bool
	}{
		{"reset string", `{"workflowTitle":null}`, true},
		{"string", `{"workflowTitle":"分镜"}`, true},
		{"wrong nullable type", `{"workflowTitle":42}`, false},
		{"reset number", `{"shotIndex":null}`, true},
		{"number", `{"shotIndex":2}`, true},
		{"wrong number type", `{"shotIndex":"two"}`, false},
		{"number bounds", `{"shotIndex":100001}`, false},
		{"reset array", `{"referenceAssetNodeIds":null}`, true},
		{"array", `{"referenceAssetNodeIds":["image-1"]}`, true},
		{"wrong array type", `{"referenceAssetNodeIds":42}`, false},
		{"reset object", `{"cameraControl":null}`, true},
		{"wrong object type", `{"cameraControl":"camera"}`, false},
		{"scalar alternatives", `{"workflowKind":"storyboard"}`, true},
		{"wrong alternatives type", `{"workflowKind":42}`, false},
		{"wrong alternatives enum", `{"workflowKind":"unregistered-role"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Exercise both runtime Go values and the JSON form supplied to Pi.
			wire, _ := json.Marshal(schema)
			var decoded map[string]any
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			for _, parameters := range []map[string]any{schema, decoded} {
				if err := validateCloudAgentToolArguments(parameters, test.raw); (err == nil) != test.pass {
					t.Fatalf("pass=%v: %v", test.pass, err)
				}
			}
		})
	}
}
