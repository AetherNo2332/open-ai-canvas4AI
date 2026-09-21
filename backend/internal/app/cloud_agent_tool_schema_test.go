package app

import (
	"errors"
	"testing"
)

// 本地 JSON Schema 预检的单元用例：只覆盖我们 schema 真正用到的关键字。
func TestCloudAgentToolArgumentValidation(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"snapshotHash": map[string]any{"type": "string"},
			"mode":         map[string]any{"type": "string", "enum": []any{"row", "grid"}},
			"count":        map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
			"ratio":        map[string]any{"type": "number", "minimum": 0, "maximum": 1},
			"dryRun":       map[string]any{"type": "boolean"},
			"nodeIds":      map[string]any{"type": "array", "maxItems": 3, "items": map[string]any{"type": "string"}},
			"groups": map[string]any{"type": "array", "items": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{"label": map[string]any{"type": "string"}},
				"required":             []string{"label"},
				"additionalProperties": false,
			}},
		},
		"required":             []string{"snapshotHash"},
		"additionalProperties": false,
	}
	cases := []struct {
		name      string
		raw       string
		field     string
		issue     string
		wantError bool
	}{
		{name: "合法参数", raw: `{"snapshotHash":"h","mode":"row","count":3,"ratio":0.5,"dryRun":true,"nodeIds":["a"],"groups":[{"label":"g"}]}`},
		{name: "缺必填字段", raw: `{}`, field: "snapshotHash", issue: "required", wantError: true},
		{name: "必填字段为 null", raw: `{"snapshotHash":null}`, field: "snapshotHash", issue: "required", wantError: true},
		{name: "类型不符", raw: `{"snapshotHash":12}`, field: "snapshotHash", issue: "type_mismatch", wantError: true},
		{name: "整数不能是小数", raw: `{"snapshotHash":"h","count":1.5}`, field: "count", issue: "type_mismatch", wantError: true},
		{name: "未定义字段", raw: `{"snapshotHash":"h","whatever":1}`, field: "whatever", issue: "unknown_field", wantError: true},
		{name: "枚举外取值", raw: `{"snapshotHash":"h","mode":"circle"}`, field: "mode", issue: "invalid_value", wantError: true},
		{name: "超出上限", raw: `{"snapshotHash":"h","count":21}`, field: "count", issue: "invalid_value", wantError: true},
		{name: "低于下限", raw: `{"snapshotHash":"h","ratio":-1}`, field: "ratio", issue: "invalid_value", wantError: true},
		{name: "数组超限", raw: `{"snapshotHash":"h","nodeIds":["a","b","c","d"]}`, field: "nodeIds", issue: "item_count", wantError: true},
		{name: "数组元素类型不符", raw: `{"snapshotHash":"h","nodeIds":[1]}`, field: "nodeIds[0]", issue: "type_mismatch", wantError: true},
		{name: "嵌套对象缺字段", raw: `{"snapshotHash":"h","groups":[{"label":"g"},{}]}`, field: "groups[1].label", issue: "required", wantError: true},
		{name: "嵌套对象多字段", raw: `{"snapshotHash":"h","groups":[{"label":"g","extra":1}]}`, field: "groups[0].extra", issue: "unknown_field", wantError: true},
		{name: "非法 JSON", raw: `{"snapshotHash":`, field: "arguments", issue: "invalid_json", wantError: true},
		{name: "不是对象", raw: `["snapshotHash"]`, field: "arguments", issue: "type_mismatch", wantError: true},
		{name: "空参数按空对象处理", raw: ``, field: "snapshotHash", issue: "required", wantError: true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			err := validateCloudAgentToolArguments(schema, item.raw)
			if !item.wantError {
				if err != nil {
					t.Fatalf("应通过校验，实际：%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("应被拒绝")
			}
			var fieldErr *cloudAgentFieldArgumentError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("应是字段级参数错误：%v", err)
			}
			if fieldErr.Field != item.field || fieldErr.Issue != item.issue {
				t.Fatalf("字段结论 = %s/%s，期望 %s/%s", fieldErr.Field, fieldErr.Issue, item.field, item.issue)
			}
		})
	}
}

// 条件必填（oneOf + const）：canvas_apply_ops 的 ops 项按 type 决定必填字段。
func TestCloudAgentToolArgumentValidationConditionalBranches(t *testing.T) {
	// 空运行时没有下发过的工具表：预检必须退回平台工具集判断，而不是把所有调用当成幻觉。
	if _, advertised := cloudAgentAdvertisedTool(&cloudAgentRuntime{}, "canvas_apply_ops"); advertised {
		t.Fatal("空运行时不应有工具表")
	}
	req := agentTestRequest()
	req.PermissionMode = "auto" // canvas_apply_ops 是写入工具，read_only 下不暴露
	tools := cloudAgentTools(req)
	var parameters map[string]any
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if stringField(function, "name") == "canvas_apply_ops" {
			parameters, _ = function["parameters"].(map[string]any)
		}
	}
	if parameters == nil {
		t.Fatal("缺少 canvas_apply_ops schema")
	}
	valid := `{"snapshotHash":"h","ops":[{"type":"add_node","id":"n1","nodeType":"text"}]}`
	if err := validateCloudAgentToolArguments(parameters, valid); err != nil {
		t.Fatalf("合法 add_node 应通过：%v", err)
	}
	missingNodeType := `{"snapshotHash":"h","ops":[{"type":"add_node","id":"n1"}]}`
	err := validateCloudAgentToolArguments(parameters, missingNodeType)
	var fieldErr *cloudAgentFieldArgumentError
	if !errors.As(err, &fieldErr) || fieldErr.Field != "ops[0]" || fieldErr.Issue != "invalid_value" {
		t.Fatalf("add_node 缺 nodeType 应被条件必填拒绝：%v", err)
	}
	missingPatch := `{"snapshotHash":"h","ops":[{"type":"update_node","id":"n1"}]}`
	if err := validateCloudAgentToolArguments(parameters, missingPatch); err == nil {
		t.Fatal("update_node 缺 patch 应被拒绝")
	}
	unknownType := `{"snapshotHash":"h","ops":[{"type":"delete_node","id":"n1"}]}`
	if err := validateCloudAgentToolArguments(parameters, unknownType); err == nil {
		t.Fatal("未定义的 op 类型应被拒绝")
	}
	// patch 的键来自能力 registry，未声明的键要走 unknown_field。
	badPatch := `{"snapshotHash":"h","ops":[{"type":"update_node","id":"n1","patch":{"nonsense":1}}]}`
	err = validateCloudAgentToolArguments(parameters, badPatch)
	if !errors.As(err, &fieldErr) || fieldErr.Issue != "unknown_field" {
		t.Fatalf("patch 未声明键应被拒绝：%v", err)
	}
}
