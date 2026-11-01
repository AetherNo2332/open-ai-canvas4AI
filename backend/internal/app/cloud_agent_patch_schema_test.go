package app

import (
	"encoding/json"
	"strings"
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
	// variants; the advertised field keeps that union as a merged type list.
	kind := properties["workflowKind"].(map[string]any)
	if types := cloudAgentSchemaStrings(kind["type"]); len(types) != 2 || types[0] != "null" || types[1] != "string" || kind["anyOf"] != nil {
		t.Fatalf("mixed types lost their union: %#v", kind)
	}
	if _, err := json.Marshal(schema); err != nil {
		t.Fatal(err)
	}
}

// 公共文本字段在 schema 里保留完整合同；其余字段只声明类型，取值约束按节点类型从 canvas_list_node_types 读取。
func TestCloudAgentPatchSchemaKeepsCommonFieldsAndTypesTheRest(t *testing.T) {
	properties := cloudAgentPatchSchema()["properties"].(map[string]any)
	for name := range cloudAgentPatchCommonFields {
		raw, _ := json.Marshal(properties[name])
		if properties[name] == nil || !strings.Contains(string(raw), "maxLength") {
			t.Fatalf("common field %s must keep its full contract: %s", name, raw)
		}
	}
	for name, value := range properties {
		if cloudAgentPatchCommonFields[name] {
			continue
		}
		field := value.(map[string]any)
		extra := len(field) - 1
		if _, ok := field["items"]; ok {
			extra--
		}
		if extra != 0 || field["type"] == nil {
			t.Fatalf("field %s must only declare its type: %#v", name, field)
		}
	}
	description, _ := cloudAgentPatchSchema()["description"].(string)
	if !strings.Contains(description, "canvas_list_node_types(nodeType)") {
		t.Fatalf("patch schema must point to the per-type contract: %q", description)
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
		{"reset array", `{"referenceAssetNodeIds":null}`, true},
		{"array", `{"referenceAssetNodeIds":["image-1"]}`, true},
		{"wrong array type", `{"referenceAssetNodeIds":42}`, false},
		{"reset object", `{"cameraControl":null}`, true},
		{"wrong object type", `{"cameraControl":"camera"}`, false},
		{"scalar alternatives", `{"workflowKind":"storyboard"}`, true},
		{"wrong alternatives type", `{"workflowKind":42}`, false},
		{"common field length", `{"title":"` + strings.Repeat("长", 241) + `"}`, false},
		{"unknown field", `{"notAField":"x"}`, false},
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

// 类型之外的约束（枚举、数值范围）不在工具 schema 里，节点描述符在预览时仍会拒绝，回执指出具体字段。
func TestCloudAgentPatchValuesStillValidatedByNodeDescriptor(t *testing.T) {
	s, _, _ := agentMediaFixture(t)
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	hash := cloudAgentCanvasHash(doc)
	for _, test := range []struct{ name, patch, want string }{
		{"enum outside the node contract", `{"workflowKind":"unregistered-role"}`, "workflowKind"},
		{"number above the node bound", `{"shotIndex":100001}`, "shotIndex"},
		{"field the node type does not own", `{"storyboardShotCount":"3"}`, "storyboardShotCount"},
	} {
		t.Run(test.name, func(t *testing.T) {
			call := cloudAgentCall{ID: "patch-" + test.want}
			call.Function.Name = "canvas_apply_ops"
			call.Function.Arguments = `{"snapshotHash":"` + hash + `","ops":[{"type":"update_node","id":"shot-1","patch":` + test.patch + `}]}`
			if err := validateCloudAgentToolArguments(cloudAgentToolParameters(t, "canvas_apply_ops"), call.Function.Arguments); err != nil {
				t.Fatalf("type-valid patch must pass preflight: %v", err)
			}
			_, err := prepareCloudAgentCanvasMutation(s.repo, "user", "agent-canvas", call)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("descriptor must reject %s with a field-specific error, got %v", test.patch, err)
			}
		})
	}
}

func cloudAgentToolParameters(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, tool := range cloudAgentTools(tierAutoRequest()) {
		function, _ := tool["function"].(map[string]any)
		if function["name"] == name {
			parameters, _ := function["parameters"].(map[string]any)
			return parameters
		}
	}
	t.Fatalf("tool %s not compiled", name)
	return nil
}
