package app

func cloudAgentStoryboardContracts(schema map[string]any) {
	props := schema["properties"].(map[string]any)
	props["rowIds"].(map[string]any)["minItems"] = 1
	// The root field definitions carry types. Repeated patch/asset descriptions
	// add tokens without changing the shared validation contract.
	var compact func(any)
	compact = func(value any) {
		switch item := value.(type) {
		case map[string]any:
			delete(item, "description")
			for _, child := range item {
				compact(child)
			}
		case []any:
			for _, child := range item {
				compact(child)
			}
		}
	}
	for _, property := range props {
		compact(property)
	}
	read := []string{"operationIds", "view", "mode", "offset", "rows", "rowIds", "fields", "missingAnyFields"}
	model := []string{"selectionId", "logicalModelId", "channelId", "channelModelKey", "request", "shotCount", "shotDurationSeconds"}
	write := []string{"snapshotHash", "operationId"}
	branches := []map[string]any{}
	branch := func(action string, allowed, required []string) map[string]any {
		properties := map[string]any{"action": map[string]any{"const": action}, "nodeId": map[string]any{}}
		for _, key := range allowed {
			properties[key] = map[string]any{}
		}
		result := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			result["required"] = required
		}
		branches = append(branches, result)
		return result
	}
	selectRows := []map[string]any{
		{"required": []string{"rowId"}, "properties": cloudAgentForbiddenFields("rowIds")},
		{"required": []string{"rowIds"}, "properties": cloudAgentForbiddenFields("rowId")},
	}
	r := branch("read", read, nil)
	r["allOf"] = []map[string]any{
		{"anyOf": []map[string]any{{"properties": cloudAgentForbiddenFields("rowIds")}, {"properties": map[string]any{"offset": map[string]any{"const": 0}}}}},
		{"anyOf": []map[string]any{{"properties": map[string]any{"view": map[string]any{"enum": []string{"creative", "summary", "bindings"}}}}, {"properties": map[string]any{"view": map[string]any{"const": "full"}, "rows": map[string]any{"maximum": 20}}, "required": []string{"view"}}}},
	}
	split := branch("split", append(model, "rowIds"), nil)
	split["properties"].(map[string]any)["action"] = map[string]any{"enum": []string{"split", "resplit"}}
	split["oneOf"] = []map[string]any{
		{"properties": map[string]any{"action": map[string]any{"const": "split"}, "rowIds": false}},
		{"required": []string{"rowIds"}, "properties": map[string]any{"action": map[string]any{"const": "resplit"}}},
	}
	split["anyOf"] = []map[string]any{
		{"required": []string{"selectionId"}, "properties": cloudAgentForbiddenFields("logicalModelId", "channelId", "channelModelKey")},
		{"required": []string{"logicalModelId"}, "properties": cloudAgentForbiddenFields("selectionId", "channelId", "channelModelKey")},
		{"required": []string{"channelId", "channelModelKey"}, "properties": cloudAgentForbiddenFields("selectionId", "logicalModelId")},
		{"properties": cloudAgentForbiddenFields("selectionId", "logicalModelId", "channelId", "channelModelKey")},
	}
	u := branch("update", append(append([]string{}, write...), "rowId", "rowIds", "patch", "updates", "storyboardRevision"), []string{"snapshotHash"})
	purePatch := map[string]any{"type": "object", "properties": cloudAgentForbiddenFields("assets")}
	direct := branch("write_rows", append(append([]string{}, write...), "updates", "newRows", "removeRowIds"), []string{"snapshotHash", "operationId"})
	direct["anyOf"] = []map[string]any{{"required": []string{"updates"}}, {"required": []string{"newRows"}}, {"required": []string{"removeRowIds"}}}
	u["anyOf"] = []map[string]any{{"properties": cloudAgentForbiddenFields("storyboardRevision")}, {"properties": map[string]any{"patch": purePatch, "updates": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"patch": purePatch}}}}}}
	u["oneOf"] = []map[string]any{
		{"required": []string{"patch"}, "properties": cloudAgentForbiddenFields("updates"), "oneOf": selectRows},
		{"required": []string{"updates"}, "properties": cloudAgentForbiddenFields("rowId", "rowIds", "patch")},
	}
	for _, action := range []string{"lock"} {
		b := branch(action, append(append([]string{}, write...), "rowId", "rowIds"), []string{"snapshotHash"})
		b["properties"].(map[string]any)["action"] = map[string]any{"enum": []string{"lock", "unlock"}}
		b["oneOf"] = selectRows
	}
	assets := branch("set_assets", append(append([]string{}, write...), "rowId", "rowIds", "assets"), []string{"snapshotHash", "assets"})
	assets["properties"].(map[string]any)["action"] = map[string]any{"enum": []string{"set_assets", "add_assets", "remove_assets", "upsert_assets"}}
	assets["oneOf"] = selectRows
	assets["anyOf"] = storyboardSetOrNonempty("set_assets", "assets")
	references := branch("set_references", append(append([]string{}, write...), "references", "referenceNodeIds"), []string{"snapshotHash"})
	references["properties"].(map[string]any)["action"] = map[string]any{"enum": []string{"set_references", "add_references", "remove_references"}}
	references["oneOf"] = []map[string]any{{"required": []string{"references"}, "properties": cloudAgentForbiddenFields("referenceNodeIds")}, {"required": []string{"referenceNodeIds"}, "properties": cloudAgentForbiddenFields("references")}}
	references["anyOf"] = storyboardSetOrNonempty("set_references", "references", "referenceNodeIds")
	branch("bind_output", append(append([]string{}, write...), "rowId", "outputMode", "outputNodeId"), []string{"snapshotHash", "rowId", "outputMode", "outputNodeId"})
	branch("unbind_output", append(append([]string{}, write...), "rowId", "outputMode"), []string{"snapshotHash", "rowId", "outputMode"})
	schema["oneOf"] = branches
}

func storyboardSetOrNonempty(action string, fields ...string) []map[string]any {
	nonempty := map[string]any{}
	for _, field := range fields {
		nonempty[field] = map[string]any{"type": "array", "minItems": 1}
	}
	return []map[string]any{
		{"properties": map[string]any{"action": map[string]any{"const": action}}},
		{"properties": nonempty},
	}
}
