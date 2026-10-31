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
	for _, name := range []string{"patch", "updates", "assets"} {
		compact(props[name])
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
	for _, action := range []string{"split", "resplit"} {
		allowed := append([]string{}, model...)
		required := []string{}
		if action == "resplit" {
			allowed = append(allowed, "rowIds")
			required = append(required, "rowIds")
		}
		b := branch(action, allowed, required)
		b["oneOf"] = []map[string]any{
			{"required": []string{"selectionId"}, "properties": cloudAgentForbiddenFields("logicalModelId", "channelId", "channelModelKey")},
			{"required": []string{"logicalModelId"}, "properties": cloudAgentForbiddenFields("selectionId", "channelId", "channelModelKey")},
			{"required": []string{"channelId", "channelModelKey"}, "properties": cloudAgentForbiddenFields("selectionId", "logicalModelId")},
		}
	}
	u := branch("update", append(append([]string{}, write...), "rowId", "rowIds", "patch", "updates", "storyboardRevision"), []string{"snapshotHash"})
	purePatch := map[string]any{"type": "object", "properties": cloudAgentForbiddenFields("assets")}
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
	for _, action := range []string{"set_assets", "add_assets"} {
		b := branch(action, append(append([]string{}, write...), "rowId", "rowIds", "assets"), []string{"snapshotHash", "assets"})
		b["oneOf"] = selectRows
		if action != "set_assets" {
			b["properties"].(map[string]any)["action"] = map[string]any{"enum": []string{"add_assets", "remove_assets", "upsert_assets"}}
			b["properties"].(map[string]any)["assets"] = map[string]any{"type": "array", "minItems": 1}
		}
	}
	for _, action := range []string{"set_references", "add_references"} {
		b := branch(action, append(append([]string{}, write...), "references", "referenceNodeIds"), []string{"snapshotHash"})
		if action != "set_references" {
			b["properties"].(map[string]any)["action"] = map[string]any{"enum": []string{"add_references", "remove_references"}}
			b["properties"].(map[string]any)["references"] = map[string]any{"type": "array", "minItems": 1}
			b["properties"].(map[string]any)["referenceNodeIds"] = map[string]any{"type": "array", "minItems": 1}
		}
		b["oneOf"] = []map[string]any{{"required": []string{"references"}, "properties": cloudAgentForbiddenFields("referenceNodeIds")}, {"required": []string{"referenceNodeIds"}, "properties": cloudAgentForbiddenFields("references")}}
	}
	branch("bind_output", append(append([]string{}, write...), "rowId", "outputMode", "outputNodeId"), []string{"snapshotHash", "rowId", "outputMode", "outputNodeId"})
	branch("unbind_output", append(append([]string{}, write...), "rowId", "outputMode"), []string{"snapshotHash", "rowId", "outputMode"})
	schema["oneOf"] = branches
}
