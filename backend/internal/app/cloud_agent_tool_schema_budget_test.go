package app

import (
	"encoding/json"
	"reflect"
	"testing"
)

// 工具 schema 是每一步都要发出去（并且是前缀缓存的第一段）的固定开销，因此值得钉住体积：
// 平台工具全集按 auto + canvas + 技能构造（覆盖条件暴露的工具）。
func platformToolSchema(t *testing.T) ([]map[string]any, []byte) {
	t.Helper()
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, SkillIDs: []string{"capability-list"}}
	req.Budget.MaxGenerationTasks = 1
	tools := cloudAgentTools(req)
	raw, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	return tools, raw
}

func TestCloudAgentToolSchemaStaysCompact(t *testing.T) {
	tools, raw := platformToolSchema(t)
	t.Logf("platform tool schema: %d tools, %d bytes", len(tools), len(raw))
	for _, tool := range tools {
		encoded, _ := json.Marshal(tool)
		if len(encoded) > 2000 {
			t.Logf("%v: %d bytes", tool["function"].(map[string]any)["name"], len(encoded))
		}
	}

	// 条件必填不是 type 枚举能表达的约束，压缩描述不能删掉协议语义。
	found := false
	for _, tool := range tools {
		function, _ := tool["function"].(map[string]any)
		if function["name"] != "canvas_apply_ops" {
			continue
		}
		found = true
		parameters, _ := function["parameters"].(map[string]any)
		ops, _ := parameters["properties"].(map[string]any)
		items, _ := ops["ops"].(map[string]any)
		opItem, _ := items["items"].(map[string]any)
		// 节点操作及免费的媒体版本切换/取出；边操作与端口字段已删除。
		want := []map[string]any{
			{"properties": map[string]any{"type": map[string]any{"const": "add_node"}}, "required": []string{"nodeType"}},
			{"properties": map[string]any{"type": map[string]any{"const": "update_node"}}, "required": []string{"patch"}},
			{"properties": map[string]any{"type": map[string]any{"const": "delete_node"}}},
		}
		branches, ok := opItem["oneOf"].([]map[string]any)
		if !ok || len(branches) != 10 || !reflect.DeepEqual(branches[:3], want) || !reflect.DeepEqual(branches[8], map[string]any{"properties": map[string]any{"type": map[string]any{"const": "use_media_version"}}, "required": []string{"versionId"}}) || !reflect.DeepEqual(branches[9], map[string]any{"properties": map[string]any{"type": map[string]any{"const": "extract_media_version"}}, "required": []string{"versionId", "nodeId"}}) {
			t.Fatalf("操作类型条件必填约束丢失: %#v", opItem["oneOf"])
		}
		if props, ok := opItem["properties"].(map[string]any); !ok || len(props) == 0 {
			t.Fatal("canvas_apply_ops 的 ops.items 必须保留 properties")
		}
	}
	if !found {
		t.Fatal("canvas_apply_ops 未暴露")
	}

	// The eligible catalog stays complete for authorization. The model wire is
	// the frozen L0∪L1 advertised subset for an empty canvas with no L1 intent.
	_ = raw
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, SkillIDs: []string{"capability-list"}}
	req.Budget.MaxGenerationTasks = 1
	advertised := cloudAgentToolsNamed(tools, cloudAgentAdvertisedToolNames(req, cloudAgentCanvasInventory{}, "现在画布有几个节点"))
	wire, err := json.Marshal(advertised)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("eligible catalog: %d tools, %d bytes; advertised wire: %d tools, %d bytes", len(cloudAgentToolNames(tools)), len(raw), len(cloudAgentToolNames(advertised)), len(wire))
	if len(wire) > 64000 {
		t.Fatalf("模型实际接收的 advertised 工具 schema 超出 64000 字节：%d", len(wire))
	}
}
