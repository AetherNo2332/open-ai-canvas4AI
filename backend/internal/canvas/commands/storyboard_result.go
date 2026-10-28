package commands

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"yingce/backend/internal/canvas/capability"
)

// Pure counterpart of Canary storyboardRowsFromTask. Row identifiers derive
// from the durable task, so replay cannot mint another set of rows.
func ApplyGeneratedStoryboard(doc Document, nodeID, taskID, raw string) (Document, error) {
	next, err := cloneDocument(doc)
	if err != nil {
		return doc, err
	}
	p := &planner{registry: capability.BuiltinRegistry(), now: func() time.Time { return time.Unix(0, 0) }}
	node, board, old, err := p.storyboardNodeFromDocument(next, nodeID)
	if err != nil {
		return doc, err
	}
	payload, err := storyboardPayload(raw, 0)
	if err != nil {
		return doc, err
	}
	source := maps(payload["rows"])
	if len(source) == 0 || len(source) > maxCanvasStoryboardRows {
		return doc, BadAuthRequest("分镜任务必须返回 1 至 100 个真实镜头")
	}
	fields := capability.StoryboardRowFields()
	rows := []map[string]any{}
	for index, row := range source {
		patch := map[string]any{"durationSeconds": float64(6)}
		for key, field := range fields {
			if value, ok := row[key]; ok {
				if field.Kind == "string" {
					patch[key] = normalizeStoryboardText(value, 0)
				} else {
					patch[key] = value
				}
			}
		}
		if err = p.validateCanvasStoryboardRow(patch, true); err != nil {
			return doc, err
		}
		if err = p.validateCanvasStoryboardReferences(next, patch); err != nil {
			return doc, err
		}
		result := p.canvasStoryboardRowDefaults()
		for key, value := range patch {
			result[key] = value
		}
		result["id"], result["shotNumber"] = "shot-"+taskID+"-"+strconv.Itoa(index+1), float64(index+1)
		rows = append(rows, result)
	}
	board["rows"] = p.mapsAsAny(rows)
	columns := []any{}
	for _, column := range stringList(board["visibleColumns"]) {
		columns = append(columns, column)
	}
	board["visibleColumns"] = capability.NormalizeStoryboardColumns(columns)
	if title := strings.TrimSpace(text(payload["title"])); title != "" {
		if len([]rune(title)) > 240 {
			return doc, BadAuthRequest("分镜标题过长")
		}
		node["title"] = title
	}
	oldIDs := map[string]bool{}
	for _, row := range old {
		oldIDs[text(row["id"])] = true
	}
	edges := []map[string]any{}
	for _, edge := range maps(next["connections"]) {
		if (text(edge["fromNodeId"]) == nodeID || text(edge["toNodeId"]) == nodeID) && oldIDs[text(edge["storyboardRowId"])] {
			continue
		}
		edges = append(edges, edge)
	}
	next["connections"] = p.mapsAsAny(edges)
	if err = validateResult(next); err != nil {
		return doc, err
	}
	return next, nil
}
func storyboardPayload(raw string, depth int) (map[string]any, error) {
	if depth > 4 {
		return nil, BadAuthRequest("分镜结果包装层次过深")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, BadAuthRequest("分镜结果不是有效 JSON")
	}
	if len(maps(payload["rows"])) > 0 {
		return payload, nil
	}
	switch nested := payload["text"].(type) {
	case string:
		start, end := strings.Index(nested, "{"), strings.LastIndex(nested, "}")
		if start >= 0 && end > start {
			return storyboardPayload(nested[start:end+1], depth+1)
		}
	case map[string]any:
		encoded, _ := json.Marshal(nested)
		return storyboardPayload(string(encoded), depth+1)
	}
	return nil, BadAuthRequest("分镜任务没有返回镜头行")
}
func normalizeStoryboardText(value any, depth int) string {
	if depth > 8 {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64, bool:
		return fmt.Sprint(v)
	case []any:
		parts := []string{}
		for _, child := range v {
			if s := normalizeStoryboardText(child, depth+1); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "、")
	case map[string]any:
		for _, key := range []string{"text", "content", "value", "description", "name"} {
			if s := normalizeStoryboardText(v[key], depth+1); s != "" {
				return s
			}
		}
	}
	return ""
}
