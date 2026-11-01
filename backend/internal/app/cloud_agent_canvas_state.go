package app

import (
	"fmt"
	"strings"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/repository"
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
		"showImageInfo": true, "starterMode": true,
		"backgroundMode": true, "appearance": true,
		// 行元数据：上游 operations 写入时以数据库列为准投影进正文（id/title/projectId/createdAt），
		// 并去掉 revision/remoteContentHash。它们不是 Agent 可写内容，不能让投影本身变成"并发修改"。
		"id": true, "title": true, "projectId": true, "createdAt": true, "revision": true, "remoteContentHash": true,
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
	// 上游 operations 写回总会带上 connections；缺省与空列表是同一份内容。
	if content["connections"] == nil {
		content["connections"] = []any{}
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
