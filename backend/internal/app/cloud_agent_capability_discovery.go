package app

func cloudAgentNodeTypesFiltered(nodeType string) (map[string]any, error) {
	result := cloudAgentNodeTypes()
	entries, _ := result["nodes"].([]any)
	// The domain helper currently returns []map so support both forms.
	if typed, ok := result["nodes"].([]map[string]any); ok {
		entries = make([]any, len(typed))
		for i, item := range typed {
			entries[i] = item
		}
	}
	filtered := []any{}
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if nodeType != "" && stringValue(item["type"]) != nodeType {
			continue
		}
		if nodeType == "" {
			if fields, ok := item["updateFields"].(map[string]any); ok {
				summary := map[string]any{}
				for key, value := range fields {
					schema, _ := value.(map[string]any)
					summary[key] = map[string]any{"type": schema["type"], "label": schema["label"]}
				}
				item["updateFields"] = summary
			}
			item["schemaHint"] = "传 nodeType 精读完整字段合同"
		}
		if stringValue(item["type"]) == "drawing" {
			item["contentTools"] = []string{"canvas_read_drawing", "canvas_edit_drawing"}
		}
		filtered = append(filtered, item)
	}
	if nodeType != "" && len(filtered) == 0 {
		return nil, BadAuthRequest("节点类型未注册")
	}
	result["nodes"] = filtered
	return result, nil
}
