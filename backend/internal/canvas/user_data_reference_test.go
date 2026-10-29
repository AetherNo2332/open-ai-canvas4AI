package canvas

import (
	"encoding/json"
	"reflect"
	"testing"

	"yingce/backend/internal/model"
)

// 浏览器 operations 只改节点标题时，Agent 写入的声明式引用字段与文档版本必须原样保留。
func TestOperationsPreserveReferenceFields(t *testing.T) {
	svc := newCanvasOperationTestService(t)
	payload := `{"referenceFieldsVersion":1,"nodes":[` +
		`{"id":"img","type":"image","title":"参考图","metadata":{}},` +
		`{"id":"target","type":"image","title":"旧标题","metadata":{"inputBindings":[{"id":"node:img:reference","nodeId":"img","mediaType":"image","role":"reference","order":0,"resolution":"latest"}]}}` +
		`],"connections":[{"id":"ref-1","fromNodeId":"img","toNodeId":"target","managedBy":"reference-fields-v1","referenceOwnerNodeId":"target","referenceBindingId":"node:img:reference"}]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "user", PayloadJSON: payload}); err != nil {
		t.Fatal(err)
	}
	request := CanvasOperationsRequest{MutationID: "rename", Operations: []CanvasOperation{{Type: "patch_node", ID: "target",
		Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"新标题"`)},
		Before: map[string]json.RawMessage{"title": json.RawMessage(`"旧标题"`)}}}}
	if _, err := svc.ApplyUserCanvasOperations("user", "canvas", request); err != nil {
		t.Fatal(err)
	}
	project, err := svc.repo.CanvasProjectForUser("user", "canvas")
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal([]byte(project.PayloadJSON), &saved); err != nil {
		t.Fatal(err)
	}
	if saved["referenceFieldsVersion"] != float64(1) {
		t.Fatalf("referenceFieldsVersion = %#v, want 1", saved["referenceFieldsVersion"])
	}
	var target map[string]any
	for _, node := range saved["nodes"].([]any) {
		if n := node.(map[string]any); n["id"] == "target" {
			target = n
		}
	}
	if target == nil || target["title"] != "新标题" {
		t.Fatalf("target node = %#v, want renamed", target)
	}
	want := []any{map[string]any{"id": "node:img:reference", "nodeId": "img", "mediaType": "image", "role": "reference", "order": float64(0), "resolution": "latest"}}
	if got := target["metadata"].(map[string]any)["inputBindings"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("inputBindings = %#v, want %#v", got, want)
	}
	if edges := saved["connections"].([]any); len(edges) != 1 || edges[0].(map[string]any)["managedBy"] != "reference-fields-v1" {
		t.Fatalf("connections = %#v, want managed reference edge kept", edges)
	}
}
