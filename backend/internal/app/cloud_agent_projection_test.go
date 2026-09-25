package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

// 本文件从 dev 的 cloud_agent_projection_test.go 恢复，只保留覆盖本次移植清单的用例：
// ④ 轮首摘要分级降级、⑤ 读工具行级分页与投影分档、② 同批写入快照重基与快照语义、
// ③ 工具回执瘦身、⑥ 缓存键口径。
//
// 未恢复的用例与原因（详见报告 UPSTREAM-PORT-GAP-CLOSURE.md 的"需要人工确认"）：
//   - TestCloudAgentLargeHistoryBelowCompactionLineIsSentIntact / TestCloudAgentContextBreakdown* /
//     TestCloudAgentContextPressure*：依赖 dev 的上下文计量与压缩实现，不在本次清单内；
//   - TestCloudAgentModelListDefersVerboseFields：依赖 dev 的 model_list verbose 参数，
//     上游（合并树）没有该参数；
//   - TestCloudAgentToolsFollowRunCapabilities：依赖 dev 按 HasMemories / read_only 裁剪
//     工具表的能力，上游的 compileCloudAgentTools 不做该裁剪；
//   - TestCloudAgentVision* / TestCloudAgentImageInspection* / TestCloudAgentKeepsImages* /
//     TestCloudAgentInspectedAsset* 等看图用例：看图链路的既有覆盖已在 port-bot 的移植提交里
//     恢复（cloud_agent_vision_test.go 等），这里不重复。

// storyboardDigestFixture builds a canvas whose storyboard nodes carry the same
// number of rows the audit measured on a real canvas.
func storyboardDigestFixture(nodes, rows int) map[string]any {
	items := make([]map[string]any, 0, nodes)
	for n := 0; n < nodes; n++ {
		rowItems := make([]any, 0, rows)
		for r := 0; r < rows; r++ {
			rowItems = append(rowItems, map[string]any{
				"id":                  cloudAgentID("user", "storyboard:node:"+string(rune('a'+n))+":"+string(rune('a'+r%26))),
				"shotNumber":          float64(r + 1),
				"durationSeconds":     3.0,
				"plotDescription":     strings.Repeat("镜头画面描述", 12),
				"videoMotionPrompt":   strings.Repeat("运镜与动作", 12),
				"imageNodeId":         "image-" + string(rune('a'+r%26)),
				"videoNodeId":         "video-" + string(rune('a'+r%26)),
				"assetBindings":       []any{map[string]any{"nodeId": "hero", "role": "character", "priority": float64(1)}},
				"characters":          []any{map[string]any{"characterName": "主角", "characterAssetId": "asset-1"}},
				"optionalDetails":     []any{"细节"},
				"mustHave":            []any{"要素"},
				"continuityOut":       "承接下一镜",
				"unrelatedPrivateKey": "do-not-expose",
			})
		}
		items = append(items, map[string]any{
			"id": "storyboard-" + string(rune('a'+n)), "type": "script", "title": "分镜脚本",
			"position": map[string]any{"x": 10.0, "y": 20.0},
			"metadata": map[string]any{"storyboard": map[string]any{"rows": rowItems}},
		})
	}
	return map[string]any{"nodes": items}
}

// The digest is rebuilt every run and resent every step, so its size must be
// bounded by construction and must never fail the run.

func TestCloudAgentDigestStaysWithinBudgetForLargeCanvas(t *testing.T) {
	for _, shape := range []struct{ nodes, rows int }{{5, 89}, {20, 40}, {80, 30}, {200, 20}} {
		doc := storyboardDigestFixture(shape.nodes, shape.rows)
		raw, _ := json.Marshal(doc)
		summary, err := cloudAgentCanvasSummary(&model.CanvasProject{PayloadJSON: string(raw), Title: "画布"})
		if err != nil {
			t.Fatalf("digest failed for %d nodes/%d rows: %v", shape.nodes, shape.rows, err)
		}
		if len(summary) > cloudAgentCanvasSummaryBudgetBytes {
			t.Fatalf("node catalog %d bytes exceeds budget %d for %d nodes/%d rows", len(summary), cloudAgentCanvasSummaryBudgetBytes, shape.nodes, shape.rows)
		}
		if strings.Contains(summary, "do-not-expose") {
			t.Fatal("digest leaked arbitrary metadata")
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(summary), &decoded); err != nil {
			t.Fatalf("digest is not JSON: %v", err)
		}
		if decoded["totalNodes"] != float64(shape.nodes) {
			t.Fatalf("digest lost totalNodes: %v", decoded["totalNodes"])
		}
		// Scale-only: the digest must not carry storyboard rows.
		nodes, _ := decoded["nodes"].([]any)
		for _, value := range nodes {
			item, _ := value.(map[string]any)
			storyboard, hasStoryboard := item["storyboard"].(map[string]any)
			if !hasStoryboard {
				continue
			}
			if _, hasRows := storyboard["rows"]; hasRows {
				t.Fatalf("digest copied storyboard rows: %+v", storyboard)
			}
			if storyboard["totalRows"] != float64(shape.rows) {
				t.Fatalf("digest lost row scale: %+v", storyboard)
			}
		}
	}
}

func TestCloudAgentDigestKeepsUnsupportedNodeVisibility(t *testing.T) {
	doc := map[string]any{"nodes": []map[string]any{{"id": "weird", "type": "unknown-type", "title": "未知节点", "metadata": map[string]any{"content": "x"}}}}
	raw, _ := json.Marshal(doc)
	summary, err := cloudAgentCanvasSummary(&model.CanvasProject{PayloadJSON: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, `"agentSupported":false`) {
		t.Fatalf("unsupported node lost its visibility marker: %s", summary)
	}
}

// Index promises existence and scale only; page is compact and carries no row

func TestCloudAgentProjectionModes(t *testing.T) {
	doc := storyboardDigestFixture(1, 12)
	storyboard := creationMaps(doc["nodes"])[0]["metadata"].(map[string]any)["storyboard"]

	index := cloudAgentStoryboardState(storyboard.(map[string]any), 0, cloudAgentProjectionIndex)
	if _, hasRows := index["rows"]; hasRows {
		t.Fatal("index projection must not copy rows")
	}
	if index["totalRows"] != 12 || index["firstShotNumber"] != "1" || index["lastShotNumber"] != "12" {
		t.Fatalf("index summary missing scale: %+v", index)
	}
	if index["readRowsWith"] == nil {
		t.Fatal("index summary must tell the model how to read rows")
	}

	page := cloudAgentStoryboardState(storyboard.(map[string]any), 0, cloudAgentProjectionPage)
	pageRows := page["rows"].([]any)
	if len(pageRows) != cloudAgentPageRows || page["nextOffset"] != cloudAgentPageRows {
		t.Fatalf("page size/pagination wrong: %d rows, nextOffset=%v", len(pageRows), page["nextOffset"])
	}
	for _, value := range pageRows {
		row := value.(map[string]any)
		if _, hasID := row["id"]; hasID {
			t.Fatal("page projection must not carry row IDs")
		}
		if _, hasBindings := row["assetBindings"]; !hasBindings {
			t.Fatal("page projection dropped a non-empty collection")
		}
		if _, hasTruncated := row["assetBindingsTruncated"]; hasTruncated {
			t.Fatal("false truncation flags must be omitted")
		}
		if row["shotNumber"] == nil {
			t.Fatal("page projection must address rows by shot number")
		}
	}
	if page["rowIdSource"] == nil {
		t.Fatal("page projection must say where row IDs come from")
	}

	detail := cloudAgentStoryboardState(storyboard.(map[string]any), 0, cloudAgentProjectionDetail)
	detailRows := detail["rows"].([]any)
	if len(detailRows) != cloudAgentDetailStoryboardRows {
		t.Fatalf("detail must return one row, got %d", len(detailRows))
	}
	if detailRows[0].(map[string]any)["id"] == nil {
		t.Fatal("detail projection must carry the row ID")
	}
	if _, hasHint := detail["rowIdSource"]; hasHint {
		t.Fatal("detail projection must not claim row IDs are missing")
	}
}

// Empty collections and false flags are what the audit measured as scaffolding:

func TestCloudAgentProjectionOmitsEmptyScaffolding(t *testing.T) {
	doc := map[string]any{"nodes": []map[string]any{{
		"id": "storyboard-1", "type": "script", "title": "分镜",
		"metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{
			"id": "shot-1", "shotNumber": float64(1), "durationSeconds": 2.0, "plotDescription": "画面",
			"assetBindings": []any{}, "characters": []any{},
		}}}},
	}}}
	// 合并树（上游版）的画布读取带 canvasID 与连线分页偏移，这里按上游签名多传一个画布 ID。
	view, err := cloudAgentCanvasState(nil, "user", "agent-canvas", doc, 0, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	row := view.(map[string]any)["nodes"].([]any)[0].(map[string]any)["storyboard"].(map[string]any)["rows"].([]any)[0].(map[string]any)
	for _, key := range []string{"assetBindings", "characters", "assetBindingsTruncated", "charactersTruncated"} {
		if _, exists := row[key]; exists {
			t.Fatalf("empty scaffolding %q still emitted: %+v", key, row)
		}
	}
}

// composerContent duplicates prompt on prepared drafts; the draft/edit

func TestCloudAgentProjectionDeduplicatesComposerContent(t *testing.T) {
	descriptor, known := cloudAgentNodeCapabilityForType("image")
	if !known {
		t.Fatal("image capability missing")
	}
	node := map[string]any{"id": "image-1", "type": "image", "title": "图"}
	same := map[string]any{"prompt": "同一个提示词", "composerContent": "同一个提示词"}
	projected, err := cloudAgentProjectNodeFields(node, same, descriptor, descriptor.SummaryFields, 2000, cloudAgentProjectionPage, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projected["composerContent"]; exists {
		t.Fatalf("duplicate composerContent was sent: %+v", projected)
	}
	different := map[string]any{"prompt": "已提交提示词", "composerContent": "用户改过的草稿"}
	projected, err = cloudAgentProjectNodeFields(node, different, descriptor, descriptor.SummaryFields, 2000, cloudAgentProjectionPage, 0)
	if err != nil {
		t.Fatal(err)
	}
	if projected["composerContent"] != "用户改过的草稿" {
		t.Fatalf("distinct draft was dropped: %+v", projected)
	}
}

// The cheap deterministic eviction must be able to fire below the semantic
// compaction threshold, and it must judge the conversation messages rather

func TestCloudAgentPromptCacheKeyIgnoresVolatileContext(t *testing.T) {
	req := agentTestRequest()
	first := cloudAgentCanonical("system v1 canvas seven nodes", nil, "开始", req)
	second := cloudAgentCanonical("system v2 canvas eight nodes edited", nil, "继续", req)
	if first.PromptCacheKey != second.PromptCacheKey {
		t.Fatal("cache key churned with volatile system content")
	}
	other := req
	other.PermissionMode = "auto"
	if cloudAgentCanonical("system v1 canvas seven nodes", nil, "开始", other).PromptCacheKey == first.PromptCacheKey {
		t.Fatal("cache key must distinguish different tool/permission configurations")
	}
}

// The approval preview is authored for the human card; the model gets a

func TestCloudAgentModelToolResultReplacesPreviewWithReceipt(t *testing.T) {
	result := map[string]any{
		"canvasId": "c1", "nodeId": "n1", "snapshotHash": "h", "summary": "创建分镜脚本《示例》",
		"preview": map[string]any{"kind": "storyboard", "title": "创建分镜脚本", "description": "创建分镜脚本《示例》", "items": []any{map[string]any{"summary": "x"}}},
	}
	modelFacing := cloudAgentModelToolResult("canvas_create_storyboard", result).(map[string]any)
	if _, hasPreview := modelFacing["preview"]; hasPreview {
		t.Fatal("preview still injected into model context")
	}
	if modelFacing["previewOmitted"] != true || modelFacing["summary"] == nil || modelFacing["nodeId"] != "n1" || modelFacing["snapshotHash"] != "h" {
		t.Fatalf("receipt lost required fields: %+v", modelFacing)
	}
	// The SSE/UI copy keeps the full preview object.
	if result["preview"] == nil {
		t.Fatal("SSE result lost the approval preview")
	}
	// Results without a preview pass through untouched.
	plain := map[string]any{"nodeId": "n1"}
	if got := cloudAgentModelToolResult("task_get", plain).(map[string]any); len(got) != 1 {
		t.Fatalf("unrelated result was rewritten: %+v", got)
	}
}

func TestCloudAgentSnapshotHashTracksContentNotBrowserBookkeeping(t *testing.T) {
	doc := map[string]any{
		"id": "canvas-1", "title": "画布", "createdAt": "2026-01-01T00:00:00Z",
		"viewport": map[string]any{"x": 1.0, "y": 2.0, "k": 0.5},
		"nodes": []any{map[string]any{
			"id": "script-1", "type": "script", "title": "分镜", "position": map[string]any{"x": 10.0, "y": 20.0},
			"width": 920.0, "height": 360.0,
			"metadata": map[string]any{"status": "idle", "storyboard": map[string]any{"rows": []any{map[string]any{"id": "row-1", "shotNumber": 1.0, "plotDescription": "镜头一"}}}},
		}},
		"connections": []any{},
	}
	base := cloudAgentCanvasHash(doc)

	// 浏览器保存：补节点时间戳、测量高度、顶层 UI 状态、视口
	bookkeeping := map[string]any{}
	for k, v := range doc {
		bookkeeping[k] = v
	}
	bookkeeping["updatedAt"] = "2026-01-01T00:00:05Z"
	bookkeeping["viewport"] = map[string]any{"x": 99.0, "y": 88.0, "k": 0.9}
	bookkeeping["showImageInfo"] = true
	bookkeeping["chatSessions"] = []any{map[string]any{"id": "chat-1"}}
	bookkeeping["activeChatId"] = "chat-1"
	nodes := []any{}
	for _, value := range doc["nodes"].([]any) {
		node := map[string]any{}
		for k, v := range value.(map[string]any) {
			node[k] = v
		}
		node["createdAt"] = "2026-01-01T00:00:00Z"
		node["updatedAt"] = "2026-01-01T00:00:05Z"
		meta := map[string]any{}
		for k, v := range node["metadata"].(map[string]any) {
			meta[k] = v
		}
		meta["storyboardComposerHeight"] = 320.0
		node["metadata"] = meta
		nodes = append(nodes, node)
	}
	bookkeeping["nodes"] = nodes
	if got := cloudAgentCanvasHash(bookkeeping); got != base {
		t.Fatalf("browser bookkeeping changed the Agent snapshot hash:\n base=%s\n got =%s", base, got)
	}

	// 内容变化必须仍然改变哈希
	content := map[string]any{}
	for k, v := range bookkeeping {
		content[k] = v
	}
	editedNodes := []any{}
	for _, value := range bookkeeping["nodes"].([]any) {
		node := map[string]any{}
		for k, v := range value.(map[string]any) {
			node[k] = v
		}
		if node["id"] == "script-1" {
			meta := map[string]any{}
			for k, v := range node["metadata"].(map[string]any) {
				meta[k] = v
			}
			meta["storyboard"] = map[string]any{"rows": []any{map[string]any{"id": "row-1", "shotNumber": 1.0, "plotDescription": "镜头一已改"}}}
			node["metadata"] = meta
		}
		editedNodes = append(editedNodes, node)
	}
	content["nodes"] = editedNodes
	if cloudAgentCanvasHash(content) == base {
		t.Fatal("edited shot content did not change the snapshot hash")
	}

	// 布局变化仍然需要重新读取（documented contract）
	moved := map[string]any{}
	for k, v := range bookkeeping {
		moved[k] = v
	}
	movedNodes := []any{}
	for _, value := range bookkeeping["nodes"].([]any) {
		node := map[string]any{}
		for k, v := range value.(map[string]any) {
			node[k] = v
		}
		node["position"] = map[string]any{"x": 500.0, "y": 600.0}
		movedNodes = append(movedNodes, node)
	}
	moved["nodes"] = movedNodes
	if cloudAgentCanvasHash(moved) == base {
		t.Fatal("node layout must still require a re-read")
	}
	if cloudAgentMediaContentHash(moved) != cloudAgentMediaContentHash(bookkeeping) {
		t.Fatal("media snapshot must keep ignoring pure layout")
	}
}

// A browser autosave between the Agent's write and its next write must not break

func TestCloudAgentApprovedWriteSurvivesBrowserAutosave(t *testing.T) {
	s, canvas := cloudAgentStoryboardFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "request_approval"
	req.IdempotencyKey = "storyboard-autosave-race"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	call := cloudAgentStoryboardCall(t, "canvas_create_storyboard", "autosave-race-call", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc),
		"nodeId":       "race-storyboard",
		"title":        "自动保存竞态分镜",
		"rows":         []map[string]any{{"durationSeconds": 3.0, "plotDescription": "镜头"}},
	})
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.ActiveTaskID = ""
	state.Calls = []cloudAgentCall{call}
	state.CallIndex = 0
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	state, _ = cloudAgentDecode(run)
	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CloudAgentRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting_approval" || waiting.Approval == nil {
		t.Fatalf("write did not enter approval: %+v", waiting)
	}
	// 浏览器在前端渲染新节点后保存画布：补时间戳、测量高度、UI 状态、平移视口
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	browserDoc, _ := creationDocument(stored.PayloadJSON)
	browserDoc["updatedAt"] = "2026-01-01T00:00:10Z"
	browserDoc["viewport"] = map[string]any{"x": 42.0, "y": 24.0, "k": 0.7}
	browserDoc["showImageInfo"] = true
	if err := saveCloudAgentDocument(s.repo, stored, browserDoc, mustRuntimePolicy(t, s)); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", "确认创建"); err != nil {
		t.Fatal(err)
	}
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	final, err := s.CloudAgentRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status == "failed" {
		t.Fatalf("browser autosave failed the approved write: %s", final.FailureMessage)
	}
	stored, _ = s.repo.CanvasProjectForUser("user", canvas.ID)
	if !strings.Contains(stored.PayloadJSON, "race-storyboard") {
		t.Fatal("approved storyboard was not written after autosave")
	}
}

// A genuine concurrent content edit is a recoverable tool failure: the model gets

func TestCloudAgentStaleSnapshotIsRecoverableToolFailure(t *testing.T) {
	s, canvas := cloudAgentStoryboardFixture(t)
	req := agentTestRequest()
	req.PermissionMode = "request_approval"
	req.IdempotencyKey = "storyboard-stale-snapshot"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	call := cloudAgentStoryboardCall(t, "canvas_create_storyboard", "stale-snapshot-call", map[string]any{
		"snapshotHash": cloudAgentCanvasHash(doc),
		"nodeId":       "stale-storyboard",
		"title":        "过期快照分镜",
		"rows":         []map[string]any{{"durationSeconds": 3.0, "plotDescription": "镜头"}},
	})
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.ActiveTaskID = ""
	state.Calls = []cloudAgentCall{call}
	state.CallIndex = 0
	if err := s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	state, _ = cloudAgentDecode(run)
	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CloudAgentRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting_approval" || waiting.Approval == nil {
		t.Fatalf("write did not enter approval: %+v", waiting)
	}
	// 真实的并发内容编辑：画布标题被改（浏览器记的账不算，内容算）
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	edited, _ := creationDocument(stored.PayloadJSON)
	edited["title"] = "并发编辑过的画布"
	if err := saveCloudAgentDocument(s.repo, stored, edited, mustRuntimePolicy(t, s)); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", "确认创建"); err != nil {
		t.Fatal(err)
	}
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.CloudAgentRun("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status == "failed" {
		t.Fatalf("stale snapshot killed the run instead of the tool call: %s", after.FailureMessage)
	}
	failed := false
	for _, event := range after.Events {
		if event.Type == "tool_failed" && strings.Contains(stringValue(event.Payload["text"]), "画布已变化") {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("stale snapshot was not reported as a recoverable tool failure: %+v", after.Events)
	}
}

// The context meter reports what occupies the request: three canonical buckets

func TestCloudAgentSameBatchWritesRebaseSnapshot(t *testing.T) {
	s, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	firstID, secondID := stringValue(rows[0]["id"]), stringValue(rows[1]["id"])
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.IdempotencyKey = "batch-write-rebase"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	seen := cloudAgentCanvasHash(doc)
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Messages: []map[string]any{}}}
	run, err := s.repo.CloudAgent("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	decoded.Request = req
	decoded.ActiveTaskID = ""
	decoded.Calls = []cloudAgentCall{
		cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "batch-write-1", map[string]any{
			"snapshotHash": seen, "nodeId": "storyboard-1", "action": "update", "rowId": firstID,
			"patch": map[string]any{"dialogue": "第一批第一处"}}),
		cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "batch-write-2", map[string]any{
			"snapshotHash": seen, "nodeId": "storyboard-1", "action": "update", "rowId": secondID,
			"patch": map[string]any{"dialogue": "第一批第二处"}}),
	}
	decoded.CallIndex = 0
	state = &decoded
	if err := s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatal(err)
	}

	advance := func() {
		t.Helper()
		current, err := s.repo.CloudAgent("user", root.ID)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := cloudAgentDecode(current)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.advanceCloudAgentTool(current, &decoded); err != nil {
			t.Fatal(err)
		}
	}
	advance() // 第一次写入：成功并产出新版本
	advance() // 第二次写入：同批，必须重基后成功

	stored, err := s.repo.CanvasProjectForUser("user", canvas.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.PayloadJSON, "第一批第一处") || !strings.Contains(stored.PayloadJSON, "第一批第二处") {
		t.Fatalf("same-batch writes did not both land: %s", stored.PayloadJSON)
	}
	final, err := s.CloudAgentRun("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range final.Events {
		if event.Type == "tool_failed" {
			t.Fatalf("same-batch write failed: %+v", event.Payload)
		}
	}
}

func TestCloudAgentBatchRebaseStillRejectsThirdPartyEdit(t *testing.T) {
	s, canvas := cloudAgentStoryboardFixture(t)
	rows := createCloudAgentStoryboardForTest(t, s, canvas)
	firstID, secondID := stringValue(rows[0]["id"]), stringValue(rows[1]["id"])
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.IdempotencyKey = "batch-write-third-party"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	seen := cloudAgentCanvasHash(doc)
	run, _ := s.repo.CloudAgent("user", root.ID)
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Request = req
	state.ActiveTaskID = ""
	state.Calls = []cloudAgentCall{
		cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "third-party-1", map[string]any{
			"snapshotHash": seen, "nodeId": "storyboard-1", "action": "update", "rowId": firstID,
			"patch": map[string]any{"dialogue": "Agent 写入"}}),
		cloudAgentStoryboardCall(t, "canvas_edit_storyboard", "third-party-2", map[string]any{
			"snapshotHash": seen, "nodeId": "storyboard-1", "action": "update", "rowId": secondID,
			"patch": map[string]any{"dialogue": "Agent 第二处"}}),
	}
	state.CallIndex = 0
	if err := s.repo.MutateCloudAgent("user", root.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	advance := func() *cloudAgentRuntime {
		t.Helper()
		current, err := s.repo.CloudAgent("user", root.ID)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := cloudAgentDecode(current)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.advanceCloudAgentTool(current, &decoded); err != nil {
			t.Fatal(err)
		}
		return &decoded
	}
	advance()
	// 第三方并发改动：直接覆盖画布（模拟浏览器保存）
	stored, _ := s.repo.CanvasProjectForUser("user", canvas.ID)
	edited, _ := creationDocument(stored.PayloadJSON)
	edited["title"] = "用户在浏览器里改过的标题"
	if err := saveCloudAgentDocument(s.repo, stored, edited, mustRuntimePolicy(t, s)); err != nil {
		t.Fatal(err)
	}
	advance()
	final, err := s.CloudAgentRun("user", root.ID)
	if err != nil {
		t.Fatal(err)
	}
	conflicted := false
	for _, event := range final.Events {
		if event.Type == "tool_failed" && strings.Contains(stringValue(event.Payload["text"]), "画布已变化") {
			conflicted = true
		}
	}
	if !conflicted {
		t.Fatal("third-party edit did not reject the batched write")
	}
}

// 写入回执必须给出结果口径：模型曾经因为读到"批准后才会写入画布"而以为还在等审批，

func TestCloudAgentWriteReceiptStatesApplied(t *testing.T) {
	result := map[string]any{
		"canvasId": "c1", "nodeId": "script-1", "snapshotHash": "after", "beforeSnapshotHash": "before",
		"summary": "Agent 准备修改分镜脚本。批准后才会写入画布。",
		"preview": cloudAgentApprovalPreview{Kind: "canvas_mutation", Title: "确认修改分镜脚本",
			Description: "Agent 准备修改分镜脚本。批准后才会写入画布。",
			Items:       []cloudAgentApprovalPreviewItem{{Operation: "edit_storyboard", NodeID: "script-1", Summary: "修改分镜脚本《示例》"}}},
	}
	receipt := cloudAgentModelToolResult("canvas_edit_storyboard", result).(map[string]any)
	if receipt["applied"] != true || stringValue(receipt["outcome"]) == "" {
		t.Fatalf("write receipt does not state the outcome: %+v", receipt)
	}
	if strings.Contains(stringValue(receipt["summary"]), "批准后才会写入画布") {
		t.Fatalf("write receipt still carries the approval wording: %+v", receipt)
	}
	if receipt["summary"] != "修改分镜脚本《示例》" {
		t.Fatalf("write receipt should keep the item summary: %+v", receipt)
	}
	if receipt["snapshotHash"] != "after" || receipt["beforeSnapshotHash"] != "before" {
		t.Fatalf("write receipt lost the version chain: %+v", receipt)
	}
	// 非画布写入（例如 generate_media 的草稿）不得声称已写入画布
	media := map[string]any{"nodeId": "n1", "summary": "已创建草稿", "preview": cloudAgentApprovalPreview{Kind: "media"}}
	mediaReceipt := cloudAgentModelToolResult("generate_media", media).(map[string]any)
	if mediaReceipt["applied"] != nil {
		t.Fatalf("media receipt must keep its own semantics: %+v", mediaReceipt)
	}
}

func TestCloudAgentStoryboardReadRows(t *testing.T) {
	storyboard := map[string]any{"rows": []any{}}
	rowList := []any{}
	for i := 1; i <= 12; i++ {
		rowList = append(rowList, map[string]any{
			"id": "row-" + string(rune('a'+i-1)), "shotNumber": float64(i), "durationSeconds": 3.0,
			"plotDescription": strings.Repeat("镜头描述", 400),
		})
	}
	storyboard["rows"] = rowList

	page := cloudAgentStoryboardState(storyboard, 0, cloudAgentProjectionDetail, 5)
	pageRows := page["rows"].([]any)
	if len(pageRows) != 5 || page["nextOffset"] != 5 {
		t.Fatalf("rows=5 must return five rows and paginate: %d rows, next=%v", len(pageRows), page["nextOffset"])
	}
	for _, value := range pageRows {
		if stringValue(value.(map[string]any)["id"]) == "" {
			t.Fatal("multi-row read must keep rowId for editing")
		}
	}
	precise := cloudAgentStoryboardState(storyboard, 0, cloudAgentProjectionDetail, 1)
	if len(precise["rows"].([]any)) != 1 {
		t.Fatal("rows=1 must stay single-row precise read")
	}
	// 超长字段：通读按 2000 字符截断并标记，精读保留 16000 字符
	longRow := map[string]any{"rows": []any{map[string]any{
		"id": "row-long", "shotNumber": float64(1), "durationSeconds": 3.0,
		"plotDescription": strings.Repeat("长", 20000),
	}}}
	multi := cloudAgentStoryboardState(longRow, 0, cloudAgentProjectionDetail, 5)["rows"].([]any)[0].(map[string]any)
	// truncateRunes 会追回 "..."，所以上限是 limit+3
	got := len([]rune(stringValue(multi["plotDescription"])))
	if got != cloudAgentMultiRowTextLimit+3 || multi["plotDescriptionTruncated"] != true {
		t.Fatalf("multi-row read must cap field text at %d(+3): %d runes", cloudAgentMultiRowTextLimit, got)
	}
	single := cloudAgentStoryboardState(longRow, 0, cloudAgentProjectionDetail, 1)["rows"].([]any)[0].(map[string]any)
	if got := len([]rune(stringValue(single["plotDescription"]))); got != cloudAgentDetailTextLimit+3 {
		t.Fatalf("precise single-row read must keep up to %d(+3) runes, got %d", cloudAgentDetailTextLimit, got)
	}
}

func TestCloudAgentReadBodiesAreNotPruned(t *testing.T) {
	body := `{"skillId":"s1","path":"SKILL.md","content":"` + strings.Repeat("技能正文", 4000) + `"}`
	request := canonicalAgentRequest{Messages: []map[string]any{
		{"role": "user", "content": "用技能改分镜"},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call-skill", "type": "function", "function": map[string]any{"name": "skill_read_file", "arguments": `{"skillId":"s1","path":"SKILL.md"}`}}}},
		{"role": "tool", "tool_call_id": "call-skill", "content": body},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call-read", "type": "function", "function": map[string]any{"name": "canvas_get_state", "arguments": `{}`}}}},
		{"role": "tool", "tool_call_id": "call-read", "content": `{"nodes":[],"content":"` + strings.Repeat("画布正文", 4000) + `"}`},
		{"role": "assistant", "content": "继续"},
	}}
	before, _ := json.Marshal(request.Messages)
	if changed, pruned := cloudAgentPruneInspectedImages(&request, nil); changed || pruned != 0 {
		t.Fatalf("read bodies must not be pruned: changed=%v pruned=%d", changed, pruned)
	}
	after, _ := json.Marshal(request.Messages)
	if string(before) != string(after) {
		t.Fatal("read bodies were rewritten")
	}
	if !strings.Contains(stringField(request.Messages[2], "content"), "技能正文") {
		t.Fatal("skill body disappeared")
	}
	if !strings.Contains(stringField(request.Messages[4], "content"), "画布正文") {
		t.Fatal("canvas read body disappeared")
	}
}

func compactJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
