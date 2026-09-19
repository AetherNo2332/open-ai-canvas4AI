package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

// 位置/尺寸变化只发几何增量：一次整理几十个分镜节点时，完整 before/after 会把单条
// canvas_updated 事件顶穿 128KB 上限，整轮被判"超过安全限制"（线上回归见 PR 说明）。
func TestCloudAgentGeometryOnlyChangeShrinksPatch(t *testing.T) {
	heavy := strings.Repeat("分镜行正文", 2000)
	before := map[string]any{
		"id": "sb-1", "type": "script", "title": "第一场",
		"position": map[string]any{"x": 0.0, "y": 0.0}, "width": 920.0, "height": 360.0,
		"metadata": map[string]any{"storyboard": map[string]any{"rows": []any{heavy}}, "status": "idle"},
	}
	after := map[string]any{
		"id": "sb-1", "type": "script", "title": "第一场",
		"position": map[string]any{"x": 120.0, "y": 480.0}, "width": 920.0, "height": 360.0,
		"metadata": map[string]any{"storyboard": map[string]any{"rows": []any{heavy}}, "status": "idle"},
	}
	shrunkBefore, shrunkAfter := cloudAgentShrinkChange(before, after)
	raw, _ := json.Marshal([]map[string]any{{"before": shrunkBefore, "after": shrunkAfter}})
	if len(raw) > 512 {
		t.Fatalf("几何增量仍然过大（%d 字节）：%s", len(raw), raw)
	}
	if shrunkBefore["id"] != "sb-1" || shrunkAfter["id"] != "sb-1" {
		t.Fatalf("增量必须带 id：%+v %+v", shrunkBefore, shrunkAfter)
	}
	if _, ok := shrunkAfter["position"]; !ok {
		t.Fatalf("几何增量缺少 position：%+v", shrunkAfter)
	}
	if _, ok := shrunkAfter["metadata"]; ok {
		t.Fatalf("几何增量不应携带正文字段：%+v", shrunkAfter)
	}

	// 内容变化（正文/metadata）仍发完整 before/after，保证前端的三方合并与任务状态保护不变。
	contentAfter := map[string]any{
		"id": "sb-1", "type": "script", "title": "第一场",
		"position": after["position"], "width": 920.0, "height": 360.0,
		"metadata": map[string]any{"storyboard": map[string]any{"rows": []any{heavy + "改"}}, "status": "idle"},
	}
	keptBefore, keptAfter := cloudAgentShrinkChange(before, contentAfter)
	if _, ok := keptAfter["metadata"]; !ok {
		t.Fatalf("内容变化被错误地缩成几何增量：%+v", keptAfter)
	}
	if keptBefore["id"] != before["id"] || keptBefore["metadata"] == nil {
		t.Fatalf("内容变化应保留完整 before：%+v", keptBefore)
	}

	// 新增节点（before 为空）不缩。
	if _, after := cloudAgentShrinkChange(nil, after); after["metadata"] == nil {
		t.Fatalf("新增节点不应被缩减：%+v", after)
	}
}

// 兜底：单条事件载荷超过硬上限时降级为"需要刷新"，而不是让整轮失败。
func TestCloudAgentSlimEventHistoryDegradesOversizedPayload(t *testing.T) {
	huge := strings.Repeat("x", cloudAgentEventPayloadLimitBytes+1024)
	state := &cloudAgentRuntime{Events: []CloudAgentEvent{{
		RunID: "run-1", Seq: 1, EventID: "event-1", Type: "canvas_updated",
		Payload: map[string]any{
			"canvasId":    "canvas-1",
			"text":        "整理了 50 个节点",
			"canvasPatch": map[string]any{"nodes": []any{map[string]any{"after": map[string]any{"id": "n1", "metadata": map[string]any{"content": huge}}}}},
		},
	}}}
	if !cloudAgentSlimEventHistory(state, false) {
		t.Fatal("超限载荷未被治理")
	}
	payload := state.Events[0].Payload
	if _, exists := payload["canvasPatch"]; exists {
		t.Fatalf("超限的 canvasPatch 应被丢弃：%+v", payload)
	}
	if payload["canvasPatchSlimmed"] != true || payload["requiresRefresh"] != true {
		t.Fatalf("应标记需要刷新：%+v", payload)
	}
	raw, _ := json.Marshal(payload)
	if len(raw) > cloudAgentEventPayloadLimitBytes {
		t.Fatalf("治理后仍然超限：%d", len(raw))
	}
	state.Request = agentTestRequest()
	state.Profile = cloudAgentProfileSnapshot{Revision: agentProfileRevision(nil), Hash: agentProfileHash("")}
	if _, policy, err := compileCloudAgentPolicies(state.Request, nil, "{}", state.Profile); err == nil {
		state.Policy = policy
	} else {
		t.Fatal(err)
	}
	state.TaskIDs = []string{"task-1"}
	state.Decisions = map[string]string{}
	state.Events[0].CreatedAt = time.Now()
	if err := validateCloudAgentRuntime(&model.CloudAgentExecution{ID: "run-1"}, state); err != nil {
		t.Fatalf("治理后应能通过状态校验：%v", err)
	}
}
