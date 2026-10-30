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

// 浏览器 create_canvas 建出的新画布与命令层建画布一致使用声明式引用；同一请求带入
// 历史节点与连线（导入、离线首次同步）时先规范化，有冲突则保持历史画布。
func TestCreateCanvasOperationAdoptsDeclarativeReferences(t *testing.T) {
	create := func(t *testing.T, id string, extra ...CanvasOperation) map[string]any {
		t.Helper()
		svc := newCanvasOperationTestService(t)
		operations := append([]CanvasOperation{{Type: "create_canvas", ID: id, Patch: map[string]json.RawMessage{"title": json.RawMessage(`"新画布"`)}}}, extra...)
		if _, err := svc.ApplyUserCanvasOperations("user", id, CanvasOperationsRequest{MutationID: "create-" + id, Operations: operations}); err != nil {
			t.Fatal(err)
		}
		project, err := svc.repo.CanvasProjectForUser("user", id)
		if err != nil {
			t.Fatal(err)
		}
		var saved map[string]any
		if err := json.Unmarshal([]byte(project.PayloadJSON), &saved); err != nil {
			t.Fatal(err)
		}
		return saved
	}
	node := func(raw string) CanvasOperation {
		return CanvasOperation{Type: "create_node", Node: json.RawMessage(raw)}
	}
	edge := func(raw string) CanvasOperation {
		return CanvasOperation{Type: "create_connection", Connection: json.RawMessage(raw)}
	}

	t.Run("empty", func(t *testing.T) {
		if saved := create(t, "empty"); saved["referenceFieldsVersion"] != float64(1) {
			t.Fatalf("referenceFieldsVersion = %#v, want 1", saved["referenceFieldsVersion"])
		}
	})
	t.Run("legacy edge is normalized into a field", func(t *testing.T) {
		saved := create(t, "imported",
			node(`{"id":"img","type":"image","title":"参考图","metadata":{}}`),
			node(`{"id":"clip","type":"video","title":"视频","metadata":{}}`),
			edge(`{"id":"legacy","fromNodeId":"img","toNodeId":"clip"}`))
		if saved["referenceFieldsVersion"] != float64(1) {
			t.Fatalf("referenceFieldsVersion = %#v, want 1", saved["referenceFieldsVersion"])
		}
		for _, item := range saved["nodes"].([]any) {
			if n := item.(map[string]any); n["id"] == "clip" {
				spec, _ := n["metadata"].(map[string]any)["generationSpec"].(map[string]any)
				if bindings, _ := spec["referenceBindings"].([]any); len(bindings) != 1 {
					t.Fatalf("legacy edge was not normalized into referenceBindings: %#v", n["metadata"])
				}
			}
		}
	})
	stayedLegacy := func(t *testing.T, saved map[string]any, edges int) {
		t.Helper()
		if _, marked := saved["referenceFieldsVersion"]; marked {
			t.Fatalf("document was marked declarative: %#v", saved["referenceFieldsVersion"])
		}
		if got := saved["connections"].([]any); len(got) != edges {
			t.Fatalf("connections changed: %#v", got)
		}
	}
	t.Run("unresolvable references save as legacy", func(t *testing.T) {
		saved := create(t, "cycle",
			node(`{"id":"a","type":"image","title":"A","metadata":{}}`),
			node(`{"id":"b","type":"image","title":"B","metadata":{}}`),
			edge(`{"id":"ab","fromNodeId":"a","toNodeId":"b"}`),
			edge(`{"id":"ba","fromNodeId":"b","toNodeId":"a"}`))
		stayedLegacy(t, saved, 2)
	})
	t.Run("unknown node type saves as legacy", func(t *testing.T) {
		saved := create(t, "plugin",
			node(`{"id":"img","type":"image","title":"参考图","metadata":{}}`),
			node(`{"id":"ext","type":"plugin-widget","title":"插件","metadata":{}}`),
			edge(`{"id":"legacy","fromNodeId":"img","toNodeId":"ext"}`))
		stayedLegacy(t, saved, 1)
	})
	t.Run("conflict stays legacy", func(t *testing.T) {
		saved := create(t, "conflict",
			node(`{"id":"hero","type":"text","metadata":{"workflowKind":"character","characterAssetId":"hero-asset"}}`),
			node(`{"id":"scene","type":"image","metadata":{}}`),
			node(`{"id":"board","type":"script","metadata":{"storyboard":{"referenceNodeIds":[],"rows":[{"id":"shot","assetBindings":[{"nodeId":"hero","role":"character","priority":100}],"imageNodeId":"old-product","videoNodeId":"old-clip"}]}}}`),
			node(`{"id":"old-product","type":"image","metadata":{}}`),
			node(`{"id":"old-clip","type":"video","metadata":{}}`),
			node(`{"id":"new-product","type":"image","metadata":{}}`),
			edge(`{"id":"row-scene","fromNodeId":"scene","toNodeId":"board","toHandleId":"row:shot"}`),
			edge(`{"id":"output-new","fromNodeId":"board","toNodeId":"new-product","fromHandleId":"row:shot"}`))
		if _, marked := saved["referenceFieldsVersion"]; marked {
			t.Fatalf("conflicting import was marked declarative: %#v", saved["referenceFieldsVersion"])
		}
		if edges := saved["connections"].([]any); len(edges) != 2 {
			t.Fatalf("conflicting import connections changed: %#v", edges)
		}
	})
}
