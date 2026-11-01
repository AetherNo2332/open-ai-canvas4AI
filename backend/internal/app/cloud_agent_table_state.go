package app

import (
	"fmt"
	"sort"
	"strings"

	"yingce/backend/internal/canvas/capability"
)

// cloudAgentStoryboardVisibleColumns 暴露归一化后的展示列：缺省与历史默认都归一到当前
// 默认，用户手选原样回读，让 Agent 不依赖浏览器状态也能知道分镜节点显示哪些列。
func cloudAgentStoryboardVisibleColumns(storyboard map[string]any) []any {
	columns, _ := storyboard["visibleColumns"].([]any)
	if columns == nil {
		if values, ok := storyboard["visibleColumns"].([]string); ok {
			columns = make([]any, len(values))
			for index, value := range values {
				columns[index] = value
			}
		}
	}
	return capability.NormalizeStoryboardColumns(columns)
}

// Summaries locate a shot; a precise node read returns one full row at a time.
// Only narrative fields and canvas IDs are exposed, never arbitrary metadata.
func cloudAgentStoryboardState(storyboard map[string]any, offset int, mode cloudAgentProjectionMode, readRows ...int) map[string]any {
	all := creationMaps(storyboard["rows"])
	visibleColumns := cloudAgentStoryboardVisibleColumns(storyboard)
	if mode == cloudAgentProjectionIndex {
		return cloudAgentStoryboardIndex(all, visibleColumns)
	}
	requested := 0
	if len(readRows) > 0 {
		requested = readRows[0]
	}
	count, textLimit := cloudAgentStoryboardReadLimits(mode, requested)
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
		cloudAgentStoryboardRowFields(row, item, mode, textLimit)
		cloudAgentStoryboardCollections(row, item)
		if mode == cloudAgentProjectionDetail {
			cloudAgentStoryboardDetailFields(row, item, textLimit)
		}
		// rowId 是编辑句柄：它只出现在逐字档，分页档由下面的提示告诉模型去哪里取。
		if mode != cloudAgentProjectionDetail {
			delete(item, "id")
		} else {
			item["rowId"] = item["id"]
			delete(item, "id")
		}
		rows = append(rows, item)
	}
	state := map[string]any{"visibleColumns": visibleColumns, "referenceNodeIds": cloudAgentStrings(storyboard["referenceNodeIds"]), "rows": rows, "totalRows": len(all), "nextOffset": next, "hasMore": next > 0}
	if mode != cloudAgentProjectionDetail {
		state["rowIdSource"] = "本页不含 rowId；用 storyboard(action=read, nodeId, view=bindings) 读取行标识和绑定，需要正文时指定 view=full"
	}
	return state
}

// cloudAgentStoryboardReadLimits 决定一次读返回的行数与单字段上限：逐字档单行宽松预算，
// 通读一次多行字段按 2000 字符回，避免 42 行分镜要 42 次调用。
func cloudAgentStoryboardReadLimits(mode cloudAgentProjectionMode, requested int) (int, int) {
	count, textLimit := cloudAgentPageRows, cloudAgentPageTextLimit
	switch {
	case mode == cloudAgentProjectionDetail && requested > 1:
		count, textLimit = min(requested, cloudAgentMaxReadRows), cloudAgentMultiRowTextLimit
	case mode == cloudAgentProjectionDetail:
		count, textLimit = cloudAgentDetailStoryboardRows, cloudAgentDetailTextLimit
	}
	return count, textLimit
}

// cloudAgentStoryboardRowFields 复制叙事字段并共享 24000 字符总预算；逐字档追加全部
// 正文列。展示省略号可能耗尽最后几个字符，后续字段必须保留合法空预览而不是负预算。
func cloudAgentStoryboardRowFields(row map[string]any, item map[string]any, mode cloudAgentProjectionMode, textLimit int) {
	fields := []string{"id", "shotNumber", "durationSeconds", "plotDescription", "imageNodeId", "videoNodeId"}
	if mode == cloudAgentProjectionDetail {
		fields = append(fields, "videoMotionPrompt", "imageGenerationPrompt", "dialogue", "narrativeIntent", "viewerPOV", "performanceBlocking", "shotSize", "emotion", "lightingAndAtmosphere", "audioEffects", "camera", "motion", "timeBeats", "mustHave", "optionalDetails", "continuityOut", "negativePrompt")
	}
	budget := 24000
	for _, key := range fields {
		switch value := row[key].(type) {
		case string:
			text := truncateRunes(value, min(textLimit, budget))
			budget = max(0, budget-len([]rune(text)))
			item[key] = text
			if text != value {
				item[key+"Truncated"] = true
			}
		case float64:
			item[key] = value
		}
	}
}

// cloudAgentStoryboardCollections 复制绑定与角色集合的轻量预览；超过 16 条只标记截断。
func cloudAgentStoryboardCollections(row map[string]any, item map[string]any) {
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
}

// cloudAgentStoryboardDetailFields 在逐字档补充 PatchField 投影，并把角色资产与版本
// ID 收紧到 120 字符，避免长 ID 挤占正文预算。
func cloudAgentStoryboardDetailFields(row map[string]any, item map[string]any, textLimit int) {
	fields := capability.StoryboardRowFields()
	extra := map[string]capability.PatchField{}
	for _, key := range []string{"mustHave", "optionalDetails", "characters", "assetBindings", "imagePromptTemplateVariables", "videoPromptTemplateVariables", "sourceStartMs", "sourceEndMs", "keyframeTimeMs"} {
		extra[key] = fields[key]
	}
	projected := cloudAgentRowFieldProjection(row, extra, textLimit, 8000)
	if characters, ok := projected["characters"].([]any); ok {
		originals := creationMaps(row["characters"])
		for index, value := range characters {
			if index >= len(originals) {
				break
			}
			character := value.(map[string]any)
			for _, key := range []string{"characterAssetId", "characterVersionId"} {
				if id, ok := originals[index][key].(string); ok {
					character[key] = truncateRunes(id, 120)
				}
			}
		}
	}
	for key, value := range projected {
		item[key] = value
	}
}

// 摘要只承诺"存在与规模"：有多少镜、编号从哪到哪、以及哪个工具能取到行本身。
func cloudAgentStoryboardIndex(all []map[string]any, visibleColumns []any) map[string]any {
	summary := map[string]any{"totalRows": len(all), "visibleColumns": visibleColumns}
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
	summary["readRowsWith"] = "引用检查用 storyboard(nodeId, view=summary/bindings)，默认最多100行；正文用 view=full、offset/rows，默认1行最多20行，按 nextRead 续读"
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
	columns := cloudAgentBatchReferenceColumns(table)
	globalPrompt := strings.TrimSpace(stringValue(table["globalPrompt"]))
	all := creationMaps(table["rows"])
	ready, enabled, missingPrompt, missingReferences, outputLinked := cloudAgentBatchTableStats(all, operation, globalPrompt, len(columns))
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
	requested := 0
	if len(readRows) > 0 {
		requested = readRows[0]
	}
	count, textLimit := cloudAgentBatchTableLimits(mode, requested)
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
		rows = append(rows, cloudAgentBatchTableRowItem(row, mode, textLimit, len(columns)))
	}
	projected := map[string]any{
		"operation": operation, "concurrency": concurrency, "referenceColumns": columns,
		"rows": rows, "totalRows": len(all), "nextOffset": next, "hasMore": next > 0,
		"generationPreview": map[string]any{
			"enabledRows": enabled, "readyRows": ready, "missingPromptRows": missingPrompt,
			"missingReferenceRows": missingReferences, "outputLinkedRows": outputLinked,
		},
	}
	if mode == cloudAgentProjectionDetail {
		if textColumns := cloudAgentBatchTextColumns(table); len(textColumns) > 0 {
			projected["textColumns"] = textColumns
		}
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

// cloudAgentBatchReferenceColumns 归一化参考图列：缺省与历史默认都落到默认列集，
// mentionToken 按列序号生成，供 Agent 用 @参考图N 引用。
func cloudAgentBatchReferenceColumns(table map[string]any) []any {
	columns := []any{}
	for index, column := range creationMaps(table["referenceColumns"])[:min(len(creationMaps(table["referenceColumns"])), maxCloudAgentBatchReferences)] {
		id, label := truncateRunes(stringValue(column["id"]), 120), truncateRunes(stringValue(column["label"]), 120)
		if id != "" && label != "" {
			columns = append(columns, map[string]any{"id": id, "label": label, "mentionToken": fmt.Sprintf("@参考图%d", index+1)})
		}
	}
	if len(columns) == 0 {
		columns = defaultCloudAgentBatchReferenceColumns()
	}
	return columns
}

// cloudAgentBatchTableStats 统计计划形状：启用行里有多少就绪、缺提示词或缺参考图，
// 以及已挂输出节点的行数。就绪判定与生成入口的准入条件保持一致。
func cloudAgentBatchTableStats(all []map[string]any, operation, globalPrompt string, columnCount int) (ready, enabled, missingPrompt, missingReferences, outputLinked int) {
	for _, row := range all {
		rowEnabled, _ := row["enabled"].(bool)
		prompt := strings.TrimSpace(stringValue(row["prompt"]))
		effectivePrompt := globalPrompt
		if effectivePrompt == "" {
			effectivePrompt = prompt
		}
		inputs := cloudAgentBatchInputIDs(row["inputNodeIds"], columnCount)
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
	return ready, enabled, missingPrompt, missingReferences, outputLinked
}

// cloudAgentBatchTableLimits 决定分页行数与单字段上限：逐字档用详情预算，一次多行
// 时收紧到通读上限。
func cloudAgentBatchTableLimits(mode cloudAgentProjectionMode, requested int) (int, int) {
	count, textLimit := cloudAgentPageRows, 240
	if mode == cloudAgentProjectionDetail {
		count, textLimit = cloudAgentDetailBatchRows, cloudAgentDetailTextLimit
	}
	if requested > 0 {
		count = min(requested, cloudAgentMaxReadRows)
		if count > 1 {
			textLimit = min(textLimit, cloudAgentMultiRowTextLimit)
		}
	}
	return count, textLimit
}

// cloudAgentBatchTableRowItem 投影单行：rowId 是编辑句柄，只出现在逐字档。
func cloudAgentBatchTableRowItem(row map[string]any, mode cloudAgentProjectionMode, textLimit, columnCount int) map[string]any {
	item := map[string]any{}
	if mode == cloudAgentProjectionDetail {
		item["id"] = truncateRunes(stringValue(row["id"]), 120)
	}
	item["enabled"] = row["enabled"] == true
	if inputs := cloudAgentBatchInputIDs(row["inputNodeIds"], columnCount); len(inputs) > 0 {
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
	if mode == cloudAgentProjectionDetail {
		fields := capability.BatchRowFields()
		extra := map[string]capability.PatchField{"textNodeIds": fields["textNodeIds"], "cells": fields["cells"]}
		for key, value := range cloudAgentRowFieldProjection(row, extra, textLimit, 8000) {
			item[key] = value
		}
	}
	return item
}

// cloudAgentBatchTextColumns 归一化文本列，供逐字档展示可编辑单元格的来源。
func cloudAgentBatchTextColumns(table map[string]any) []any {
	columns := []any{}
	for _, column := range creationMaps(table["textColumns"])[:min(len(creationMaps(table["textColumns"])), 100)] {
		id, label := truncateRunes(stringValue(column["id"]), 120), truncateRunes(stringValue(column["label"]), 120)
		if id != "" && label != "" {
			columns = append(columns, map[string]any{"id": id, "label": label, "type": "text"})
		}
	}
	return columns
}

func cloudAgentBatchInputIDs(value any, limit int) []any {
	if limit <= 0 || limit > maxCloudAgentBatchReferences {
		limit = maxCloudAgentBatchReferences
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

func cloudAgentRowFieldProjection(row map[string]any, fields map[string]capability.PatchField, textLimit, budget int) map[string]any {
	paths := map[string]capability.PatchField{}
	for key, field := range fields {
		field.Path = key
		paths[key] = field
	}
	values, flags := (capability.Descriptor{PatchFields: paths}).EditableValues(row, textLimit)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, cut := cloudAgentBoundRowValue(key, values[key], &budget)
		values[key] = value
		if cut || flags[key] {
			values[key+"Truncated"] = true
		}
	}
	return values
}

func cloudAgentBoundRowValue(key string, value any, budget *int) (any, bool) {
	switch typed := value.(type) {
	case string:
		if key == "nodeId" || strings.HasSuffix(key, "NodeId") || strings.HasSuffix(key, "NodeIds") || key == "role" {
			return typed, false
		}
		text := truncateRunes(typed, max(*budget, 0))
		*budget -= len([]rune(text))
		return text, text != typed
	case []any:
		values := make([]any, len(typed))
		cut := false
		for index, item := range typed {
			safe, truncated := cloudAgentBoundRowValue(key, item, budget)
			values[index] = safe
			cut = cut || truncated
		}
		return values, cut
	case map[string]any:
		values := map[string]any{}
		cut := false
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			safe, truncated := cloudAgentBoundRowValue(key, typed[key], budget)
			values[key] = safe
			cut = cut || truncated
		}
		return values, cut
	default:
		return value, false
	}
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
