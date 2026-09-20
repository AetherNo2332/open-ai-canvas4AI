package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/canvas/layout"
	"infinite-canvas/backend/internal/model"
)

// layoutFixture 造一张用于整理测试的画布：文本 / 图片 / 视频三类节点挤在同一个角落。
func layoutFixture(t *testing.T, nodes []map[string]any) (*Service, map[string]any) {
	t.Helper()
	s, db, _, _ := creationTestService(t)
	doc := map[string]any{"nodes": nodes, "connections": []any{}}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasProject{ID: "layout-canvas", UserID: "user", PayloadJSON: string(raw)}).Error; err != nil {
		t.Fatal(err)
	}
	return s, doc
}

func layoutNode(id, nodeType string, x, y float64, metadata ...map[string]any) map[string]any {
	meta := map[string]any{"content": "内容-" + id}
	if len(metadata) > 0 {
		for key, value := range metadata[0] {
			meta[key] = value
		}
	}
	return map[string]any{
		"id": id, "type": nodeType, "title": "节点-" + id,
		"position": map[string]any{"x": x, "y": y},
		"width":    300.0, "height": 200.0,
		"metadata": meta,
	}
}

func arrangeCall(t *testing.T, args map[string]any) cloudAgentCall {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: "arrange-1"}
	call.Function.Name = "canvas_arrange_nodes"
	call.Function.Arguments = string(raw)
	return call
}

func canvasPositions(t *testing.T, s *Service) map[string]layout.Position {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", "layout-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	positions := map[string]layout.Position{}
	for _, node := range creationMaps(doc["nodes"]) {
		x, _ := cloudAgentSafeNumber(node["position"].(map[string]any)["x"])
		y, _ := cloudAgentSafeNumber(node["position"].(map[string]any)["y"])
		xf, _ := x.(float64)
		yf, _ := y.(float64)
		positions[stringValue(node["id"])] = layout.Position{X: xf, Y: yf}
	}
	return positions
}

func TestCloudAgentArrangeNodesLaysOutByLane(t *testing.T) {
	nodes := []map[string]any{
		layoutNode("text-1", "text", 0, 0),
		layoutNode("text-2", "text", 10, 10),
		layoutNode("image-1", "image", 20, 20),
		layoutNode("video-1", "video", 30, 30),
	}
	s, doc := layoutFixture(t, nodes)
	policy := mustRuntimePolicy(t, s)

	result, err := applyCloudAgentArrangeNodes(s.repo, "user", "layout-canvas", arrangeCall(t, map[string]any{
		"snapshotHash": creationHash(doc),
		"mode":         "byType",
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := result.(map[string]any)
	// 布局以当前左上角为锚点：已经在锚点上的 text-1 不需要移动，movement 只统计真正变动的节点。
	if moved, _ := receipt["moved"].(int); moved < 3 {
		t.Fatalf("整理节点数 = %v，期望至少 3：%+v", receipt["moved"], receipt)
	}

	positions := canvasPositions(t, s)
	if positions["text-1"].Y >= positions["image-1"].Y || positions["image-1"].Y >= positions["video-1"].Y {
		t.Fatalf("未按媒体泳道分区：%+v", positions)
	}
	if positions["text-1"].X != positions["image-1"].X {
		t.Fatalf("各泳道应左对齐：%+v", positions)
	}
	applied := []layout.Node{
		{ID: "text-1", X: positions["text-1"].X, Y: positions["text-1"].Y, Width: 300, Height: 200},
		{ID: "text-2", X: positions["text-2"].X, Y: positions["text-2"].Y, Width: 300, Height: 200},
		{ID: "image-1", X: positions["image-1"].X, Y: positions["image-1"].Y, Width: 300, Height: 200},
		{ID: "video-1", X: positions["video-1"].X, Y: positions["video-1"].Y, Width: 300, Height: 200},
	}
	if pairs := layout.Overlaps(applied, 0); len(pairs) != 0 {
		t.Fatalf("整理后仍有重叠：%+v", pairs)
	}
	if hash, _ := receipt["snapshotHash"].(string); hash == "" || hash == receipt["beforeSnapshotHash"] {
		t.Fatalf("整理后快照应变化：%+v", receipt)
	}
}

func TestCloudAgentArrangeNodesKeepsLockedAndContainers(t *testing.T) {
	nodes := []map[string]any{
		layoutNode("text-1", "text", 0, 0),
		layoutNode("text-2", "text", 5, 5),
		layoutNode("locked-1", "text", 400, 400, map[string]any{"locked": true}),
		layoutNode("frame-1", "frame", 800, 800),
	}
	s, doc := layoutFixture(t, nodes)
	policy := mustRuntimePolicy(t, s)

	result, err := applyCloudAgentArrangeNodes(s.repo, "user", "layout-canvas", arrangeCall(t, map[string]any{
		"snapshotHash": creationHash(doc),
		"mode":         "byType",
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	positions := canvasPositions(t, s)
	if positions["locked-1"] != (layout.Position{X: 400, Y: 400}) {
		t.Fatalf("锁定节点被移动：%+v", positions["locked-1"])
	}
	if positions["frame-1"] != (layout.Position{X: 800, Y: 800}) {
		t.Fatalf("背板被移动：%+v", positions["frame-1"])
	}
	if positions["text-2"].Y == 5 && positions["text-2"].X == 5 {
		t.Fatalf("可整理节点没有移动：%+v", positions)
	}
	_ = result
}

func TestCloudAgentArrangeNodesGroupsMakeBands(t *testing.T) {
	nodes := []map[string]any{
		layoutNode("a-1", "image", 0, 0),
		layoutNode("a-2", "image", 10, 10),
		layoutNode("b-1", "video", 20, 20),
		layoutNode("b-2", "video", 30, 30),
		layoutNode("left-out", "text", 900, 900),
	}
	s, doc := layoutFixture(t, nodes)
	policy := mustRuntimePolicy(t, s)

	result, err := applyCloudAgentArrangeNodes(s.repo, "user", "layout-canvas", arrangeCall(t, map[string]any{
		"snapshotHash": creationHash(doc),
		"groups": []any{
			map[string]any{"label": "A 场景", "nodeIds": []any{"a-1", "a-2"}, "mode": "grid"},
			map[string]any{"label": "B 场景", "nodeIds": []any{"b-1", "b-2"}, "mode": "grid"},
		},
		"gap": 120,
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := result.(map[string]any)
	groups, _ := receipt["groups"].([]string)
	if len(groups) != 2 || groups[0] != "A 场景" {
		t.Fatalf("分组信息缺失：%+v", receipt["groups"])
	}
	positions := canvasPositions(t, s)
	if positions["b-1"].Y <= positions["a-1"].Y {
		t.Fatalf("两组未分带：a=%+v b=%+v", positions["a-1"], positions["b-1"])
	}
	if positions["left-out"] != (layout.Position{X: 900, Y: 900}) {
		t.Fatalf("未参与分组的节点被移动：%+v", positions["left-out"])
	}
}

func TestCloudAgentArrangeNodesDryRunDoesNotWrite(t *testing.T) {
	nodes := []map[string]any{layoutNode("text-1", "text", 0, 0), layoutNode("text-2", "text", 5, 5)}
	s, doc := layoutFixture(t, nodes)
	policy := mustRuntimePolicy(t, s)

	result, err := applyCloudAgentArrangeNodes(s.repo, "user", "layout-canvas", arrangeCall(t, map[string]any{
		"snapshotHash": creationHash(doc),
		"mode":         "row",
		"dryRun":       true,
	}), policy)
	if err != nil {
		t.Fatal(err)
	}
	receipt, _ := result.(map[string]any)
	if wouldMove, _ := receipt["wouldMove"].(int); wouldMove < 1 {
		t.Fatalf("预演结果 = %+v", receipt)
	}
	if moved, _ := receipt["moved"].(int); moved != 0 {
		t.Fatalf("预演不应写入：%+v", receipt)
	}
	positions := canvasPositions(t, s)
	if positions["text-2"] != (layout.Position{X: 5, Y: 5}) {
		t.Fatalf("预演不应写入画布：%+v", positions)
	}
}

func TestCloudAgentArrangeNodesRejectsStaleSnapshotAndTooManyNodes(t *testing.T) {
	nodes := []map[string]any{layoutNode("text-1", "text", 0, 0), layoutNode("text-2", "text", 5, 5)}
	s, _ := layoutFixture(t, nodes)
	policy := mustRuntimePolicy(t, s)

	if _, err := applyCloudAgentArrangeNodes(s.repo, "user", "layout-canvas", arrangeCall(t, map[string]any{
		"snapshotHash": "stale-hash",
		"mode":         "row",
	}), policy); err == nil || !cloudAgentSnapshotConflict(err) {
		t.Fatalf("陈旧快照应被拒绝：%v", err)
	}

	many := make([]map[string]any, 0, cloudAgentArrangeMaxNodes+1)
	for index := 0; index <= cloudAgentArrangeMaxNodes; index++ {
		many = append(many, layoutNode(fmt.Sprintf("text-%d", index), "text", float64(index), 0))
	}
	s2, doc2 := layoutFixture(t, many)
	if _, err := applyCloudAgentArrangeNodes(s2.repo, "user", "layout-canvas", arrangeCall(t, map[string]any{
		"snapshotHash": creationHash(doc2),
		"mode":         "row",
	}), mustRuntimePolicy(t, s2)); err == nil || !strings.Contains(err.Error(), "最多整理") {
		t.Fatalf("超限应被拒绝：%v", err)
	}
}

// 已移除：TestCloudAgentAddedNodeWithoutCoordinatesLandsOrdered。
//
// 它断言的是"add_node 不带坐标时由服务端按泳道自动落位（且作为生成输入时排到目标左侧）"，
// 而这段自动排布逻辑在我们这边是写在 applyCloudAgentCanvasPlan / prepareCloudAgentCanvasMutation 里的，
// 本轮云端 Agent 主链路整体取上游后它随上游实现一起消失（上游的 agentCanvasOp.X/Y 是 float64，
// 不带"未指定坐标"的语义，默认落在 (0,0)）。
//
// 也就是说：下面的 TestCloudAgentArrangeNodes* 覆盖的是**布局工具本身**（我们保留的
// cloud_agent_layout.go + internal/canvas/layout），而"新增节点自动排布"属于**待移植项**——
// 移植时还须恢复 agentCanvasOp.X/Y 的指针语义，否则"不传坐标"会被当成"坐标 0"。
// 详见报告"后续需要逐个移植的我方功能清单"的布局工具一行。

func TestCloudAgentUpdateNodePositionPatch(t *testing.T) {
	nodes := []map[string]any{layoutNode("text-1", "text", 0, 0)}
	_, doc := layoutFixture(t, nodes)

	ops := []agentCanvasOp{{Type: "update_node", ID: "text-1", Patch: map[string]any{"x": 480.0, "y": 260.0}}}
	if _, err := applyCloudAgentCanvasPlan(doc, ops); err != nil {
		t.Fatal(err)
	}
	if got := positionOf(t, nodeByID(t, doc, "text-1")); got != (layout.Position{X: 480, Y: 260}) {
		t.Fatalf("坐标未写入：%+v", got)
	}

	ops = []agentCanvasOp{{Type: "update_node", ID: "text-1", Patch: map[string]any{"x": 1e9}}}
	if _, err := applyCloudAgentCanvasPlan(doc, ops); err == nil {
		t.Fatal("越界坐标应被拒绝")
	}
}

func nodeByID(t *testing.T, doc map[string]any, id string) map[string]any {
	t.Helper()
	for _, node := range creationMaps(doc["nodes"]) {
		if stringValue(node["id"]) == id {
			return node
		}
	}
	t.Fatalf("节点 %s 不存在", id)
	return nil
}

func positionOf(t *testing.T, node map[string]any) layout.Position {
	t.Helper()
	position, _ := node["position"].(map[string]any)
	x, _ := cloudAgentSafeNumber(position["x"])
	y, _ := cloudAgentSafeNumber(position["y"])
	xf, _ := x.(float64)
	yf, _ := y.(float64)
	return layout.Position{X: xf, Y: yf}
}

// mustRuntimePolicy 是从我们这边沿用下来的测试助手（原先定义在 cloud_agent_projection_test.go，
// 该文件随自研投影/正文治理一起本轮未移植），这里保留最小实现，供布局用例读取运行时策略。
func mustRuntimePolicy(t *testing.T, s *Service) RuntimePolicySetting {
	t.Helper()
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	return policy
}
