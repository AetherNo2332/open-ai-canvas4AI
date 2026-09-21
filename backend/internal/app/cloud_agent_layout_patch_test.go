package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
)

// 本文件从 dev 的 cloud_agent_layout_patch_test.go 恢复。
//
// 未恢复的用例：TestCloudAgentStoryboardAssetBindings —— 它断言 canvas_edit_storyboard 的
// patch 允许 assetBindings。上游（合并树取的那一侧）明确禁止改素材绑定，而我们自己的
// cloudAgentStoryboardBindingsInDoc / validateCloudAgentStoryboardBindings 也不在本次移植清单里，
// 因此这条断言依赖合并树不具备的能力，见报告"需要人工确认"。

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

// 兜底：单条事件载荷超过硬上限时降级为"需要刷新"（不再进检查点，但仍要过校验），
// 而不是让整轮判死。
func TestCloudAgentOversizedEventPayloadDegradesToRefresh(t *testing.T) {
	huge := strings.Repeat("x", cloudAgentEventPayloadLimitBytes+1024)
	state := &cloudAgentRuntime{Events: []CloudAgentEvent{{
		RunID: "run-1", Seq: 1, EventID: "event-1", Type: "canvas_updated",
		Payload: map[string]any{
			"canvasId":    "canvas-1",
			"text":        "整理了 50 个节点",
			"canvasPatch": map[string]any{"nodes": []any{map[string]any{"after": map[string]any{"id": "n1", "metadata": map[string]any{"content": huge}}}}},
		},
	}}}
	payload := cloudAgentBoundEventPayload(state.Events[0].Payload)
	state.Events[0].Payload = payload
	if _, exists := payload["canvasPatch"]; exists {
		t.Fatalf("超限的 canvasPatch 应被丢弃：%+v", payload)
	}
	if payload["requiresRefresh"] != true || payload["payloadSlimmedBytes"] == nil {
		t.Fatalf("应标记需要刷新并记录省略体积：%+v", payload)
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

// 分镜行编辑只下发变化的那几行：整节点 before/after 会把每行正文重复两遍，
// 一轮 55 次分镜写入就把 canvas_updated 载荷推到 257KB（线上那轮就是因此撞线）。
func TestCloudAgentStoryboardRowDeltaShrinksPatch(t *testing.T) {
	body := strings.Repeat("镜头正文", 800) // 单行 ~3.2KB
	row := func(id string, shot float64, prompt string) map[string]any {
		return map[string]any{"id": id, "shotNumber": shot, "imageGenerationPrompt": prompt, "plotDescription": body, "dialogue": body}
	}
	storyboardNode := func(rows []map[string]any) map[string]any {
		return map[string]any{
			"id": "sb-1", "type": "script", "title": "第一幕", "position": map[string]any{"x": 0.0, "y": 0.0}, "width": 920.0, "height": 360.0,
			"metadata": map[string]any{"status": "idle", "storyboard": map[string]any{"rows": rows, "hasMore": false, "nextOffset": 0}},
		}
	}
	beforeRows := []map[string]any{row("r1", 1, "旧1"), row("r2", 2, "旧2"), row("r3", 3, "旧3")}
	afterRows := []map[string]any{row("r1", 1, "旧1"), row("r2", 2, "新2"), row("r3", 3, "旧3")}
	before, after := storyboardNode(beforeRows), storyboardNode(afterRows)

	shrunkBefore, shrunkAfter := cloudAgentShrinkChange(before, after)
	full, _ := json.Marshal([]map[string]any{{"before": before, "after": after}})
	shrunk, _ := json.Marshal([]map[string]any{{"before": shrunkBefore, "after": shrunkAfter}})
	if len(shrunk) >= len(full)/4 {
		t.Fatalf("行级增量没有明显缩小：%d → %d", len(full), len(shrunk))
	}
	rowsBefore := rowsOfNode(t, shrunkBefore)
	rowsAfter := rowsOfNode(t, shrunkAfter)
	if len(rowsBefore) != 1 || len(rowsAfter) != 1 || stringValue(rowsAfter[0]["id"]) != "r2" {
		t.Fatalf("增量行集合不对：before=%+v after=%+v", rowsBefore, rowsAfter)
	}
	if _, carried := rowsAfter[0]["dialogue"]; carried {
		t.Fatalf("未变化的字段不应出现在增量里：%+v", rowsAfter[0])
	}
	if rowsAfter[0]["imageGenerationPrompt"] != "新2" || rowsBefore[0]["imageGenerationPrompt"] != "旧2" {
		t.Fatalf("变化字段缺失：before=%+v after=%+v", rowsBefore[0], rowsAfter[0])
	}

	// 删除行下发完整旧行；新增行下发完整新行。
	removedBefore, removedAfter := cloudAgentShrinkChange(storyboardNode(beforeRows), storyboardNode(beforeRows[1:]))
	removedRows := rowsOfNode(t, removedBefore)
	if len(removedRows) != 1 || removedRows[0]["dialogue"] == nil {
		t.Fatalf("删除行必须带完整旧行：%+v", removedRows)
	}
	if got := rowsOfNode(t, removedAfter); len(got) != 0 {
		t.Fatalf("删除行的 after 应为空：%+v", got)
	}

	// 标题变化（非行变化）仍发完整节点。
	titleChanged := storyboardNode(afterRows)
	titleChanged["title"] = "第一幕（改）"
	if keptBefore, _ := cloudAgentShrinkChange(before, titleChanged); len(rowsOfNode(t, keptBefore)) != 3 {
		t.Fatalf("非行变化不应被缩成行增量：%+v", keptBefore)
	}
}

func rowsOfNode(t *testing.T, node map[string]any) []map[string]any {
	t.Helper()
	meta, _ := node["metadata"].(map[string]any)
	board, ok := cloudAgentStoryboardBoard(meta)
	if !ok {
		return nil
	}
	return cloudAgentRowsOf(board["rows"])
}

// 分镜/批量表读到的行正文必须原样保留：卸载机制已删除（它原本是被 512KiB 状态守卫逼出来的），
// 现在轮内只裁剪图片；正文改写历史中段既作废后续前缀缓存、又会让模型重复读取。
func TestCloudAgentReadBodiesForStoryboardAndBatchTableSurviveSave(t *testing.T) {
	body := strings.Repeat("镜头正文", 900)
	rows := make([]any, 0, 40)
	for index := 0; index < 40; index++ {
		rows = append(rows, map[string]any{"id": fmt.Sprintf("row-%02d", index), "shotNumber": float64(index + 1), "plotDescription": body, "dialogue": body})
	}
	request := canonicalAgentRequest{Messages: []map[string]any{
		{"role": "user", "content": "读一下分镜"},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call-1", "function": map[string]any{"name": "canvas_read_storyboard", "arguments": "{}"}}}},
		{"role": "tool", "tool_call_id": "call-1", "content": patchTestJSON(map[string]any{"nodeId": "sb-1", "snapshotHash": "hash-1", "storyboard": map[string]any{"rows": rows, "hasMore": false, "nextOffset": 0}})},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call-2", "function": map[string]any{"name": "canvas_read_batch_table", "arguments": "{}"}}}},
		{"role": "tool", "tool_call_id": "call-2", "content": patchTestJSON(map[string]any{"nodeId": "bt-1", "batchTable": map[string]any{"operation": "try_on", "rows": rows[:10], "hasMore": true, "nextOffset": 10}})},
		{"role": "assistant", "content": "读完了"},
		{"role": "user", "content": "继续"},
	}}
	before, _ := json.Marshal(request.Messages)
	if changed, pruned := cloudAgentPruneInspectedImages(&request, nil); changed || pruned != 0 {
		t.Fatalf("纯文本历史不该被裁剪：changed=%v pruned=%d", changed, pruned)
	}
	after, _ := json.Marshal(request.Messages)
	if string(before) != string(after) {
		t.Fatal("读到的行正文被改写了")
	}
	storyboard := mustDecodeJSON(t, request.Messages[2]["content"].(string))
	board, _ := storyboard["storyboard"].(map[string]any)
	if len(cloudAgentRowsOf(board["rows"])) != 40 {
		t.Fatalf("分镜行正文丢失：%+v", board)
	}
	table := mustDecodeJSON(t, request.Messages[4]["content"].(string))
	tableBody, _ := table["batchTable"].(map[string]any)
	if len(cloudAgentRowsOf(tableBody["rows"])) != 10 {
		t.Fatalf("批量表行正文丢失：%+v", tableBody)
	}
	if tableBody["operation"] != "try_on" {
		t.Fatalf("批量表事实字段丢失：%+v", tableBody)
	}
}

func patchTestJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func mustDecodeJSON(t *testing.T, raw string) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		t.Fatalf("解码失败：%v", err)
	}
	return value
}
