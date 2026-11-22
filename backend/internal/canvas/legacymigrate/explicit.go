package legacymigrate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func applyBatchOutputFields(doc map[string]any) ([]string, []Issue) {
	byID := map[string]map[string]any{}
	for _, node := range list(doc["nodes"]) {
		byID[text(node["id"])] = node
	}
	consumed := []string{}
	warnings := []Issue{}
	for _, edge := range list(doc["connections"]) {
		if text(edge["relation"]) != "batch-output" {
			continue
		}
		table := byID[text(edge["fromNodeId"])]
		if table == nil || text(table["type"]) != "batch-table" {
			continue
		}
		row := batchRow(table, text(edge["storyboardRowId"]))
		if row == nil {
			continue
		}
		declared := strings.TrimSpace(text(row["outputNodeId"]))
		target := text(edge["toNodeId"])
		if declared == "" {
			row["outputNodeId"] = target
			consumed = append(consumed, strings.TrimSpace(text(edge["id"])))
			continue
		}
		if declared == target {
			consumed = append(consumed, strings.TrimSpace(text(edge["id"])))
			continue
		}
		warnings = append(warnings, Issue{Code: "batch_output_conflict", Path: "connections." + text(edge["id"]), Message: "批量行 outputNodeId 与旧连线不一致，保留字段值：" + declared + " / " + target})
	}
	return consumed, warnings
}

func dropConsumedEdges(doc map[string]any, consumed []string) map[string]any {
	if len(consumed) == 0 {
		return doc
	}
	remove := map[string]bool{}
	for _, id := range consumed {
		remove[id] = true
	}
	kept := []any{}
	for _, edge := range list(doc["connections"]) {
		if !remove[strings.TrimSpace(text(edge["id"]))] {
			kept = append(kept, edge)
		}
	}
	doc["connections"] = kept
	return doc
}

func uniqueResources(resources []Resource) []Resource {
	seen := map[string]bool{}
	out := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		key := resource.Kind + "\x00" + resource.ID + "\x00" + resource.NodeID + "\x00" + resource.Role
		if resource.ID == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, resource)
	}
	return out
}

func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Code == issues[j].Code {
			return issues[i].Path < issues[j].Path
		}
		return issues[i].Code < issues[j].Code
	})
}

func pathOf(parts ...string) string {
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if out == "" {
			out = part
		} else {
			out += "." + part
		}
	}
	return out
}

func list(value any) []map[string]any {
	out := []map[string]any{}
	switch entries := value.(type) {
	case []any:
		for _, entry := range entries {
			if item, ok := entry.(map[string]any); ok {
				out = append(out, item)
			}
		}
	case []map[string]any:
		out = append(out, entries...)
	}
	return out
}

func object(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	return map[string]any{}
}

func text(value any) string {
	if value == nil {
		return ""
	}
	if item, ok := value.(string); ok {
		return item
	}
	return fmt.Sprint(value)
}

func number(value any) float64 {
	switch item := value.(type) {
	case int:
		return float64(item)
	case int64:
		return float64(item)
	case float64:
		return item
	case json.Number:
		parsed, _ := item.Float64()
		return parsed
	}
	return 0
}
