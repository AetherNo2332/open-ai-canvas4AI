package commands

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"infinite-canvas/backend/internal/canvas/capability"
)

type BatchDocumentContext struct {
	SourceNodeID   string   `json:"sourceNodeId"`
	InputNodeIDs   []string `json:"inputNodeIds"`
	Video          bool     `json:"video"`
	TargetRowCount int      `json:"targetRowCount"`
	Model          string   `json:"model"`
	X              float64  `json:"x"`
	Y              float64  `json:"y"`
}

func PrepareBatchDocument(doc Document, sourceID string, input map[string]any, model string) (BatchDocumentContext, error) {
	out := BatchDocumentContext{SourceNodeID: sourceID, InputNodeIDs: []string{}, Model: model}
	node := entity(doc, "node:"+sourceID)
	if node == nil || text(node["type"]) != "text" {
		return out, BadAuthRequest("列表分析来源必须是文本节点")
	}
	if raw, ok := input["video"]; ok {
		v, valid := raw.(bool)
		if !valid {
			return out, BadAuthRequest("列表视频模式无效")
		}
		out.Video = v
	}
	if raw, ok := input["targetRowCount"]; ok {
		n, valid := raw.(float64)
		if !valid || n < 1 || n > 500 || math.Trunc(n) != n {
			return out, BadAuthRequest("列表行数必须为 1 至 500")
		}
		out.TargetRowCount = int(n)
	}
	seen := map[string]bool{}
	for _, id := range stringList(input["inputNodeIds"]) {
		if seen[id] {
			continue
		}
		seen[id] = true
		n := entity(doc, "node:"+id)
		if n == nil || (text(n["type"]) != "image" && text(n["type"]) != "video") {
			return out, BadAuthRequest("列表分析输入节点无效")
		}
		out.InputNodeIDs = append(out.InputNodeIDs, id)
	}
	if len(out.InputNodeIDs) > 100 {
		return out, BadAuthRequest("列表分析输入过多")
	}
	pos, _ := node["position"].(map[string]any)
	out.X, _ = pos["x"].(float64)
	out.Y, _ = pos["y"].(float64)
	out.X += 400
	return out, nil
}

func batchResultPayload(raw string) (map[string]any, error) {
	var root map[string]any
	if json.Unmarshal([]byte(raw), &root) == nil {
		if len(maps(root["rows"])) > 0 {
			return root, nil
		}
		if s, ok := root["text"].(string); ok {
			raw = s
		}
	}
	start, depth := -1, 0
	quoted, escaped := false, false
	for i, ch := range raw {
		if quoted {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				quoted = false
			}
			continue
		}
		if ch == '"' {
			quoted = true
			continue
		}
		if ch == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		}
		if ch == '}' && depth > 0 {
			depth--
			if depth == 0 && start >= 0 {
				var result map[string]any
				if json.Unmarshal([]byte(raw[start:i+1]), &result) == nil && len(maps(result["rows"])) > 0 {
					return result, nil
				}
			}
		}
	}
	return nil, BadAuthRequest("列表任务未返回有效数据行")
}
func batchCellText(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(value)
	case []any:
		values := []string{}
		for _, v := range value {
			if s := batchCellText(v); s != "" {
				values = append(values, s)
			}
		}
		return strings.Join(values, "、")
	default:
		raw, _ := json.Marshal(value)
		return string(raw)
	}
}
func normalizedBatchColumn(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("-_：:，,。.!！？?（）()【】[]", r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(s))
}
func batchRowCell(row map[string]any, label string, index int) string {
	if s := batchCellText(row[label]); s != "" {
		return s
	}
	target := normalizedBatchColumn(label)
	keys := []string{}
	for key := range row {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		normalized := normalizedBatchColumn(key)
		if normalized != "" && target != "" && (normalized == target || strings.Contains(normalized, target) || strings.Contains(target, normalized)) {
			return batchCellText(row[key])
		}
	}
	if index < len(keys) {
		return batchCellText(row[keys[index]])
	}
	return ""
}

var numberedListStart = regexp.MustCompile(`(?:^|\s)(?:[0-9]{1,3}|[一二三四五六七八九十百]+)[.、)）]\s*`)

func expandBatchRows(rows []map[string]any, column string, count int) []map[string]any {
	if count == 0 {
		return rows
	}
	if len(rows) >= count {
		return rows[:count]
	}
	expanded := []map[string]any{}
	for _, row := range rows {
		first := batchRowCell(row, column, 0)
		parts := []string{}
		for _, line := range strings.Split(first, "\n") {
			indexes := numberedListStart.FindAllStringIndex(line, -1)
			if len(indexes) == 0 {
				if s := strings.TrimSpace(line); s != "" {
					parts = append(parts, s)
				}
				continue
			}
			for i, index := range indexes {
				end := len(line)
				if i+1 < len(indexes) {
					end = indexes[i+1][0]
				}
				if s := strings.TrimSpace(line[index[1]:end]); s != "" {
					parts = append(parts, s)
				}
			}
		}
		if len(parts) <= 1 {
			expanded = append(expanded, row)
			continue
		}
		for _, part := range parts {
			copy := map[string]any{}
			for k, v := range row {
				copy[k] = v
			}
			copy[column] = part
			expanded = append(expanded, copy)
		}
	}
	if len(expanded) > count {
		return expanded[:count]
	}
	return expanded
}

var batchStoryboardColumns = []string{"durationSeconds", "videoMotionPrompt", "dialogue", "shotSize", "narrativeIntent", "viewerPOV", "performanceBlocking", "camera", "motion", "timeBeats", "lightingAndAtmosphere", "continuityOut", "negativePrompt"}
var batchStoryboardLabels = map[string]string{"durationSeconds": "时长（秒）", "videoMotionPrompt": "视频提示词", "dialogue": "台词/旁白", "shotSize": "景别", "narrativeIntent": "叙事目的", "viewerPOV": "观看视角", "performanceBlocking": "动作调度", "camera": "镜头设计", "motion": "运镜", "timeBeats": "时间节拍", "lightingAndAtmosphere": "光影氛围", "continuityOut": "结尾状态", "negativePrompt": "负面提示词"}

func ApplyGeneratedBatchDocument(doc Document, taskID, raw string, context BatchDocumentContext) (Document, error) {
	next, err := cloneDocument(doc)
	if err != nil {
		return doc, err
	}
	payload, err := batchResultPayload(raw)
	if err != nil {
		return doc, err
	}
	id := "batch-table-" + taskID
	if err := validateCanvasID(id, "生成表格 ID", 80); err != nil {
		return doc, err
	}
	if entity(next, "node:"+id) != nil {
		return doc, BadAuthRequest("生成表格 ID 已存在")
	}
	source := maps(payload["rows"])
	if len(source) > 500 {
		return doc, BadAuthRequest("列表任务最多返回 500 行")
	}
	title := "AI 多维表格"
	columns := []map[string]any{}
	rows := []map[string]any{}
	table := map[string]any{"operation": "creative", "concurrency": float64(10), "aiGenerated": true, "referenceColumns": []any{map[string]any{"id": "ai-ref", "label": "输入", "type": "image"}}}
	inputs := []string{}
	for _, id := range context.InputNodeIDs {
		if entity(next, "node:"+id) != nil {
			inputs = append(inputs, id)
		}
	}
	if context.Video {
		stageID := "list-stage-" + taskID
		stage := map[string]any{"id": stageID, "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{}, "visibleColumns": capability.StoryboardDefaultColumnValues(), "referenceNodeIds": []any{}}}}
		for _, row := range source {
			for seconds, ms := range map[string]string{"sourceStartSeconds": "sourceStartMs", "sourceEndSeconds": "sourceEndMs", "keyframeTimeSeconds": "keyframeTimeMs"} {
				v := row[seconds]
				var n float64
				valid := false
				switch value := v.(type) {
				case float64:
					n = value
					valid = true
				case string:
					if strings.TrimSpace(value) != "" {
						var e error
						n, e = strconv.ParseFloat(value, 64)
						valid = e == nil
					}
				}
				if valid && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 {
					row[ms] = math.Round(n * 1000)
				}
			}
			if batchCellText(row["imageGenerationPrompt"]) == "" {
				row["imageGenerationPrompt"] = batchCellText(row["plotDescription"])
			}
			if batchCellText(row["videoMotionPrompt"]) == "" {
				parts := []string{}
				for _, key := range []string{"plotDescription", "motion"} {
					if s := batchCellText(row[key]); s != "" {
						parts = append(parts, s)
					}
				}
				row["videoMotionPrompt"] = strings.Join(parts, "；")
			}
		}
		stageDoc, _ := cloneDocument(next)
		stageDoc["nodes"] = append(maps(stageDoc["nodes"]), stage)
		encoded, _ := json.Marshal(payload)
		normalized, err := ApplyGeneratedStoryboard(stageDoc, stageID, taskID, string(encoded))
		if err != nil {
			return doc, err
		}
		stageNode := entity(normalized, "node:"+stageID)
		board := stageNode["metadata"].(map[string]any)["storyboard"].(map[string]any)
		storyRows := maps(board["rows"])
		title = text(payload["title"])
		if title == "" {
			title = "视频分镜多维表格"
		}
		table["contentKind"], table["storyboardRows"], table["storyboardTitle"], table["storyboardSourceNodeIds"] = "storyboard", storyRows, title, inputs
		for _, column := range batchStoryboardColumns {
			columns = append(columns, map[string]any{"id": "storyboard-" + column, "label": batchStoryboardLabels[column], "type": "text"})
		}
		for _, row := range storyRows {
			cells := map[string]any{}
			for key, value := range row {
				if key != "id" {
					cells["storyboard-"+key] = batchCellText(value)
				}
			}
			rowInputs := []string{}
			for _, nodeID := range inputs {
				if text(entity(next, "node:"+nodeID)["type"]) == "video" {
					rowInputs = append(rowInputs, nodeID)
					break
				}
			}
			rows = append(rows, map[string]any{"id": row["id"], "enabled": true, "inputNodeIds": rowInputs, "prompt": row["videoMotionPrompt"], "cells": cells})
		}
	} else {
		labels := []string{}
		seen := map[string]bool{}
		for _, label := range stringList(payload["columns"]) {
			label = strings.TrimSpace(label)
			if label != "" && !seen[label] {
				seen[label] = true
				labels = append(labels, label)
			}
		}
		if len(labels) == 0 || len(labels) > 32 {
			return doc, BadAuthRequest("列表需要 1 至 32 个有效列")
		}
		source = expandBatchRows(source, labels[0], context.TargetRowCount)
		if context.TargetRowCount > 0 && len(source) != context.TargetRowCount {
			return doc, BadAuthRequest(fmt.Sprintf("列表返回 %d 行，未满足要求的 %d 行", len(source), context.TargetRowCount))
		}
		for i, label := range labels {
			columns = append(columns, map[string]any{"id": "ai-col-" + strconv.Itoa(i), "label": label, "type": "text"})
		}
		for i, row := range source {
			cells := map[string]any{}
			prompts := []string{}
			for j, label := range labels {
				s := batchRowCell(row, label, j)
				if len(s) > 20000 {
					return doc, BadAuthRequest("列表单元格过长")
				}
				cells["ai-col-"+strconv.Itoa(j)] = s
				if s != "" {
					prompts = append(prompts, label+"："+s)
				}
			}
			rowInputs := []string{}
			if len(inputs) > 0 {
				rowInputs = append(rowInputs, inputs[i%len(inputs)])
			}
			rows = append(rows, map[string]any{"id": "batch-row-" + taskID + "-" + strconv.Itoa(i+1), "enabled": true, "inputNodeIds": rowInputs, "prompt": strings.Join(prompts, "\n"), "cells": cells})
		}
	}
	table["textColumns"], table["rows"] = columns, rows
	node := map[string]any{"id": id, "type": "batch-table", "title": title, "position": map[string]any{"x": context.X, "y": context.Y}, "width": float64(1280), "height": float64(560), "metadata": map[string]any{"batchTable": table, "model": context.Model, "status": "idle", "sourceAnalysisTaskId": taskID}}
	next["nodes"] = append(maps(next["nodes"]), node)
	edges := maps(next["connections"])
	for i, nodeID := range inputs {
		edges = append(edges, map[string]any{"id": "batch-edge-" + taskID + "-" + strconv.Itoa(i+1), "fromNodeId": nodeID, "fromHandleId": "output", "toNodeId": id, "toHandleId": "batch-reference:ai-ref", "relation": "batch-input"})
	}
	next["connections"] = edges
	if err := validateResult(next); err != nil {
		return doc, err
	}
	return next, nil
}
