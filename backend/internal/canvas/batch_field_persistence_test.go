package canvas

import (
	"encoding/json"
	"reflect"
	"testing"

	"yingce/backend/internal/model"
)

// 批量表的列 ID、行覆盖、列输入快照和重试签名都落在行字段里；字段文档保存
// 不允许丢掉这些业务状态，否则重开后会按当前连线重新分配或重复提交。
func TestBatchTableFieldsSurviveFormalSaveAndReopen(t *testing.T) {
	svc := newCanvasOperationTestService(t)
	payload := `{"referenceFieldsVersion":1,"nodes":[` +
		`{"id":"image-1","type":"image","title":"参考图","metadata":{}},` +
		`{"id":"text-1","type":"text","title":"说明","metadata":{"content":"布料"}},` +
		`{"id":"out-1","type":"image","title":"结果","metadata":{}},` +
		`{"id":"batch","type":"batch-table","title":"批量表","metadata":{"batchTable":{` +
		`"operation":"creative","concurrency":2,` +
		`"referenceColumns":[{"id":"ref-a","label":"参考图 1"}],` +
		`"textColumns":[{"id":"text-a","label":"文本 1"}],` +
		`"referenceColumnInputs":{"ref-a":["image-1"]},` +
		`"referenceMode":"cells",` +
		`"rows":[{"id":"row-1","enabled":true,"inputNodeIds":["image-1"],"referenceOverrides":["ref-a"],` +
		`"textNodeIds":["text-1"],"prompt":"生成一张图","outputNodeId":"out-1",` +
		`"generatedInputSignature":"sig-1","inputChanged":true,"cells":{"text-a":"布料"}}]}}}` +
		`],"connections":[]}`
	if err := svc.repo.Create(&model.CanvasProject{ID: "canvas", UserID: "user", Title: "批量", PayloadJSON: payload}); err != nil {
		t.Fatal(err)
	}
	request := CanvasOperationsRequest{MutationID: "rename-batch", ClientCapabilities: []string{CanvasClientCapabilityReferenceFields}, Operations: []CanvasOperation{{
		Type: "patch_node", ID: "batch",
		Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"批量表 2"`)},
		Before: map[string]json.RawMessage{"title": json.RawMessage(`"批量表"`)},
	}}}
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
	var table map[string]any
	for _, item := range saved["nodes"].([]any) {
		node := item.(map[string]any)
		if node["id"] == "batch" {
			table = node["metadata"].(map[string]any)["batchTable"].(map[string]any)
		}
	}
	if table == nil {
		t.Fatal("batch table missing after reopen")
	}
	if table["concurrency"] != float64(2) || table["referenceMode"] != "cells" {
		t.Fatalf("table settings lost: %#v", table)
	}
	if refs := table["referenceColumnInputs"].(map[string]any)["ref-a"]; !reflect.DeepEqual(refs, []any{"image-1"}) {
		t.Fatalf("referenceColumnInputs = %#v", refs)
	}
	columns := table["referenceColumns"].([]any)
	textColumns := table["textColumns"].([]any)
	if len(columns) != 1 || columns[0].(map[string]any)["id"] != "ref-a" || len(textColumns) != 1 || textColumns[0].(map[string]any)["id"] != "text-a" {
		t.Fatalf("column IDs lost: %#v / %#v", columns, textColumns)
	}
	rows := table["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %#v", rows)
	}
	row := rows[0].(map[string]any)
	if !reflect.DeepEqual(row["inputNodeIds"], []any{"image-1"}) ||
		!reflect.DeepEqual(row["referenceOverrides"], []any{"ref-a"}) ||
		!reflect.DeepEqual(row["textNodeIds"], []any{"text-1"}) {
		t.Fatalf("row inputs/overrides lost: %#v", row)
	}
	if row["outputNodeId"] != "out-1" || row["generatedInputSignature"] != "sig-1" || row["inputChanged"] != true {
		t.Fatalf("retry snapshot lost: %#v", row)
	}
	if cells := row["cells"].(map[string]any); cells["text-a"] != "布料" {
		t.Fatalf("explicit cell lost: %#v", cells)
	}
}
