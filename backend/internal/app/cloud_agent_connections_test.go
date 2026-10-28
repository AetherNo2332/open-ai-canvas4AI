package app

import (
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

// Task 8 关闭了 Agent 的连线操作：计划器、审批预览与真实执行入口统一拒绝
// connect_nodes／update_connection／delete_connection。端口、自环、重复边等成功语义
// 现在只属于原生命令与手动画布路径，由 backend/internal/canvas/commands 的用例覆盖。
func TestCloudAgentConnectionOperationsAreClosed(t *testing.T) {
	for _, opType := range cloudAgentLegacyConnectionOperations {
		doc := map[string]any{"nodes": []any{
			map[string]any{"id": "image", "type": "image"},
			map[string]any{"id": "video", "type": "video"},
		}, "connections": []any{}}
		before, _ := json.Marshal(doc)
		_, err := applyCloudAgentCanvasPlan(doc, []agentCanvasOp{{Type: opType, ID: "edge", FromNodeID: "image", ToNodeID: "video", ToHandleID: "row:r1"}})
		if err == nil || !strings.Contains(err.Error(), "connection_ops_disabled") {
			t.Fatalf("%s 未被拒绝：%v", opType, err)
		}
		after, _ := json.Marshal(doc)
		if string(before) != string(after) {
			t.Fatalf("%s 的失败批次改写了画布", opType)
		}
	}
}

func TestCloudAgentConnectionCatalogCapacity(t *testing.T) {
	s, db, _ := agentMediaFixture(t)
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceVolcengineArkVideo), "seedance-test")
	profile.Video.References.MaxImages = 1
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "video-cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, profile)).Error; err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"nodes": []any{map[string]any{"id": "a", "type": "image"}, map[string]any{"id": "b", "type": "image"}, map[string]any{"id": "v", "type": "video"}}, "connections": []any{map[string]any{"fromNodeId": "a", "toNodeId": "v"}, map[string]any{"fromNodeId": "b", "toNodeId": "v"}}}
	if err := validateCloudAgentConnectionCapacities(s.repo, doc, []agentCanvasOp{{Type: "connect_nodes", ToNodeID: "v"}}); err == nil {
		t.Fatal("server catalog capacity ignored")
	}
}

func TestCloudAgentConnectionRejectsNullHandle(t *testing.T) {
	_, err := decodeCloudAgentCanvasArgs(`{"snapshotHash":"h","ops":[{"type":"connect_nodes","id":"e","fromNodeId":"a","toNodeId":"b","toHandleId":null}]}`)
	if err == nil {
		t.Fatal("null handle bypassed string contract")
	}
}

func TestCloudAgentConnectionCharacterOrderMatchesCanvas(t *testing.T) {
	nodes := []map[string]any{
		{"id": "a", "type": "drawing", "metadata": map[string]any{"workflowKind": "character", "characterAssetId": "asset-a"}},
		{"id": "b", "type": "text", "metadata": map[string]any{"workflowKind": "character", "characterAssetId": "asset-b"}},
	}
	script := map[string]any{"id": "s", "metadata": map[string]any{"storyboard": map[string]any{}}}
	row := map[string]any{"characters": []any{map[string]any{"characterAssetId": "asset-b"}, map[string]any{"characterAssetId": "asset-a"}}}
	if got := cloudAgentConnectionComposer(script, row, nodes, "walk"); got != "参考资产：@绘图1 @角色1\nwalk" {
		t.Fatalf("character order differs from manual UI: %s", got)
	}
}
