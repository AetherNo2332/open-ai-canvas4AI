package app

import (
	"encoding/json"
	"fmt"
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

// 单步净增超过预算时，折叠最旧的画布增量而不是删事件（seq 必须连续）。
func TestCloudAgentFoldEventPayloadsToBudget(t *testing.T) {
	state := &cloudAgentRuntime{Request: agentTestRequest(), Decisions: map[string]string{}, TaskIDs: []string{"task-1"}}
	big := strings.Repeat("行正文", 4000) // 每份补丁 ~36KB
	for i := 0; i < 3; i++ {
		state.event("run-1", "canvas_updated", map[string]any{
			"canvasId": "canvas-1", "text": "修改分镜",
			"canvasPatch": map[string]any{"canvasId": "canvas-1", "nodes": []any{map[string]any{"after": map[string]any{"id": "sb-1", "metadata": map[string]any{"storyboard": big}}}}},
		})
	}
	rawBefore, _ := json.Marshal(state)
	if !cloudAgentFoldEventPayloadsToBudget(state, int(float64(len(rawBefore))*0.5)) {
		t.Fatal("超预算时应折叠最旧的画布增量")
	}
	folded := 0
	for index, event := range state.Events {
		if event.Payload["canvasPatchSlimmed"] == true {
			folded++
			if _, exists := event.Payload["canvasPatch"]; exists {
				t.Fatalf("折叠后不应还带 canvasPatch：%+v", event.Payload)
			}
			if event.Seq != index+1 {
				t.Fatalf("折叠不得改变事件序号：seq=%d index=%d", event.Seq, index)
			}
		}
	}
	if folded == 0 {
		t.Fatal("没有任何事件被折叠")
	}
	rawAfter, _ := json.Marshal(state)
	if len(rawAfter) >= len(rawBefore) {
		t.Fatalf("折叠后体积未下降：%d → %d", len(rawBefore), len(rawAfter))
	}
	// 预算内不应触发任何折叠。
	budgeted := &cloudAgentRuntime{Request: agentTestRequest(), Decisions: map[string]string{}, TaskIDs: []string{"task-1"}}
	budgeted.event("run-1", "canvas_updated", map[string]any{"canvasId": "canvas-1", "canvasPatch": map[string]any{"nodes": []any{}}})
	rawBudgeted, _ := json.Marshal(budgeted)
	if cloudAgentFoldEventPayloadsToBudget(budgeted, len(rawBudgeted)-1024) {
		t.Fatal("预算内的增长不应触发折叠")
	}
}

// 卸载必须覆盖分镜/批量表读取结果与写回执：线上那轮 99 条消息里 0 个卸载候选，
// 正是因为正文藏在 storyboard.rows 与 tool_call 参数里，而不是 content/nodes 键上。
func TestCloudAgentEvictionCoversStoryboardAndBatchTable(t *testing.T) {
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
	if len(before) < cloudAgentEvictionThresholdBytes {
		t.Fatalf("测试载荷太小：%d", len(before))
	}
	changed, beforeBytes, afterBytes := compactCloudAgentContext(&request, nil)
	if !changed || afterBytes >= beforeBytes {
		t.Fatalf("分镜/批量表正文未被卸载：%d → %d", beforeBytes, afterBytes)
	}
	storyboard := mustDecodeJSON(t, request.Messages[2]["content"].(string))
	board, _ := storyboard["storyboard"].(map[string]any)
	if _, exists := board["rows"]; exists {
		t.Fatalf("分镜行正文应被移出：%+v", board)
	}
	if board["totalRows"] != float64(40) || len(cloudAgentRowsOf(board["shotNumbers"])) != 0 {
		// shotNumbers 是镜号数组（不是行对象），这里只校验规模与骨架存在
		if board["totalRows"] != float64(40) {
			t.Fatalf("应保留规模骨架：%+v", board)
		}
	}
	if numbers, ok := board["shotNumbers"].([]any); !ok || len(numbers) != 40 {
		t.Fatalf("应保留镜号骨架便于模型判断存在性：%+v", board["shotNumbers"])
	}
	if storyboard["snapshotHash"] != "hash-1" || storyboard["contextCompacted"] != true {
		t.Fatalf("事实字段必须保留：%+v", storyboard)
	}
	table := mustDecodeJSON(t, request.Messages[4]["content"].(string))
	tableBody, _ := table["batchTable"].(map[string]any)
	if _, exists := tableBody["rows"]; exists {
		t.Fatalf("批量表行正文应被移出：%+v", tableBody)
	}
	if tableBody["totalRows"] != float64(10) || tableBody["operation"] != "try_on" {
		t.Fatalf("批量表骨架不完整：%+v", tableBody)
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

// 分镜行现在可以关联真实画布素材（此前工具明确禁止，模型只能把 [参考: …] 写进提示词文本）。
func TestCloudAgentStoryboardAssetBindings(t *testing.T) {
	doc := map[string]any{"nodes": []map[string]any{
		{"id": "hero", "type": "image", "title": "女主", "metadata": map[string]any{"status": "success"}},
		{"id": "video-1", "type": "video", "title": "参考视频", "metadata": map[string]any{"status": "success"}},
		{"id": "text-1", "type": "text", "title": "剧本", "metadata": map[string]any{"content": "x"}},
	}}
	rows := []map[string]any{{"id": "r1", "assetBindings": []any{
		map[string]any{"nodeId": "hero", "role": "character", "priority": float64(2)},
		map[string]any{"nodeId": "video-1", "role": "motion"},
	}}}
	if err := cloudAgentStoryboardBindingsInDoc(doc, rows); err != nil {
		t.Fatalf("合法绑定被拒：%v", err)
	}
	// 形状校验：role 枚举、重复、数量、字段类型
	if err := validateCloudAgentStoryboardBindings([]any{map[string]any{"nodeId": "hero", "role": "主角"}}); err == nil {
		t.Fatal("非法 role 应被拒绝")
	}
	if err := validateCloudAgentStoryboardBindings([]any{
		map[string]any{"nodeId": "hero", "role": "character"},
		map[string]any{"nodeId": "hero", "role": "prop"},
	}); err == nil {
		t.Fatal("同一行重复关联同一节点应被拒绝")
	}
	if err := validateCloudAgentStoryboardBindings("hero"); err == nil {
		t.Fatal("非数组应被拒绝")
	}
	// 归属校验：不存在的节点 / 非媒体节点
	if err := cloudAgentStoryboardBindingsInDoc(doc, []map[string]any{{"id": "r1", "assetBindings": []any{map[string]any{"nodeId": "missing", "role": "character"}}}}); err == nil {
		t.Fatal("不存在的节点应被拒绝")
	}
	if err := cloudAgentStoryboardBindingsInDoc(doc, []map[string]any{{"id": "r1", "assetBindings": []any{map[string]any{"nodeId": "text-1", "role": "character"}}}}); err == nil {
		t.Fatal("文本节点不能被关联为素材")
	}
	// schema 与描述都要暴露这个字段（否则模型看不到能力）
	schema := cloudAgentStoryboardPatchSchema()
	if _, ok := schema["properties"].(map[string]any)["assetBindings"]; !ok {
		t.Fatal("分镜 patch schema 未暴露 assetBindings")
	}
	description := ""
	writeRequest := agentTestRequest()
	writeRequest.PermissionMode = "auto"
	for _, tool := range cloudAgentTools(writeRequest) {
		function, _ := tool["function"].(map[string]any)
		if function["name"] == "canvas_edit_storyboard" {
			description = stringValue(function["description"])
		}
	}
	if !strings.Contains(description, "assetBindings") || strings.Contains(description, "不能修改素材绑定") {
		t.Fatalf("工具描述未更新：%s", description)
	}
}
