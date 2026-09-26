package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/repository"
)

// cloudAgentProjectionMode 决定一次读取往模型上下文里注入多少节点内容。
// 轮首摘要每一步都会重发，所以它只承诺"存在与规模"；默认的画布读取返回一页紧凑行；
// 显式按 nodeIds 读取才返回可编辑所需的逐字正文（含真实 rowId）。
//
// 移植说明：上游用 `precise bool` 表达这两种口径。这里换成三档枚举，保留上游的
// Detail/Page 语义（true/false 一一对应），并补上我们自己的 Index 档供轮首摘要使用。
type cloudAgentProjectionMode int

const (
	cloudAgentProjectionIndex cloudAgentProjectionMode = iota
	cloudAgentProjectionPage
	cloudAgentProjectionDetail
)

const (
	// A page keeps the default read small enough that re-reading is cheap.
	cloudAgentPageRows = 5
	// Row IDs exist only in the detail projection: editing already requires the
	// dedicated read tool, so the digest and pages must not pay for them twice.
	cloudAgentDetailStoryboardRows = 1
	cloudAgentDetailBatchRows      = 20
	// 结构化读取一次能要的最大行数；多行读取按 2000 字符/字段回，单行精读才给 16000。
	cloudAgentMaxReadRows       = 20
	cloudAgentMultiRowTextLimit = 2000
	// Non-structured text keeps its existing per-node limits; the structured
	// projectors own their row-level text limits.
	cloudAgentPageTextLimit     = 2000
	cloudAgentDetailTextLimit   = 16000
	cloudAgentIndexExcerptRunes = 60
	cloudAgentListPageNodes     = 40
)

// cloudAgentStructuredProjector 的 rows 是调用方要求的每页行数（0 = 该模式默认）。
type cloudAgentStructuredProjector func(value any, offset int, mode cloudAgentProjectionMode, rows int) (any, error)

var cloudAgentStructuredProjectors = map[string]cloudAgentStructuredProjector{
	"storyboard": func(value any, offset int, mode cloudAgentProjectionMode, rows int) (any, error) {
		storyboard, ok := value.(map[string]any)
		if !ok {
			return nil, nil
		}
		return cloudAgentStoryboardState(storyboard, offset, mode, rows), nil
	},
	"batch_table": func(value any, offset int, mode cloudAgentProjectionMode, rows int) (any, error) {
		table, ok := value.(map[string]any)
		if !ok {
			return nil, nil
		}
		return cloudAgentBatchTableState(table, offset, mode, rows), nil
	},
}

// 画布文档是浏览器与 Agent 共写的：浏览器在同一个记录上维护自己的记账。Agent 快照哈希
// 因此只覆盖 Agent 读得到、写得动的内容，不覆盖界面的自用状态。
// 把界面记账算进哈希会让浏览器写入后的那次自动保存——测量出来的编辑器高度、节点时间戳、
// 视口与聊天面板状态——被当成并发编辑，于是服务端刚刚授权的写入被它自己的下一步拒绝。
var (
	// Top-level keys only the browser writes.
	cloudAgentCanvasUIKeys = map[string]bool{
		"viewport": true, "updatedAt": true, "activeChatId": true, "chatSessions": true,
		"directorScenes": true, "showImageInfo": true, "starterMode": true,
		"backgroundMode": true, "appearance": true,
	}
	// Node fields stamped by the client on save, not authored by the Agent.
	cloudAgentCanvasUINodeKeys = map[string]bool{"createdAt": true, "updatedAt": true}
	// Layout the UI measures and rewrites while rendering (composer height).
	cloudAgentCanvasUIMetadataKeys = map[string]bool{"storyboardComposerHeight": true}
)

func cloudAgentCanvasHash(doc map[string]any) string {
	return creationHash(cloudAgentCanvasContent(doc))
}

// cloudAgentCanvasContent 去掉浏览器自有的记账，让快照哈希只跟踪可读可写的内容：
// id、类型、标题、metadata 正文、连线以及其余画布字段。节点几何仍然算进哈希，
// 因此布局改动依旧要求重新读取（上游原有的 viewport/updatedAt 排除规则并入此表）。
func cloudAgentCanvasContent(doc map[string]any) map[string]any {
	content := make(map[string]any, len(doc))
	for key, value := range doc {
		if cloudAgentCanvasUIKeys[key] {
			continue
		}
		content[key] = value
	}
	nodes := creationMaps(doc["nodes"])
	if len(nodes) == 0 {
		return content
	}
	projected := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		item := make(map[string]any, len(node))
		for key, value := range node {
			if cloudAgentCanvasUINodeKeys[key] {
				continue
			}
			item[key] = value
		}
		if meta, ok := item["metadata"].(map[string]any); ok && len(meta) > 0 {
			clean := make(map[string]any, len(meta))
			for key, value := range meta {
				if cloudAgentCanvasUIMetadataKeys[key] {
					continue
				}
				clean[key] = value
			}
			item["metadata"] = clean
		}
		projected = append(projected, item)
	}
	content["nodes"] = projected
	return content
}

// Generation uses the graph, not canvas presentation or autosave bookkeeping.
// Keep all node business fields (including unknown metadata) fail-closed, and
// keep the full canvas hash for mutations/undo and the database CAS.
func cloudAgentMediaContentHash(doc map[string]any) string {
	content := map[string]any{"connections": doc["connections"]}
	nodes := creationMaps(doc["nodes"])
	projected := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		item := make(map[string]any, len(node))
		for key, value := range node {
			switch key {
			case "position", "width", "height", "createdAt", "updatedAt":
			default:
				item[key] = value
			}
		}
		projected = append(projected, item)
	}
	content["nodes"] = projected
	return cloudAgentCanvasHash(content)
}

const cloudAgentReadPageBytes = 64 << 10

// A full related-component read is useful for answering questions such as
// "what feeds this shot and what does it feed?", but it must remain bounded so
// a densely connected canvas cannot turn one tool call into an unbounded
// context dump.
const cloudAgentRelatedNodeLimit = 256

func cloudAgentCanvasState(repo *repository.Repository, userID, canvasID string, doc map[string]any, offset int, ids []string, storyboardOffset int, connectionOffsets ...int) (any, error) {
	return cloudAgentCanvasStateSelected(repo, userID, canvasID, doc, offset, ids, nil, 0, false, storyboardOffset, connectionOffsets...)
}

// cloudAgentCanvasStateWithFocus returns a bounded graph neighborhood around
// the requested nodes. It is deliberately separate from the legacy paginated
// reader so existing callers keep their exact pagination semantics.
func cloudAgentCanvasStateWithFocus(repo *repository.Repository, userID, canvasID string, doc map[string]any, offset int, focusNodeIDs []string, depth, storyboardOffset int, connectionOffsets ...int) (any, error) {
	return cloudAgentCanvasStateSelected(repo, userID, canvasID, doc, offset, nil, focusNodeIDs, depth, false, storyboardOffset, connectionOffsets...)
}

func cloudAgentCanvasStateWithRelated(repo *repository.Repository, userID, canvasID string, doc map[string]any, offset int, focusNodeIDs []string, storyboardOffset int, connectionOffsets ...int) (any, error) {
	return cloudAgentCanvasStateSelected(repo, userID, canvasID, doc, offset, nil, focusNodeIDs, 0, true, storyboardOffset, connectionOffsets...)
}

func cloudAgentCanvasStateSelected(repo *repository.Repository, userID, canvasID string, doc map[string]any, offset int, ids, focusNodeIDs []string, depth int, includeRelated bool, storyboardOffset int, connectionOffsets ...int) (any, error) {
	if offset < 0 || storyboardOffset < 0 || len(ids) > 8 || len(focusNodeIDs) > 8 || (len(ids) > 0 && len(focusNodeIDs) > 0) {
		return nil, BadAuthRequest("画布读取分页参数无效")
	}
	if len(focusNodeIDs) > 0 && !includeRelated && (depth < 0 || depth > 3) {
		return nil, BadAuthRequest("关联子图 depth 必须在 0 到 3 之间")
	}
	if includeRelated && len(focusNodeIDs) == 0 {
		return nil, BadAuthRequest("includeRelated 只能与 focusNodeIds 一起使用")
	}
	connectionOffset := 0
	if len(connectionOffsets) > 0 {
		connectionOffset = connectionOffsets[0]
	}
	return cloudAgentCanvasStatePageSelected(repo, userID, canvasID, doc, offset, ids, focusNodeIDs, depth, includeRelated, storyboardOffset, connectionOffset, 0)
}

// cloudAgentCanvasStatePage 是并集后的读取入口：上游的「连线分页 + 单页字节预算 + 投影模式」
// 全部保留，另外接受我们自己的结构化行级分页行数（readRows）。
func cloudAgentCanvasStatePage(repo *repository.Repository, userID, canvasID string, doc map[string]any, offset int, ids []string, storyboardOffset, connectionOffset, readRows int) (any, error) {
	return cloudAgentCanvasStatePageSelected(repo, userID, canvasID, doc, offset, ids, nil, 0, false, storyboardOffset, connectionOffset, readRows)
}

func cloudAgentCanvasStatePageSelected(repo *repository.Repository, userID, canvasID string, doc map[string]any, offset int, ids, focusNodeIDs []string, depth int, includeRelated bool, storyboardOffset, connectionOffset, readRows int) (any, error) {
	if offset < 0 || storyboardOffset < 0 || len(ids) > 8 || len(focusNodeIDs) > 8 || (len(ids) > 0 && len(focusNodeIDs) > 0) {
		return nil, BadAuthRequest("画布读取分页参数无效")
	}
	if len(focusNodeIDs) > 0 && (depth < 0 || depth > 3) {
		return nil, BadAuthRequest("关联子图 depth 必须在 0 到 3 之间")
	}
	if len(focusNodeIDs) == 0 && (depth != 0 || includeRelated) {
		return nil, BadAuthRequest("depth/includeRelated 只能与 focusNodeIds 一起使用")
	}
	if includeRelated && depth != 0 {
		return nil, BadAuthRequest("includeRelated 与 depth 不能同时使用")
	}
	if readRows < 0 || readRows > cloudAgentMaxReadRows {
		return nil, BadAuthRequest("每页行数超出限制")
	}
	all := creationMaps(doc["nodes"])
	if connectionOffset < 0 {
		return nil, BadAuthRequest("连线分页参数无效")
	}
	// 显式按 nodeIds 读取是"逐字"路径：只有它返回真实 rowId，而编辑工具必须用 rowId。
	mode := cloudAgentProjectionPage
	if len(ids) > 0 {
		mode = cloudAgentProjectionDetail
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	focus := map[string]bool{}
	for _, id := range focusNodeIDs {
		if id == "" || focus[id] {
			return nil, BadAuthRequest("关联子图 focusNodeIds 不能包含空值或重复节点")
		}
		focus[id] = true
	}
	nodeByID := make(map[string]map[string]any, len(all))
	for _, node := range all {
		if id := stringValue(node["id"]); id != "" {
			nodeByID[id] = node
		}
	}
	for id := range wanted {
		if nodeByID[id] == nil {
			return nil, BadAuthRequest("指定节点不在当前画布")
		}
	}
	for id := range focus {
		if nodeByID[id] == nil {
			return nil, BadAuthRequest("关联子图 focusNodeIds 中存在不在当前画布的节点")
		}
	}
	selected := map[string]bool{}
	selectionTruncated := false
	if len(focus) > 0 {
		adjacency := make(map[string][]string, len(all))
		for _, edge := range creationMaps(doc["connections"]) {
			from, to := stringValue(edge["fromNodeId"]), stringValue(edge["toNodeId"])
			if nodeByID[from] == nil || nodeByID[to] == nil {
				continue
			}
			adjacency[from] = append(adjacency[from], to)
			adjacency[to] = append(adjacency[to], from)
		}
		frontier := make([]string, 0, len(focus))
		// Preserve the caller's focus order. This makes bounded results stable
		// and keeps the selected node itself ahead of its neighbours.
		for _, id := range focusNodeIDs {
			selected[id] = true
			frontier = append(frontier, id)
		}
		levels := depth
		if includeRelated {
			levels = len(all) + 1
		}
		for level := 0; level < levels && len(frontier) > 0; level++ {
			next := make([]string, 0)
			for _, id := range frontier {
				for _, neighbor := range adjacency[id] {
					if selected[neighbor] {
						continue
					}
					if includeRelated && len(selected) >= cloudAgentRelatedNodeLimit {
						selectionTruncated = true
						continue
					}
					selected[neighbor] = true
					next = append(next, neighbor)
				}
			}
			frontier = next
		}
	}
	selectedMode := len(focus) > 0
	candidateNodes := make([]map[string]any, 0, len(all))
	for _, node := range all {
		id := stringValue(node["id"])
		switch {
		case selectedMode && selected[id]:
			candidateNodes = append(candidateNodes, node)
		case !selectedMode && len(ids) > 0 && wanted[id]:
			candidateNodes = append(candidateNodes, node)
		case !selectedMode && len(ids) == 0:
			candidateNodes = append(candidateNodes, node)
		}
	}
	nodes := []any{}
	included := map[string]bool{}
	next := 0
	pageBytes := 1024
	for candidateIndex, node := range candidateNodes {
		if candidateIndex < offset {
			continue
		}
		id := stringValue(node["id"])
		if len(ids) == 0 && len(focus) == 0 && len(nodes) >= cloudAgentListPageNodes {
			next = candidateIndex
			break
		}
		precise := len(ids) > 0 || focus[id]
		meta, _ := node["metadata"].(map[string]any)
		item := map[string]any{"id": id, "type": stringValue(node["type"])}
		if title, ok := node["title"].(string); ok {
			item["title"] = truncateRunes(title, 300)
		}
		if position, ok := node["position"].(map[string]any); ok {
			safePosition := map[string]any{}
			for _, axis := range []string{"x", "y"} {
				if value, ok := cloudAgentSafeNumber(position[axis]); ok {
					safePosition[axis] = value
				}
			}
			item["position"] = safePosition
		}
		for _, dimension := range []string{"width", "height"} {
			if value, ok := cloudAgentSafeNumber(node[dimension]); ok {
				item[dimension] = value
			}
		}
		if status, ok := meta["status"].(string); ok {
			item["status"] = truncateRunes(status, 40)
		}
		capability, known := cloudAgentNodeCapabilityForType(stringValue(node["type"]))
		if !known {
			// Read visibility is not permission to mutate or use a node as a media reference.
			item["agentSupported"] = false
			item["agentUnsupportedReason"] = "仅展示基础信息；当前 Agent 不支持操作此类型节点"
			body, _ := json.Marshal(item)
			if pageBytes+len(body) > cloudAgentReadPageBytes-(8<<10) {
				next = candidateIndex
				break
			}
			pageBytes += len(body)
			nodes = append(nodes, item)
			included[id] = true
			continue
		}
		fields := capability.SummaryFields
		projectMode := mode
		textLimit := cloudAgentPageTextLimit
		if precise {
			fields = capability.DetailFields
			projectMode = cloudAgentProjectionDetail
			textLimit = cloudAgentDetailTextLimit
		}
		projected, err := cloudAgentProjectNodeFields(node, meta, capability, fields, textLimit, projectMode, storyboardOffset, readRows)
		if err != nil {
			return nil, err
		}
		for key, value := range projected {
			item[key] = value
		}
		if capability.GenerationMode != "" {
			generation := map[string]any{"taskStatus": "not_submitted"}
			if reason, issue := cloudAgentMediaTargetIssue(node, capability.Type); reason != "" {
				generation["submitBlockedReason"], generation["submitBlockedIssue"] = reason, issue
			}
			taskID := stringValue(meta["taskId"])
			if taskID == "" {
				taskID = stringValue(meta["generationTaskId"])
			}
			if taskID != "" {
				// Do not infer success/failure from stale canvas metadata.
				generation["taskStatus"] = "unavailable"
				task, err := repo.TaskForUser(userID, taskID)
				if err == nil && task.ProjectID == canvasID {
					for key, value := range cloudAgentTaskDiagnostic(repo, task) {
						generation[key] = value
					}
				}
			}
			item["generation"] = generation
		}
		if draftRunID := stringValue(meta["agentDraftRunId"]); draftRunID != "" && stringValue(meta["taskId"]) == "" && stringValue(meta["generationTaskId"]) == "" {
			draft := map[string]any{"submitted": false, "requiresApproval": true, "ownerStatus": "unknown"}
			owner, err := repo.CloudAgent(userID, draftRunID)
			if err == nil && owner.CanvasID == canvasID {
				draft["ownerStatus"] = owner.Status
				draft["cleanupPending"] = owner.CleanupPending
			}
			item["generationDraft"] = draft
		}
		if capability.Connection.CanReference {
			ref, _, err := cloudAgentReference(repo, userID, node)
			outputReference := map[string]any{"ready": err == nil}
			item["outputReference"] = outputReference
			if err != nil {
				outputReference["issue"] = cloudAgentSafeToolError(err)
			} else {
				// Provider references contain a storage key for task submission.
				// The model only needs the verified public characteristics; never
				// forward the provider payload or storage locator into the read tool.
				item["asset"] = map[string]any{
					"mimeType": ref["mimeType"], "bytes": ref["bytes"],
					"width": ref["width"], "height": ref["height"],
					"durationMs": ref["durationMs"], "inputKind": ref["inputKind"],
				}
			}
		}
		body, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		if pageBytes+len(body) > cloudAgentReadPageBytes-(8<<10) {
			if len(nodes) == 0 {
				return nil, BadAuthRequest("节点详情超过单页读取预算，请使用节点对应的结构化分页工具")
			}
			next = candidateIndex
			break
		}
		pageBytes += len(body)
		nodes = append(nodes, item)
		included[id] = true
	}
	edges := []any{}
	nextConnection := 0
	for index, edge := range creationMaps(doc["connections"]) {
		if index < connectionOffset {
			continue
		}
		fromIncluded := included[stringValue(edge["fromNodeId"])]
		toIncluded := included[stringValue(edge["toNodeId"])]
		if (selectedMode && fromIncluded && toIncluded) || (!selectedMode && (fromIncluded || toIncluded)) {
			item := map[string]any{"id": edge["id"], "fromNodeId": edge["fromNodeId"], "toNodeId": edge["toNodeId"]}
			body, _ := json.Marshal(item)
			if pageBytes+len(body) > cloudAgentReadPageBytes {
				nextConnection = index
				break
			}
			pageBytes += len(body)
			edges = append(edges, item)
		}
	}
	result := map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "mediaSnapshotHash": cloudAgentMediaContentHash(doc), "nodes": nodes, "connections": edges, "totalNodes": len(all), "nextOffset": next, "hasMore": next > 0, "nextConnectionOffset": nextConnection, "hasMoreConnections": nextConnection > 0, "pageByteBudget": cloudAgentReadPageBytes}
	if selectedMode {
		result["totalSelectedNodes"] = len(candidateNodes)
		selection := map[string]any{"mode": "subgraph", "focusNodeIds": focusNodeIDs, "selectedNodes": len(candidateNodes)}
		if includeRelated {
			selection["relationMode"] = "all"
			selection["truncated"] = selectionTruncated
			selection["nodeLimit"] = cloudAgentRelatedNodeLimit
		} else {
			selection["depth"] = depth
		}
		result["selection"] = selection
	}
	return result, nil
}

func cloudAgentSafeNumber(value any) (any, bool) {
	switch number := value.(type) {
	case float64, float32, int, int64:
		return number, true
	default:
		return nil, false
	}
}

// cloudAgentProjectNodeFields is the single projection path for the initial run
// digest, canvas_get_state and the dedicated read tools. Capability descriptors
// decide which fields exist; this function decides how those fields are safely
// represented, and how much of them one projection mode may inject.
// It deliberately never returns arbitrary metadata, URLs, storage keys or
// media payloads.
func cloudAgentProjectNodeFields(node, meta map[string]any, descriptor capability.Descriptor, fields []string, textLimit int, mode cloudAgentProjectionMode, structuredOffset int, readRows ...int) (map[string]any, error) {
	rows := 0
	if len(readRows) > 0 {
		rows = readRows[0]
	}
	projected := map[string]any{}
	for _, key := range fields {
		if descriptor.ProjectionKind != "" && key == descriptor.ProjectionField {
			projector, registered := cloudAgentStructuredProjectors[descriptor.ProjectionKind]
			if !registered {
				return nil, BadAuthRequest(fmt.Sprintf("节点 %s 的结构化读取能力未注册", descriptor.Label))
			}
			value, ok := cloudAgentProjectionValue(node, meta, descriptor.ProjectionField)
			if !ok {
				continue
			}
			structured, err := projector(value, structuredOffset, mode, rows)
			if err != nil {
				return nil, BadAuthRequest(fmt.Sprintf("节点 %s 的结构化数据无法读取", descriptor.Label))
			}
			if structured != nil {
				projected[key] = structured
			}
			continue
		}
		value, ok := node[key]
		if !ok {
			value, ok = meta[key]
		}
		if !ok || (key == "content" && descriptor.GenerationMode != "") {
			continue
		}
		if safe, truncated := cloudAgentSafeProjection(value, textLimit); safe != nil {
			projected[key] = safe
			if truncated {
				projected[key+"Truncated"] = true
			}
		}
	}
	if mode == cloudAgentProjectionIndex {
		cloudAgentCollapseIndexPrompt(projected, descriptor, node, meta, fields)
	} else {
		cloudAgentDropDuplicateComposerContent(projected)
	}
	return projected, nil
}

// 摘要只需要够认出"这是一个生成节点"的提示词：生成草稿上媒体提示词与编辑器草稿本来就是
// 同一个字符串，一条 60 字摘录可以替掉它们全部。非生成的文本节点保持原有投影。
func cloudAgentCollapseIndexPrompt(projected map[string]any, descriptor capability.Descriptor, node, meta map[string]any, fields []string) {
	if descriptor.GenerationMode == "" {
		return
	}
	excerpt := ""
	for _, key := range []string{"prompt", "content", "composerContent"} {
		if key == "content" && descriptor.GenerationMode != "" {
			continue
		}
		if !containsString(fields, key) {
			continue
		}
		value, ok := node[key]
		if !ok {
			value, ok = meta[key]
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			excerpt = truncateRunes(text, cloudAgentIndexExcerptRunes)
			break
		}
	}
	delete(projected, "content")
	delete(projected, "prompt")
	delete(projected, "composerContent")
	delete(projected, "contentTruncated")
	delete(projected, "promptTruncated")
	delete(projected, "composerContentTruncated")
	if excerpt != "" {
		projected["promptExcerpt"] = excerpt
	}
}

// composerContent 是编辑器对 prompt 的草稿副本。只在两者确实不同时才回报，
// 既保住"草稿/已提交"的语义，又不把同一段正文发两遍。
func cloudAgentDropDuplicateComposerContent(projected map[string]any) {
	prompt, hasPrompt := projected["prompt"]
	draft, hasDraft := projected["composerContent"]
	if hasPrompt && hasDraft && prompt == draft {
		delete(projected, "composerContent")
	}
}

func cloudAgentProjectionValue(node, meta map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	for _, root := range []map[string]any{node, meta} {
		var current any = root
		found := true
		for _, part := range parts {
			object, ok := current.(map[string]any)
			if !ok {
				found = false
				break
			}
			current, ok = object[part]
			if !ok {
				found = false
				break
			}
		}
		if found {
			return current, true
		}
	}
	return nil, false
}

func cloudAgentSafeProjection(value any, textLimit int) (any, bool) {
	switch typed := value.(type) {
	case string:
		text := truncateRunes(typed, textLimit)
		return text, len([]rune(typed)) > textLimit
	case float64, float32, int, int64, bool:
		return typed, false
	case []string:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, truncateRunes(item, min(textLimit, 200)))
		}
		return items, false
	case []any:
		items := make([]any, 0, min(len(typed), 32))
		for _, item := range typed[:min(len(typed), 32)] {
			safe, _ := cloudAgentSafeProjection(item, min(textLimit, 200))
			if safe != nil {
				items = append(items, safe)
			}
		}
		return items, len(typed) > len(items)
	default:
		return nil, false
	}
}

// Summaries locate a shot; a precise node read returns one full row at a time.
// Only narrative fields and canvas IDs are exposed, never arbitrary metadata.
func cloudAgentStoryboardState(storyboard map[string]any, offset int, mode cloudAgentProjectionMode, readRows ...int) map[string]any {
	all := creationMaps(storyboard["rows"])
	if mode == cloudAgentProjectionIndex {
		return cloudAgentStoryboardIndex(all)
	}
	requested := 0
	if len(readRows) > 0 {
		requested = readRows[0]
	}
	count, textLimit := cloudAgentPageRows, cloudAgentPageTextLimit
	switch {
	case mode == cloudAgentProjectionDetail && requested > 1:
		// 通读：一次多行，字段按 2000 字符回，避免 42 行分镜要 42 次调用。
		count, textLimit = min(requested, cloudAgentMaxReadRows), cloudAgentMultiRowTextLimit
	case mode == cloudAgentProjectionDetail:
		count, textLimit = cloudAgentDetailStoryboardRows, cloudAgentDetailTextLimit
	}
	rows := []any{}
	next := 0
	for i, row := range all {
		if i < offset {
			continue
		}
		if len(rows) == count {
			next = i
			break
		}
		item := map[string]any{}
		fields := []string{"id", "shotNumber", "durationSeconds", "plotDescription", "imageNodeId", "videoNodeId"}
		if mode == cloudAgentProjectionDetail {
			fields = append(fields, "videoMotionPrompt", "imageGenerationPrompt", "dialogue", "narrativeIntent", "viewerPOV", "performanceBlocking", "shotSize", "emotion", "lightingAndAtmosphere", "audioEffects", "camera", "motion", "timeBeats", "mustHave", "optionalDetails", "continuityOut", "negativePrompt")
		}
		budget := 24000
		for _, key := range fields {
			switch value := row[key].(type) {
			case string:
				text := truncateRunes(value, min(textLimit, budget))
				budget -= len([]rune(text))
				item[key] = text
				if text != value {
					item[key+"Truncated"] = true
				}
			case float64:
				item[key] = value
			}
		}
		for collection, keys := range map[string][]string{
			"assetBindings": {"nodeId", "role", "priority"},
			"characters":    {"characterName", "characterAssetId", "characterVersionId", "characterImageNodeId"},
		} {
			entries := creationMaps(row[collection])
			values := []any{}
			for _, entry := range entries[:min(len(entries), 16)] {
				value := map[string]any{}
				for _, key := range keys {
					if text, ok := entry[key].(string); ok {
						value[key] = truncateRunes(text, 200)
					} else if number, ok := entry[key].(float64); ok {
						value[key] = number
					}
				}
				values = append(values, value)
			}
			// 空集合与缺失键对读者是同一件事，只有非空才值得占用模型上下文。
			if len(values) > 0 {
				item[collection] = values
			}
			if len(entries) > 16 {
				item[collection+"Truncated"] = true
			}
		}
		// rowId 是编辑句柄：它只出现在逐字档，分页档由下面的提示告诉模型去哪里取。
		if mode != cloudAgentProjectionDetail {
			delete(item, "id")
		}
		rows = append(rows, item)
	}
	state := map[string]any{"rows": rows, "totalRows": len(all), "nextOffset": next, "hasMore": next > 0}
	if mode != cloudAgentProjectionDetail {
		state["rowIdSource"] = "本页不含 rowId；通读可继续用本工具翻页，需要真实 rowId 时用 canvas_read_storyboard(nodeId, offset, rows=1) 精读"
	}
	return state
}

// 摘要只承诺"存在与规模"：有多少镜、编号从哪到哪、以及哪个工具能取到行本身。
func cloudAgentStoryboardIndex(all []map[string]any) map[string]any {
	summary := map[string]any{"totalRows": len(all)}
	first, last := "", ""
	for _, row := range all {
		number := truncateRunes(stringValue(row["shotNumber"]), 40)
		if number == "" {
			continue
		}
		if first == "" {
			first = number
		}
		last = number
	}
	if first != "" {
		summary["firstShotNumber"] = first
		summary["lastShotNumber"] = last
	}
	summary["rowFields"] = []string{"shotNumber", "durationSeconds", "plotDescription", "imageNodeId", "videoNodeId"}
	summary["readRowsWith"] = "通读用 canvas_get_state 分页（每页 5 行）或 canvas_read_storyboard(nodeId, offset, rows=5)；需要某一行的真实 rowId 时用 canvas_read_storyboard(nodeId, offset, rows=1) 精读"
	return summary
}

// Batch-table projection exposes only the fields rendered by the component.
// Result URLs, task IDs, storage keys and arbitrary metadata remain private.
func cloudAgentBatchTableState(table map[string]any, offset int, mode cloudAgentProjectionMode, readRows ...int) map[string]any {
	operation := stringValue(table["operation"])
	if operation != "creative" {
		operation = "try_on"
	}
	concurrency := 10
	if value, ok := cloudAgentInteger(table["concurrency"]); ok && (value == 1 || value == 5 || value == 10) {
		concurrency = value
	}
	columns := []any{}
	for index, column := range creationMaps(table["referenceColumns"])[:min(len(creationMaps(table["referenceColumns"])), 6)] {
		id, label := truncateRunes(stringValue(column["id"]), 120), truncateRunes(stringValue(column["label"]), 120)
		if id != "" && label != "" {
			columns = append(columns, map[string]any{"id": id, "label": label, "mentionToken": fmt.Sprintf("@参考图%d", index+1)})
		}
	}
	if len(columns) == 0 {
		columns = defaultCloudAgentBatchReferenceColumns()
	}

	globalPrompt := strings.TrimSpace(stringValue(table["globalPrompt"]))
	all := creationMaps(table["rows"])
	ready, enabled, missingPrompt, missingReferences, outputLinked := 0, 0, 0, 0, 0
	for _, row := range all {
		rowEnabled, _ := row["enabled"].(bool)
		prompt := strings.TrimSpace(stringValue(row["prompt"]))
		effectivePrompt := globalPrompt
		if effectivePrompt == "" {
			effectivePrompt = prompt
		}
		inputs := cloudAgentBatchInputIDs(row["inputNodeIds"], len(columns))
		if rowEnabled {
			enabled++
			if effectivePrompt == "" {
				missingPrompt++
			}
			minimumInputs := 1
			if operation == "try_on" {
				minimumInputs = 2
			}
			if len(inputs) < minimumInputs {
				missingReferences++
			}
			if effectivePrompt != "" && len(inputs) >= minimumInputs {
				ready++
			}
		}
		if stringValue(row["outputNodeId"]) != "" {
			outputLinked++
		}
	}
	// 摘要只回报这份计划的形状与就绪度，不复制任何一行。
	if mode == cloudAgentProjectionIndex {
		return map[string]any{
			"totalRows": len(all), "operation": operation, "concurrency": concurrency,
			"referenceColumnCount": len(columns),
			"generationPreview": map[string]any{
				"enabledRows": enabled, "readyRows": ready, "missingPromptRows": missingPrompt,
				"missingReferenceRows": missingReferences, "outputLinkedRows": outputLinked,
			},
			"readRowsWith": "canvas_read_batch_table(nodeId, offset)：每页最多20行并返回真实 rowId 与 snapshotHash",
		}
	}
	count, textLimit := cloudAgentPageRows, 240
	requested := 0
	if len(readRows) > 0 {
		requested = readRows[0]
	}
	if mode == cloudAgentProjectionDetail {
		count, textLimit = cloudAgentDetailBatchRows, cloudAgentDetailTextLimit
	}
	if requested > 0 {
		count = min(requested, cloudAgentMaxReadRows)
		if count > 1 {
			textLimit = min(textLimit, cloudAgentMultiRowTextLimit)
		}
	}
	rows := []any{}
	next := 0
	for index, row := range all {
		if index < offset {
			continue
		}
		if len(rows) == count {
			next = index
			break
		}
		item := map[string]any{}
		// rowId 是编辑句柄：分页档不含 rowId，由下面的 rowIdSource 指路。
		if mode == cloudAgentProjectionDetail {
			item["id"] = truncateRunes(stringValue(row["id"]), 120)
		}
		item["enabled"] = row["enabled"] == true
		if inputs := cloudAgentBatchInputIDs(row["inputNodeIds"], len(columns)); len(inputs) > 0 {
			item["inputNodeIds"] = inputs
		}
		prompt := stringValue(row["prompt"])
		item["prompt"] = truncateRunes(prompt, textLimit)
		if len([]rune(prompt)) > textLimit {
			item["promptTruncated"] = true
		}
		if outputNodeID := truncateRunes(stringValue(row["outputNodeId"]), 120); outputNodeID != "" {
			item["outputNodeId"] = outputNodeID
		}
		rows = append(rows, item)
	}
	projected := map[string]any{
		"operation": operation, "concurrency": concurrency, "referenceColumns": columns,
		"rows": rows, "totalRows": len(all), "nextOffset": next, "hasMore": next > 0,
		"generationPreview": map[string]any{
			"enabledRows": enabled, "readyRows": ready, "missingPromptRows": missingPrompt,
			"missingReferenceRows": missingReferences, "outputLinkedRows": outputLinked,
		},
	}
	if globalPrompt != "" {
		projected["globalPrompt"] = truncateRunes(globalPrompt, textLimit)
		if len([]rune(globalPrompt)) > textLimit {
			projected["globalPromptTruncated"] = true
		}
	}
	if mode != cloudAgentProjectionDetail {
		projected["rowIdSource"] = "canvas_read_batch_table 返回真实 rowId 与 snapshotHash；本分页结果不含 rowId"
	}
	return projected
}

func cloudAgentBatchInputIDs(value any, limit int) []any {
	if limit <= 0 || limit > 6 {
		limit = 6
	}
	items, _ := value.([]any)
	out := make([]any, 0, min(len(items), limit))
	seen := map[string]bool{}
	for _, item := range items {
		id := truncateRunes(stringValue(item), 120)
		if id == "" || seen[id] || len(out) == limit {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func cloudAgentInteger(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), int64(int(number)) == number
	case float64:
		integer := int(number)
		return integer, float64(integer) == number
	default:
		return 0, false
	}
}
