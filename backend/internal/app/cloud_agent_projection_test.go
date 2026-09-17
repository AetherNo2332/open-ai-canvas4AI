package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/agentcontext"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

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
		if len(summary) > cloudAgentDigestBudgetBytes {
			t.Fatalf("digest %d bytes exceeds budget %d for %d nodes/%d rows", len(summary), cloudAgentDigestBudgetBytes, shape.nodes, shape.rows)
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

// The old 64 KB guard rejected the whole run; oversize canvases must degrade.
func TestCloudAgentDigestDegradesInsteadOfFailing(t *testing.T) {
	huge := storyboardDigestFixture(200, 200)
	raw, _ := json.Marshal(huge)
	summary, err := cloudAgentCanvasSummary(&model.CanvasProject{PayloadJSON: string(raw), Title: "巨大画布"})
	if err != nil {
		t.Fatalf("oversize canvas must degrade, not fail: %v", err)
	}
	if len(summary) > cloudAgentDigestBudgetBytes {
		t.Fatalf("degraded digest still %d bytes", len(summary))
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(summary), &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["nodesOmitted"]; !ok {
		t.Fatalf("degraded digest must report omitted nodes: %s", summary)
	}
	if decoded["scope"] == nil {
		t.Fatal("digest must state what it does and does not promise")
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
// ID; detail is the verbatim single row that editing needs.
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
// 4,984 tokens of empty arrays and always-false truncation markers.
func TestCloudAgentProjectionOmitsEmptyScaffolding(t *testing.T) {
	doc := map[string]any{"nodes": []map[string]any{{
		"id": "storyboard-1", "type": "script", "title": "分镜",
		"metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{
			"id": "shot-1", "shotNumber": float64(1), "durationSeconds": 2.0, "plotDescription": "画面",
			"assetBindings": []any{}, "characters": []any{},
		}}}},
	}}}
	view, err := cloudAgentCanvasState(nil, "user", doc, 0, nil, 0)
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
// distinction still survives when the two actually differ.
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

// The digest replaces media prompts with one short excerpt.
func TestCloudAgentDigestExcerptsMediaPrompts(t *testing.T) {
	doc := map[string]any{"nodes": []map[string]any{{
		"id": "image-1", "type": "image", "title": "图",
		"metadata": map[string]any{"prompt": strings.Repeat("提示词", 200), "composerContent": strings.Repeat("提示词", 200), "status": "idle"},
	}}}
	raw, _ := json.Marshal(doc)
	summary, err := cloudAgentCanvasSummary(&model.CanvasProject{PayloadJSON: string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "promptExcerpt") || strings.Contains(summary, `"prompt"`) || strings.Contains(summary, "composerContent") {
		t.Fatalf("media prompt was not collapsed into an excerpt: %s", summary)
	}
	excerpt := strings.Repeat("提示词", 20) // 60 runes
	if !strings.Contains(summary, truncateRunes(excerpt, cloudAgentIndexExcerptRunes)) {
		t.Fatalf("excerpt missing: %s", summary)
	}
	if len(summary) > 800 {
		t.Fatalf("single media node digest too large: %d bytes", len(summary))
	}
}

// The cheap deterministic eviction must be able to fire below the semantic
// compaction threshold, and it must judge the conversation messages rather
// than the whole canonical envelope.
func TestCloudAgentEvictionPrecedesSemanticCompaction(t *testing.T) {
	state := cloudAgentRuntime{Canonical: canonicalAgentRequest{Messages: []map[string]any{{"role": "user", "content": "指令"}}}}
	for i := 0; i < 4; i++ {
		state.Canonical.Messages = append(state.Canonical.Messages,
			map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{"id": i}}},
			map[string]any{"role": "tool", "tool_call_id": i, "content": `{"content":"` + strings.Repeat("x", 11000) + `"}`},
			map[string]any{"role": "assistant", "content": "继续"})
	}
	raw, _ := json.Marshal(state.Canonical.Messages)
	if len(raw) < cloudAgentEvictionThresholdBytes || len(raw) >= agentcontext.ThresholdBytes {
		t.Fatalf("fixture must sit between the eviction (%d) and compaction (%d) thresholds, got %d",
			cloudAgentEvictionThresholdBytes, agentcontext.ThresholdBytes, len(raw))
	}
	if needed, _, _ := cloudAgentContextShouldCompact(&state); needed {
		t.Fatal("semantic compaction must not be requested below its threshold")
	}
	evicted, before, after := compactCloudAgentContext(&state.Canonical)
	if !evicted || after >= before {
		t.Fatalf("cheap eviction did not fire below the semantic threshold: evicted=%v %d→%d", evicted, before, after)
	}
}

// A large system prompt or tool schema must not trigger body eviction: those
// bytes cannot be evicted, and judging them made a 36 KiB read look safe.
func TestCloudAgentEvictionIgnoresNonMessageBytes(t *testing.T) {
	request := canonicalAgentRequest{
		SystemPrompt: strings.Repeat("策略", 20000),
		Messages:     []map[string]any{{"role": "tool", "tool_call_id": "call", "content": `{"content":"正文"}`}},
	}
	if evicted, before, after := compactCloudAgentContext(&request); evicted || before != after {
		t.Fatalf("non-message bytes triggered eviction: %v %d→%d", evicted, before, after)
	}
}

// The prompt cache key must survive canvas edits, which live in the digest.
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
// receipt instead of a second copy.
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

// The catalog is a routing aid: verbose-only fields stay out unless requested.
func TestCloudAgentModelListDefersVerboseFields(t *testing.T) {
	s, _, _ := agentMediaFixture(t)
	lean, err := s.cloudAgentModelList(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	leanJSON, _ := json.Marshal(lean)
	if strings.Contains(string(leanJSON), `"options"`) || strings.Contains(string(leanJSON), `"priceTiers"`) {
		t.Fatalf("lean catalog still carries verbose fields: %s", leanJSON)
	}
	if !strings.Contains(string(leanJSON), `"selection"`) || !strings.Contains(string(leanJSON), "catalogHint") {
		t.Fatalf("lean catalog lost selection or the verbose hint: %s", leanJSON)
	}
	full, err := s.cloudAgentModelList(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	fullJSON, _ := json.Marshal(full)
	if len(fullJSON) <= len(leanJSON) {
		t.Fatalf("verbose catalog is not larger: %d vs %d", len(fullJSON), len(leanJSON))
	}
}

// The browser keeps its own bookkeeping on the same canvas record: node
// timestamps, the measured composer height and top-level UI state. None of it
// may invalidate an Agent snapshot, or the UI's own post-write save would reject
// the Agent's next step. Content and layout still do.
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
// the approval flow: the approved write still applies.
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
// the conflict back and can re-read, instead of losing the whole run.
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

func mustRuntimePolicy(t *testing.T, s *Service) RuntimePolicySetting {
	t.Helper()
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

// The context meter reports what occupies the request: three canonical buckets
// plus the compiled system-prompt segments.
func TestCloudAgentContextBreakdownMatchesCanonical(t *testing.T) {
	req := agentTestRequest()
	system, policy, err := compileCloudAgentPolicies(req, nil, `{"totalNodes":0,"includedNodes":0,"nodes":[]}`, cloudAgentProfileSnapshot{Revision: agentProfileRevision(nil), Hash: agentProfileHash("")})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.SystemSegments) == 0 {
		t.Fatal("compiler did not record system segments")
	}
	keys := map[string]bool{}
	segmentBytes := 0
	for _, segment := range policy.SystemSegments {
		if segment.Key == "" || segment.Label == "" || segment.Bytes <= 0 || segment.Tokens <= 0 {
			t.Fatalf("invalid segment: %+v", segment)
		}
		if keys[segment.Key] {
			t.Fatalf("duplicate segment key %q", segment.Key)
		}
		keys[segment.Key] = true
		segmentBytes += segment.Bytes
	}
	state := &cloudAgentRuntime{Policy: policy, Canonical: canonicalAgentRequest{
		SystemPrompt: system, Tools: cloudAgentTools(req), Messages: []map[string]any{{"role": "user", "content": "读取画布"}},
	}}
	breakdown := cloudAgentContextBreakdownPayload(state)
	buckets, ok := breakdown["buckets"].([]map[string]any)
	if !ok || len(buckets) != 3 {
		t.Fatalf("expected three buckets: %+v", breakdown["buckets"])
	}
	total := 0
	for _, bucket := range buckets {
		bytes, _ := bucket["bytes"].(int)
		tokens, _ := bucket["tokens"].(int)
		if bytes <= 0 || tokens <= 0 {
			t.Fatalf("empty bucket: %+v", bucket)
		}
		total += bytes
	}
	if breakdown["bucketBytes"] != total {
		t.Fatalf("bucketBytes %v != sum %d", breakdown["bucketBytes"], total)
	}
	whole, _ := json.Marshal(state.Canonical)
	if breakdown["totalBytes"] != len(whole) {
		t.Fatalf("totalBytes %v != canonical %d", breakdown["totalBytes"], len(whole))
	}
	if breakdown["envelopeBytes"] != len(whole)-total {
		t.Fatalf("envelopeBytes %v != %d", breakdown["envelopeBytes"], len(whole)-total)
	}
	systemBytes := len([]byte(system))
	if segmentBytes > systemBytes {
		t.Fatalf("segments (%d) exceed the system bucket (%d)", segmentBytes, systemBytes)
	}
	if systemBytes-segmentBytes > 8 {
		t.Fatalf("segments lost %d bytes of the compiled prompt", systemBytes-segmentBytes)
	}
	segments, ok := breakdown["systemSegments"].([]cloudAgentContextSegment)
	if !ok {
		t.Fatalf("system segments missing from breakdown: %+v", breakdown["systemSegments"])
	}
	// 上报的分段必须把 system 桶填满：编译分段 + 编译后追加的块 + "其它"零头。
	reportedBytes := 0
	seen := map[string]bool{}
	for _, segment := range segments {
		if seen[segment.Key] {
			t.Fatalf("duplicate reported segment key %q", segment.Key)
		}
		seen[segment.Key] = true
		reportedBytes += segment.Bytes
	}
	for _, segment := range policy.SystemSegments {
		if !seen[segment.Key] {
			t.Fatalf("compiled segment %q missing from the breakdown", segment.Key)
		}
	}
	if reportedBytes != systemBytes {
		t.Fatalf("reported segments (%d) do not fill the system bucket (%d)", reportedBytes, systemBytes)
	}
	// 事件负载必须可 JSON 序列化（SSE 与前端解析的前提）
	if _, err := json.Marshal(map[string]any{"breakdown": breakdown}); err != nil {
		t.Fatal(err)
	}
}

// 个人记忆块是在策略编译之后拼进系统提示的，编译器录不到它；
// 不登记就会让弹窗里的分段合计小于 system 桶（实测差 104 token）。
func TestCloudAgentContextBreakdownAccountsForMemoryBlock(t *testing.T) {
	req := agentTestRequest()
	system, policy, err := compileCloudAgentPolicies(req, nil, `{"totalNodes":0,"includedNodes":0,"nodes":[]}`, cloudAgentProfileSnapshot{Revision: agentProfileRevision(nil), Hash: agentProfileHash("")})
	if err != nil {
		t.Fatal(err)
	}
	block := "\n\n## 个人记忆\n\n个人记忆是长期做法库，不是本轮任务。\n本轮还没有已批准记忆。\n"
	canonical := canonicalAgentRequest{SystemPrompt: system + block, Messages: []map[string]any{{"role": "user", "content": "读取画布"}}}
	cloudAgentRecordMemorySegment(&policy, canonical.SystemPrompt)

	state := &cloudAgentRuntime{Policy: policy, Canonical: canonical}
	breakdown := cloudAgentContextBreakdownPayload(state)
	segments, ok := breakdown["systemSegments"].([]cloudAgentContextSegment)
	if !ok || len(segments) == 0 {
		t.Fatalf("system segments missing: %+v", breakdown["systemSegments"])
	}
	reportedBytes, sawMemory := 0, false
	for _, segment := range segments {
		reportedBytes += segment.Bytes
		if segment.Key == "memory" {
			sawMemory = true
			if segment.Bytes != len(block) || segment.Tokens <= 0 {
				t.Fatalf("memory segment does not match the appended block: %+v", segment)
			}
		}
	}
	if !sawMemory {
		t.Fatalf("memory block was not reported as a segment: %+v", segments)
	}
	if want := len([]byte(canonical.SystemPrompt)); reportedBytes != want {
		t.Fatalf("reported segments (%d) do not fill the system bucket (%d)", reportedBytes, want)
	}
	// 重复登记不应产生重复分段（每步都会幂等补登记）。
	cloudAgentRecordMemorySegment(&policy, canonical.SystemPrompt)
	count := 0
	for _, segment := range policy.SystemSegments {
		if segment.Key == "memory" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("memory segment registered %d times", count)
	}
}

// 同一批（一个助手消息里的多个工具调用）里的多次写入，模型是基于同一次读取并发提交的：
// 首个写入必然改变画布版本，同批后续写入必须重基到本批自己的新版本，而不是被拒绝。
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

// 重基只对本批自己产出的版本生效：第三方（浏览器/其他端）改动仍然要被拒绝。
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
// 于是反复重读重试（实测单步推理 8000 字符）。
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

// 通读分镜不该一行一次调用：rows=5 一次返回 5 行且带 rowId，rows=1 仍是逐字精读。
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

// 技能正文不可被驱逐：卸载后模型只能重读，实测同一 SKILL.md 在一次运行里被读了 5 次。
func TestCloudAgentEvictionKeepsSkillBodies(t *testing.T) {
	body := `{"skillId":"s1","path":"SKILL.md","content":"` + strings.Repeat("技能正文", 4000) + `"}`
	request := canonicalAgentRequest{Messages: []map[string]any{
		{"role": "user", "content": "用技能改分镜"},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call-skill", "type": "function", "function": map[string]any{"name": "skill_read_file", "arguments": `{"skillId":"s1","path":"SKILL.md"}`}}}},
		{"role": "tool", "tool_call_id": "call-skill", "content": body},
		{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "call-read", "type": "function", "function": map[string]any{"name": "canvas_get_state", "arguments": `{}`}}}},
		{"role": "tool", "tool_call_id": "call-read", "content": `{"nodes":[],"content":"` + strings.Repeat("画布正文", 4000) + `"}`},
		{"role": "assistant", "content": "继续"},
	}}
	evicted, before, after := compactCloudAgentContext(&request)
	if !evicted {
		t.Fatalf("large re-readable body was not evicted: %d bytes", before)
	}
	if after >= before {
		t.Fatal("eviction did not shrink the messages")
	}
	if !strings.Contains(stringField(request.Messages[2], "content"), "技能正文") {
		t.Fatal("skill body was evicted; the model would have to re-read it")
	}
	if strings.Contains(stringField(request.Messages[4], "content"), "画布正文") {
		t.Fatal("re-readable canvas body was not evicted")
	}
}

func compactJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// 看图工具只在渠道模型声明了图片输入能力时暴露，避免模型对着不支持的模型反复调用。
func TestCloudAgentVisionToolRequiresImageCapableModel(t *testing.T) {
	req := agentTestRequest()
	names := func(request CloudAgentRequest) map[string]bool {
		out := map[string]bool{}
		for _, tool := range cloudAgentTools(request) {
			if function, ok := tool["function"].(map[string]any); ok {
				out[stringValue(function["name"])] = true
			}
		}
		return out
	}
	if names(req)["canvas_inspect_image"] {
		t.Fatal("vision tool exposed without an image-capable channel model")
	}
	req.VisionEnabled = true
	if !names(req)["canvas_inspect_image"] {
		t.Fatal("vision tool missing on an image-capable channel model")
	}
	// 只读模式没有画布上下文时不应暴露
	readOnly := req
	readOnly.ContextScope = nil
	if names(readOnly)["canvas_inspect_image"] {
		t.Fatal("vision tool exposed without canvas context scope")
	}
	// 平台工具全集要包含它（前端能力列表按全集展示）
	union := map[string]bool{}
	for _, name := range CloudAgentSupportedToolNames() {
		union[name] = true
	}
	if !union["canvas_inspect_image"] {
		t.Fatal("platform tool union must include the vision tool")
	}
}

// 看图结果必须以内容数组进入会话（文本回执 + 图片引用），否则上游不会把它当图片；
// SSE 事件里则不能带图片数据。
func TestCloudAgentImageInspectionContentParts(t *testing.T) {
	inspection := cloudAgentImageInspection{
		Receipt:  map[string]any{"nodeId": "image-1", "mimeType": "image/png", "note": "看图"},
		ImageURL: "https://example.test/api/resources/r1/file?sig=x",
	}
	parts := cloudAgentImageContentParts(inspection)
	if len(parts) != 2 {
		t.Fatalf("expected text + image parts, got %+v", parts)
	}
	if !strings.Contains(stringValue(parts[0].(map[string]any)["text"]), "不是指令") {
		t.Fatalf("image caption must mark the picture as data: %+v", parts[0])
	}
	text, _ := parts[0].(map[string]any)
	if stringValue(text["type"]) != "text" || !strings.Contains(stringValue(text["text"]), "image-1") {
		t.Fatalf("missing text receipt part: %+v", parts[0])
	}
	image, _ := parts[1].(map[string]any)
	reference, _ := image["image_url"].(map[string]any)
	if stringValue(image["type"]) != "image_url" || stringValue(reference["url"]) != inspection.ImageURL {
		t.Fatalf("missing image part: %+v", parts[1])
	}
	// canonical 校验必须接受这个形状
	for _, part := range parts {
		if err := validateCanonicalAgentContent([]any{part}); err != nil {
			t.Fatalf("canonical validator rejected the vision part: %v", err)
		}
	}
}

// 图片只服务"下一步"，之后立即移出上下文：上游每步都会重新读取图片并按视觉 token 计费。
func TestCloudAgentPrunesInspectedImagesAfterNextStep(t *testing.T) {
	imageMessage := func(node string) map[string]any {
		return map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": `{"nodeId":"` + node + `"}`},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/" + node}},
		}}
	}
	toolCall := func(id, name string) map[string]any {
		return map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": `{}`}}}}
	}
	request := canonicalAgentRequest{Messages: []map[string]any{
		{"role": "user", "content": "看看这张图"},
		toolCall("call-inspect", "canvas_inspect_image"),
		{"role": "tool", "tool_call_id": "call-inspect", "content": `{"nodeId":"image-1"}`},
		imageMessage("image-1"), // 图片挂在 user 消息上（tool 角色只接受字符串）
		{"role": "assistant", "content": "看到了"},
		// 最近一次完整工具轮次（当前这一步）必须保持原样
		toolCall("call-again", "canvas_inspect_image"),
		{"role": "tool", "tool_call_id": "call-again", "content": `{"nodeId":"image-2"}`},
		imageMessage("image-2"),
	}}
	if !cloudAgentPruneInspectedImages(&request) {
		t.Fatal("stale image was not pruned")
	}
	old := request.Messages[3]["content"].([]any)
	if len(old) != 2 || stringValue(old[1].(map[string]any)["type"]) != "text" {
		t.Fatalf("old image part was not replaced by a note: %+v", old)
	}
	if !strings.Contains(stringValue(old[0].(map[string]any)["text"]), "image-1") {
		t.Fatal("pruning lost the text receipt")
	}
	latest := request.Messages[7]["content"].([]any)
	if len(latest) != 2 || stringValue(latest[1].(map[string]any)["type"]) != "image_url" {
		t.Fatalf("the current tool turn must keep its image: %+v", latest)
	}
	// tool 回执始终是字符串，不能被改动
	if _, ok := request.Messages[2]["content"].(string); !ok {
		t.Fatal("tool receipt must stay a string")
	}
	// 幂等
	if cloudAgentPruneInspectedImages(&request) {
		t.Fatal("pruning is not idempotent")
	}
}

// 看过图之后锚点要记住，并且跨轮继承时不能被重建覆盖成 unknown。
func TestCloudAgentInspectedAssetSurvivesAnchorRefresh(t *testing.T) {
	state := &cloudAgentRuntime{CreativeAnchor: cloudAgentCreativeAnchor{Version: 1,
		ReferenceAssets: []cloudAgentReferenceAnchor{{NodeID: "image-1", VisualIdentity: "unknown", RequiresVisualInspection: true}}}}
	state.markCanvasAssetInspected("image-1")
	asset := state.CreativeAnchor.ReferenceAssets[0]
	if asset.VisualIdentity != "inspected" || asset.RequiresVisualInspection {
		t.Fatalf("inspection was not recorded: %+v", asset)
	}
	context := cloudAgentCreativeAnchorContext(state.CreativeAnchor)
	if !strings.Contains(context, "inspected") || strings.Contains(context, "visualIdentity=unknown") {
		t.Fatalf("prompt still claims there is no visual evidence: %s", context)
	}

	// 跨轮继承：锚点每轮从画布重建，重建时必须保留 inspected
	s, _, _ := agentMediaFixture(t)
	stored, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	inherited := cloudAgentCreativeAnchor{Version: 1, ReferenceNodeIDs: []string{"cat"},
		ReferenceAssets: []cloudAgentReferenceAnchor{{NodeID: "cat", VisualIdentity: "inspected", RequiresVisualInspection: false}}}
	refreshed, err := cloudAgentCreativeAnchorForCanvas(s.repo, "user", stored, "用这两张参考图", &inherited)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range refreshed.ReferenceAssets {
		if item.NodeID != "cat" {
			continue
		}
		found = true
		if item.VisualIdentity != "inspected" || item.RequiresVisualInspection {
			t.Fatalf("inspection was lost on anchor refresh: %+v", item)
		}
	}
	if !found {
		t.Fatalf("existing reference asset missing after refresh: %+v", refreshed.ReferenceAssets)
	}
}

// 看图必须校验节点确实是就绪的图片素材：非图片与不存在的节点都要拒绝；
// 合法图片则签发短时下载链接（上游自己取图，Agent 上下文不装 base64）。
func TestCloudAgentImageInspectionResolvesReadyImageOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	t.Setenv("CANVAS_PUBLIC_BASE_URL", server.URL)
	// 签发服务器访问地址同样要过出站策略：内网部署必须把该主机放进
	// CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS（与上游白名单是同一个开关）。
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, db, _ := agentMediaFixture(t)
	// 本地存储的资源才能签出服务器自有的下载链接。
	if err := db.Model(&model.Resource{}).Where("id = ?", "ref-one").Update("provider", "local").Error; err != nil {
		t.Fatal(err)
	}

	text := cloudAgentStoryboardCall(t, "canvas_inspect_image", "inspect-text", map[string]any{"nodeId": "shot-1"})
	if _, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", text); err == nil {
		t.Fatal("non-image node was accepted by the vision tool")
	}
	missing := cloudAgentStoryboardCall(t, "canvas_inspect_image", "inspect-missing", map[string]any{"nodeId": "nope"})
	if _, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", missing); err == nil {
		t.Fatal("missing node was accepted by the vision tool")
	}
	foreign := cloudAgentStoryboardCall(t, "canvas_inspect_image", "inspect-other-user", map[string]any{"nodeId": "cat"})
	if _, err := s.prepareCloudAgentImageInspection("other", "agent-canvas", foreign); err == nil {
		t.Fatal("cross-user canvas was accepted by the vision tool")
	}

	call := cloudAgentStoryboardCall(t, "canvas_inspect_image", "inspect-cat", map[string]any{"nodeId": "cat"})
	result, err := s.prepareCloudAgentImageInspection("user", "agent-canvas", call)
	if err != nil {
		t.Fatalf("ready image node was rejected: %v", err)
	}
	inspection, ok := result.(cloudAgentImageInspection)
	if !ok {
		t.Fatalf("unexpected inspection result: %#v", result)
	}
	if !strings.HasPrefix(inspection.ImageURL, server.URL+"/api/public/resources/ref-one/file/") {
		t.Fatalf("signed download URL missing: %s", inspection.ImageURL)
	}
	if !strings.Contains(inspection.ImageURL, "signature=") || !strings.Contains(inspection.ImageURL, "expires=") {
		t.Fatalf("download URL is not signed: %s", inspection.ImageURL)
	}
	if stringValue(inspection.Receipt["mimeType"]) != "image/png" || stringValue(inspection.Receipt["nodeId"]) != "cat" {
		t.Fatalf("receipt lost media facts: %+v", inspection.Receipt)
	}
	if strings.Contains(compactJSON(inspection.Receipt), "must-not-expose") {
		t.Fatalf("receipt leaked the node's external URL: %+v", inspection.Receipt)
	}
}

// 创建运行时就该把「个人记忆」登记进随任务持久化的 policy 快照：
// 只改局部 policy 变量不会生效（state.Policy 是值拷贝），实测会让分段少 104 token。
func TestCloudAgentRunRecordsMemorySegmentInState(t *testing.T) {
	s, _, _ := agentMediaFixture(t)
	run, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Canonical.SystemPrompt, cloudAgentLessonBlockMarker) {
		t.Fatal("run canonical lost the personal memory block")
	}
	memory := 0
	for _, segment := range state.Policy.SystemSegments {
		if segment.Key == "memory" {
			memory++
			if segment.Bytes <= 0 || segment.Tokens <= 0 {
				t.Fatalf("invalid memory segment: %+v", segment)
			}
		}
	}
	if memory != 1 {
		t.Fatalf("memory segment recorded %d times in the persisted policy", memory)
	}
	breakdown := cloudAgentContextBreakdownPayload(&state)
	segments, ok := breakdown["systemSegments"].([]cloudAgentContextSegment)
	if !ok || len(segments) == 0 {
		t.Fatalf("system segments missing: %+v", breakdown["systemSegments"])
	}
	reported := 0
	for _, segment := range segments {
		reported += segment.Bytes
	}
	if want := len([]byte(state.Canonical.SystemPrompt)); reported != want {
		t.Fatalf("reported segments (%d) do not fill the system bucket (%d)", reported, want)
	}
}

// 上游实测锚点：pressureTokens 用 provider 的计数，projectedTokens = 锚点 + 本地估算的有符号增量，
// 构成按锚点比例校准。这套口径与 deepseek-harness 的 token-meter 一致。
func TestCloudAgentContextPressureAnchorsOnProviderUsage(t *testing.T) {
	state := &cloudAgentRuntime{
		Canonical: canonicalAgentRequest{
			SystemPrompt: "系统提示", Tools: []map[string]any{{"type": "function"}},
			Messages: []map[string]any{{"role": "user", "content": "读取画布"}},
		},
		Policy: cloudAgentPolicySnapshot{SystemSegments: []cloudAgentContextSegment{{Key: "policy", Label: "系统行为策略", Bytes: 12, Tokens: 4}}},
		TokenAnchor: &cloudAgentTokenAnchor{
			TaskID: "task-1", Step: 1, InputTokens: 6000, CachedTokens: 1000, OutputTokens: 200,
			EstimatedTokens: 5000, SourceBytes: 20000, Accepted: true,
		},
	}
	pressure := cloudAgentContextPressure{
		EstimatedInputTokens: 5500, SourceBytes: 21000,
		ContextWindowTokens: 128000, ReservedOutputTokens: 8000, UsableInputTokens: 120000,
	}
	payload := cloudAgentContextPressurePayload(pressure, state)
	if payload["tokenSource"] != "provider" {
		t.Fatalf("tokenSource = %v, want provider", payload["tokenSource"])
	}
	if payload["pressureTokens"] != int64(6000) {
		t.Fatalf("pressureTokens = %v", payload["pressureTokens"])
	}
	if payload["anchorDeltaTokens"] != 500 {
		t.Fatalf("anchorDeltaTokens = %v, want 500", payload["anchorDeltaTokens"])
	}
	if payload["projectedTokens"] != 6500 {
		t.Fatalf("projectedTokens = %v, want 6500", payload["projectedTokens"])
	}
	usage, ok := payload["tokenUsage"].(map[string]any)
	if !ok || usage["uncachedInputTokens"] != int64(5000) || usage["cachedInputTokens"] != int64(1000) {
		t.Fatalf("tokenUsage = %v", payload["tokenUsage"])
	}
	if scale, _ := payload["tokenScale"].(float64); scale != 1.2 {
		t.Fatalf("tokenScale = %v, want 1.2", payload["tokenScale"])
	}
	if _, ok := payload["projectedPressureRatio"].(float64); !ok {
		t.Fatalf("projectedPressureRatio missing: %v", payload["projectedPressureRatio"])
	}
	// 构成：按锚点比例校准后的读数与原始估算同时给出
	breakdown := payload["breakdown"].(map[string]any)
	buckets := breakdown["buckets"].([]map[string]any)
	for _, bucket := range buckets {
		raw, _ := bucket["tokens"].(int)
		scaled, _ := bucket["scaledTokens"].(int)
		if raw <= 0 || scaled <= 0 || scaled < raw {
			t.Fatalf("bucket not calibrated: %+v", bucket)
		}
	}
	if scale, _ := breakdown["tokenScale"].(float64); scale != 1.2 {
		t.Fatalf("breakdown tokenScale = %v, want 1.2", breakdown["tokenScale"])
	}
	segments := breakdown["systemSegments"].([]cloudAgentContextSegment)
	if len(segments) == 0 || segments[0].ScaledTokens == 0 {
		t.Fatalf("segments not calibrated: %+v", segments)
	}
}

// 锚点不可信（与本地估算差出一个量级）时必须拒绝，并如实回报原因——
// 宁可继续用估算，也不要把压力曲线锚到错误基准上。
func TestCloudAgentContextPressureRejectsImplausibleAnchor(t *testing.T) {
	state := &cloudAgentRuntime{
		Canonical:   canonicalAgentRequest{SystemPrompt: "系统提示", Messages: []map[string]any{{"role": "user", "content": "读取画布"}}},
		TokenAnchor: &cloudAgentTokenAnchor{TaskID: "task-2", Step: 2, InputTokens: 100, EstimatedTokens: 5000, Accepted: false, RejectReason: "上游实测远低于本地估算，可能换了模型或口径"},
	}
	payload := cloudAgentContextPressurePayload(cloudAgentContextPressure{EstimatedInputTokens: 5200}, state)
	if payload["tokenSource"] != "estimate" {
		t.Fatalf("tokenSource = %v, want estimate", payload["tokenSource"])
	}
	if _, ok := payload["pressureTokens"]; ok {
		t.Fatal("rejected anchor must not be reported as a measurement")
	}
	if payload["projectedTokens"] != 5200 {
		t.Fatalf("projectedTokens = %v, want the estimate 5200", payload["projectedTokens"])
	}
	if payload["anchorRejected"] == "" || payload["anchorRejected"] == nil {
		t.Fatal("rejection reason missing")
	}
}

// 没有已批准记忆时 recall_lessons 没有任何可召回内容，只占 schema 开销；
// 只读运行不会创建节点，能力卡也没有用途。两者都由服务端推导并随请求持久化，
// 工具授权重跑同一张表，所以裁剪对"下发"和"授权"是一致的。
func TestCloudAgentToolsFollowRunCapabilities(t *testing.T) {
	names := func(req CloudAgentRequest) map[string]bool {
		out := map[string]bool{}
		for _, tool := range cloudAgentTools(req) {
			if function, ok := tool["function"].(map[string]any); ok {
				out[stringValue(function["name"])] = true
			}
		}
		return out
	}

	writeReq := agentTestRequest()
	writeReq.PermissionMode = "auto"
	writeReq.ContextScope = []string{"canvas"}
	writeReq.Budget.MaxGenerationTasks = 1

	withoutMemories := names(writeReq)
	if withoutMemories["recall_lessons"] {
		t.Fatal("recall_lessons exposed without any approved memory")
	}
	writeReq.HasMemories = true
	if !names(writeReq)["recall_lessons"] {
		t.Fatal("recall_lessons missing although the user has approved memories")
	}
	if !names(writeReq)["canvas_list_node_types"] {
		t.Fatal("writable run lost the node capability list")
	}

	readOnly := agentTestRequest()
	readOnly.PermissionMode = "read_only"
	readOnly.ContextScope = []string{"canvas"}
	readOnlyNames := names(readOnly)
	if readOnlyNames["canvas_list_node_types"] {
		t.Fatal("read-only run exposed the node capability list")
	}
	for _, write := range []string{"canvas_apply_ops", "canvas_create_storyboard", "canvas_edit_storyboard", "canvas_edit_batch_table", "generate_media"} {
		if readOnlyNames[write] {
			t.Fatalf("read-only run exposed write tool %s", write)
		}
	}
	if !readOnlyNames["canvas_get_state"] || !readOnlyNames["canvas_read_storyboard"] {
		t.Fatal("read-only run lost its read tools")
	}

	// 平台工具全集必须仍然包含条件暴露的工具，否则能力声明会漏项。
	union := map[string]bool{}
	for _, name := range CloudAgentSupportedToolNames() {
		union[name] = true
	}
	for _, want := range []string{"recall_lessons", "canvas_inspect_image", "canvas_list_node_types", "generate_media"} {
		if !union[want] {
			t.Fatalf("supported tool union lost %s", want)
		}
	}
}
