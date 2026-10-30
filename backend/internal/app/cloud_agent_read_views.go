package app

import (
	"reflect"
	"strings"
)

type cloudAgentCanvasReadOptions struct{ View, Connections string }

func cloudAgentCanvasReadView(view, connections string, precise, focused bool) (cloudAgentCanvasReadOptions, error) {
	if view == "" {
		view = "catalog"
		if precise {
			view = "detail"
		}
	}
	if connections == "" {
		connections = "none"
		if focused {
			connections = "selected"
		}
	}
	if view != "catalog" && view != "detail" && view != "references" && view != "edit" {
		return cloudAgentCanvasReadOptions{}, cloudAgentFieldError("view", "invalid_value", "view 必须是 catalog、detail、references 或 edit")
	}
	if connections != "none" && connections != "selected" && connections != "incident" {
		return cloudAgentCanvasReadOptions{}, cloudAgentFieldError("connections", "invalid_value", "connections 必须是 none、selected 或 incident")
	}
	return cloudAgentCanvasReadOptions{View: view, Connections: connections}, nil
}

func cloudAgentReadFields(fields []string, view string) []string {
	selected := make([]string, 0, len(fields))
	for _, key := range fields {
		if view != "edit" && key == "richText" {
			continue
		}
		selected = append(selected, key)
	}
	return selected
}

func cloudAgentContentContinuation(node, item map[string]any) {
	descriptor, ok := cloudAgentNodeCapabilityForNode(node)
	if !ok {
		return
	}
	field, ok := descriptor.PatchFields["content"]
	if !ok || field.Kind != "string" || !strings.HasPrefix(field.Path, "metadata.") {
		return
	}
	key := strings.TrimPrefix(field.Path, "metadata.")
	if item[key+"Truncated"] != true {
		return
	}
	text, ok := item[key].(string)
	original := stringValue(cloudAgentNodeMetadata(node)[key])
	if !ok && key == "composerContent" && original == stringValue(cloudAgentNodeMetadata(node)["prompt"]) {
		text, ok = item["prompt"].(string)
	}
	if !ok {
		return
	}
	// The projection appends an ellipsis. Resume at the original-text prefix,
	// rather than treating that display suffix as content already consumed.
	shown, source := []rune(text), []rune(original)
	offset := 0
	for offset < len(shown) && offset < len(source) && shown[offset] == source[offset] {
		offset++
	}
	item["nextRead"] = map[string]any{"tool": "canvas_read_content", "arguments": map[string]any{
		"nodeId": node["id"], "field": key, "offset": offset, "limit": 8000,
	}}
}

func cloudAgentCompactEditable(item map[string]any) {
	editable, _ := item["editable"].(map[string]any)
	fields, _ := editable["fields"].(map[string]any)
	// 与节点顶层字段相同的可编辑值不再重复下发；坐标已在 position 中。
	for key, value := range fields {
		if projected, exists := item[key]; exists && reflect.DeepEqual(value, projected) {
			delete(fields, key)
		}
	}
	if position, ok := item["position"].(map[string]any); ok {
		for _, axis := range []string{"x", "y"} {
			if value, exists := fields[axis]; exists && reflect.DeepEqual(value, position[axis]) {
				delete(fields, axis)
			}
		}
	}
}
