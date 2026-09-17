package app

import (
	"fmt"
	"strings"

	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/repository"
)

// cloudAgentProjectionMode decides how much node content one read injects into
// the model context. The digest is resent on every step, so it only promises
// existence and scale; the default canvas read returns one page of compact
// rows; an explicit node read returns the verbatim row needed to edit.
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

// The canvas document is co-owned: the browser keeps its own bookkeeping on the
// same record the Agent writes. The Agent snapshot hash therefore covers the
// content the Agent reads and acts on, not what the UI maintains for itself.
// Hashing UI bookkeeping made the browser's own post-write save — a measured
// composer height and node timestamps — look like a concurrent edit, so a write
// the server had just authorised was rejected by its own next step.
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

// cloudAgentCanvasContent drops browser-owned bookkeeping so the snapshot hash
// tracks readable and writable content: ids, types, titles, metadata bodies,
// connections and every other canvas key. Node geometry stays part of the hash,
// so layout edits still require a re-read.
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

// Generation does not depend on node positions. Keep the full canvas hash for
// mutations and undo, which must still detect layout edits before restoring data.
func cloudAgentMediaContentHash(doc map[string]any) string {
	content := make(map[string]any, len(doc))
	for key, value := range doc {
		content[key] = value
	}
	nodes := creationMaps(doc["nodes"])
	projected := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		item := make(map[string]any, len(node))
		for key, value := range node {
			if key != "position" {
				item[key] = value
			}
		}
		projected = append(projected, item)
	}
	content["nodes"] = projected
	return cloudAgentCanvasHash(content)
}

func cloudAgentCanvasState(repo *repository.Repository, userID string, doc map[string]any, offset int, ids []string, storyboardOffset int, rows ...int) (any, error) {
	if offset < 0 || storyboardOffset < 0 || len(ids) > 8 {
		return nil, BadAuthRequest("画布读取分页参数无效")
	}
	readRows := 0
	if len(rows) > 0 {
		if rows[0] < 0 || rows[0] > cloudAgentMaxReadRows {
			return nil, BadAuthRequest("每页行数超出限制")
		}
		readRows = rows[0]
	}
	// An explicit nodeIds read is the verbatim path: it is the only projection
	// that returns row IDs, which the edit tools require.
	mode := cloudAgentProjectionPage
	if len(ids) > 0 {
		mode = cloudAgentProjectionDetail
	}
	all := creationMaps(doc["nodes"])
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	limit := cloudAgentPageTextLimit
	if mode == cloudAgentProjectionDetail {
		limit = cloudAgentDetailTextLimit
	}
	nodes := []any{}
	included := map[string]bool{}
	next := 0
	for index, node := range all {
		id := stringValue(node["id"])
		if len(ids) > 0 {
			if !wanted[id] {
				continue
			}
		} else {
			if index < offset {
				continue
			}
			if len(nodes) == cloudAgentListPageNodes {
				next = index
				break
			}
		}
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
			nodes = append(nodes, item)
			included[id] = true
			continue
		}
		fields := capability.SummaryFields
		if mode == cloudAgentProjectionDetail {
			fields = capability.DetailFields
		}
		projected, err := cloudAgentProjectNodeFields(node, meta, capability, fields, limit, mode, storyboardOffset, readRows)
		if err != nil {
			return nil, err
		}
		for key, value := range projected {
			item[key] = value
		}
		if draftRunID := stringValue(meta["agentDraftRunId"]); draftRunID != "" && stringValue(meta["taskId"]) == "" {
			draft := map[string]any{"submitted": false, "requiresApproval": true, "ownerStatus": "unknown"}
			owner, err := repo.CloudAgent(userID, draftRunID)
			if err == nil && owner.CanvasID != "" {
				draft["ownerStatus"] = owner.Status
				draft["cleanupPending"] = owner.CleanupPending
			}
			item["generationDraft"] = draft
		}
		if capability.Connection.CanReference {
			ref, _, err := cloudAgentReference(repo, userID, node)
			item["referenceReady"] = err == nil
			if err != nil {
				item["referenceIssue"] = err.Error()
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
		nodes = append(nodes, item)
		included[id] = true
	}
	if len(ids) > 0 {
		for _, id := range ids {
			if !included[id] {
				return nil, BadAuthRequest("指定节点不在当前画布")
			}
		}
	}
	edges := []any{}
	for _, edge := range creationMaps(doc["connections"]) {
		if included[stringValue(edge["fromNodeId"])] || included[stringValue(edge["toNodeId"])] {
			edges = append(edges, map[string]any{"id": edge["id"], "fromNodeId": edge["fromNodeId"], "toNodeId": edge["toNodeId"]})
		}
	}
	return map[string]any{"snapshotHash": cloudAgentCanvasHash(doc), "mediaSnapshotHash": cloudAgentMediaContentHash(doc), "nodes": nodes, "connections": edges, "totalNodes": len(all), "nextOffset": next, "hasMore": next > 0}, nil
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

// The digest only needs enough prompt text to recognize a generated node: a
// media prompt and the composer draft are the same string on a prepared draft,
// so one 60-rune excerpt replaces all of them. Non-generation text nodes keep
// their normal projection.
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

// composerContent is the composer's draft copy of prompt. Reporting it only when
// it actually differs keeps the documented draft/edit distinction without
// sending the same text twice.
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
			// An empty collection and a missing key mean the same thing to the
			// reader, so only the non-empty case is worth model context.
			if len(values) > 0 {
				item[collection] = values
			}
			if len(entries) > 16 {
				item[collection+"Truncated"] = true
			}
		}
		// Row IDs are the edit handle. They exist in the detail projection, and
		// the top-level hint below tells the model where to get them.
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

// The digest promises existence and scale only: how many shots exist, where the
// numbering starts and ends, and which tool returns the rows themselves.
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

	all := creationMaps(table["rows"])
	ready, enabled, missingPrompt, missingReferences, outputLinked := 0, 0, 0, 0, 0
	for _, row := range all {
		rowEnabled, _ := row["enabled"].(bool)
		prompt := strings.TrimSpace(stringValue(row["prompt"]))
		inputs := cloudAgentBatchInputIDs(row["inputNodeIds"], len(columns))
		if rowEnabled {
			enabled++
			if prompt == "" {
				missingPrompt++
			}
			minimumInputs := 1
			if operation == "try_on" {
				minimumInputs = 2
			}
			if len(inputs) < minimumInputs {
				missingReferences++
			}
			if prompt != "" && len(inputs) >= minimumInputs {
				ready++
			}
		}
		if stringValue(row["outputNodeId"]) != "" {
			outputLinked++
		}
	}
	// The digest reports the plan's shape and readiness without copying any row.
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
	result := map[string]any{
		"operation": operation, "concurrency": concurrency, "referenceColumns": columns,
		"rows": rows, "totalRows": len(all), "nextOffset": next, "hasMore": next > 0,
		"generationPreview": map[string]any{
			"enabledRows": enabled, "readyRows": ready, "missingPromptRows": missingPrompt,
			"missingReferenceRows": missingReferences, "outputLinkedRows": outputLinked,
		},
	}
	if mode != cloudAgentProjectionDetail {
		result["rowIdSource"] = "canvas_read_batch_table 返回真实 rowId 与 snapshotHash；本分页结果不含 rowId"
	}
	return result
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
