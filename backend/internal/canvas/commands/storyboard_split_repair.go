package commands

import (
	"encoding/json"
	"strconv"
	"strings"
)

// RepairStoryboardSplitPayload performs one deterministic repair. It unwraps
// JSON containers, normalizes text and defaults missing durations; it never
// invents a shot or changes references to make invalid content appear valid.
func RepairStoryboardSplitPayload(raw string) (string, error) {
	var value any
	current := strings.TrimSpace(raw)
	for depth := 0; depth < 5; depth++ {
		if err := json.Unmarshal([]byte(current), &value); err != nil {
			start, end := strings.Index(current, "{"), strings.LastIndex(current, "}")
			if start < 0 || end <= start || json.Unmarshal([]byte(current[start:end+1]), &value) != nil {
				return "", BadAuthRequest("分镜结果不是有效 JSON")
			}
		}
		if list, ok := value.([]any); ok {
			value = map[string]any{"rows": list}
		}
		obj, ok := value.(map[string]any)
		if !ok {
			return "", BadAuthRequest("分镜结果没有镜头行")
		}
		if len(maps(obj["rows"])) > 0 {
			for _, row := range maps(obj["rows"]) {
				duration := row["durationSeconds"]
				if text, ok := duration.(string); ok {
					duration, _ = strconv.ParseFloat(text, 64)
				}
				if n, ok := duration.(float64); ok && n > 0 && n <= 86400 {
					row["durationSeconds"] = n
				} else {
					row["durationSeconds"] = float64(6)
				}
				for _, key := range canvasStoryboardTextFields {
					if text, exists := row[key]; exists {
						row[key] = storyboardRepairText(normalizeStoryboardText(text, 0), 20000)
					}
				}
			}
			if title, ok := obj["title"].(string); ok {
				obj["title"] = storyboardRepairText(title, 240)
			}
			encoded, err := json.Marshal(obj)
			return string(encoded), err
		}
		var nested any
		for _, key := range []string{"text", "data", "result", "storyboard", "content"} {
			if obj[key] != nil {
				nested = obj[key]
				break
			}
		}
		if nested == nil {
			return "", BadAuthRequest("分镜结果没有镜头行")
		}
		if text, ok := nested.(string); ok {
			current = text
		} else {
			encoded, _ := json.Marshal(nested)
			current = string(encoded)
		}
	}
	return "", BadAuthRequest("分镜结果包装层次过深")
}
func storyboardRepairText(text string, max int) string {
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max])
	}
	return text
}
