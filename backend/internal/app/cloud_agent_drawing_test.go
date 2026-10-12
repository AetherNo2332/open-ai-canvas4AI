package app

import (
	"encoding/json"
	"testing"
)

func TestAgentParityDrawingNativeEditAndAtomicRejection(t *testing.T) {
	doc := map[string]any{"nodes": []any{map[string]any{"id": "d", "type": "drawing", "metadata": map[string]any{"drawingEngine": "excalidraw"}}}, "connections": []any{}}
	args := cloudAgentDrawingArgs{NodeID: "d", Engine: "excalidraw", Operations: []cloudAgentDrawingOp{{Type: "upsert", ID: "rect", Record: map[string]any{"id": "rect", "type": "rectangle", "x": 1.0, "y": 2.0, "width": 100.0, "height": 80.0}}}}
	if err := editCloudAgentDrawingDocument(doc, args); err != nil {
		t.Fatal(err)
	}
	meta := cloudAgentNodeMetadata(creationMaps(doc["nodes"])[0])
	drawing := meta["drawingDocument"].(map[string]any)
	if drawing["shapeCount"] != 1 || drawing["revision"] != 1 {
		t.Fatalf("missing summary: %#v", drawing)
	}
	before, _ := json.Marshal(doc)
	args.Operations = append(args.Operations, cloudAgentDrawingOp{Type: "upsert", ID: "bad", Record: map[string]any{"id": "bad", "type": "image", "fileId": "missing"}})
	if err := editCloudAgentDrawingDocument(doc, args); err == nil {
		t.Fatal("unbound image accepted")
	}
	after, _ := json.Marshal(doc)
	if string(before) != string(after) {
		t.Fatal("rejected drawing edit changed document")
	}
	args.Operations = []cloudAgentDrawingOp{{Type: "remove", ID: "rect"}}
	if err := editCloudAgentDrawingDocument(doc, args); err != nil {
		t.Fatal(err)
	}
	if cloudAgentNodeMetadata(creationMaps(doc["nodes"])[0])["drawingShapeCount"] != 0 {
		t.Fatal("removal count stale")
	}
}

func TestAgentParityDrawingRejectsCredentialsAndTemporaryAssets(t *testing.T) {
	for _, value := range []any{map[string]any{"apiKey": "secret"}, map[string]any{"props": map[string]any{"src": "data:image/png;base64,A"}}, map[string]any{"url": "https://example.com/?token=secret"}} {
		if err := validateCloudAgentDrawingValue(value, 0); err == nil {
			t.Fatalf("accepted unsafe drawing value: %#v", value)
		}
	}
}

func TestAgentParityDrawingRemovalSchemaMatchesRuntime(t *testing.T) {
	for _, tool := range cloudAgentTools(CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}) {
		function := tool["function"].(map[string]any)
		if function["name"] != "canvas_edit_drawing" {
			continue
		}
		schema := function["parameters"].(map[string]any)
		valid := `{"snapshotHash":"h","nodeId":"d","engine":"excalidraw","operations":[{"type":"remove","id":"r"}]}`
		if err := validateCloudAgentToolArguments(schema, valid); err != nil {
			t.Fatal(err)
		}
		invalid := `{"snapshotHash":"h","nodeId":"d","engine":"excalidraw","operations":[{"type":"remove","id":"r","record":{}}]}`
		if err := validateCloudAgentToolArguments(schema, invalid); err == nil {
			t.Fatal("advertised removal schema accepts record, contrary to runtime")
		}
		return
	}
	t.Fatal("missing drawing edit tool")
}

func TestAgentParityDrawingRejectsNormalizedUnsafeFieldsAndLinks(t *testing.T) {
	for name, value := range map[string]any{
		"prototype":            map[string]any{"__proto__": map[string]any{"polluted": true}},
		"nestedCredential":     map[string]any{"customData": map[string]any{"api_key": "secret"}},
		"clientSecret":         map[string]any{"meta": map[string]any{"clientSecret": "secret"}},
		"accessKey":            map[string]any{"meta": map[string]any{"accessKeyId": "secret"}},
		"credentialBundle":     map[string]any{"meta": map[string]any{"credentials": map[string]any{"value": "secret"}}},
		"apiToken":             map[string]any{"meta": map[string]any{"apiToken": "secret"}},
		"authHeaders":          map[string]any{"meta": map[string]any{"authHeaders": map[string]any{"value": "secret"}}},
		"spacedScript":         map[string]any{"link": " \tjavascript:alert(1)"},
		"spacedTemporaryAsset": map[string]any{"link": "\ndata:image/png;base64,A"},
		"spacedSignedLink":     map[string]any{"link": " https://example.com/file?signature=secret"},
		"spacedUserInfo":       map[string]any{"link": " https://user:password@example.com/file"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCloudAgentDrawingValue(value, 0); err == nil {
				t.Fatal("unsafe native drawing value accepted")
			}
		})
	}
}
