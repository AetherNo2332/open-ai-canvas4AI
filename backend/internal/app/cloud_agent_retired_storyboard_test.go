package app

// Frozen v17 schemas exercise the low-level migration/receipt algorithms only.
// Production never advertises or authorizes these retired tool names.
func cloudAgentToolsWithRetired(req CloudAgentRequest) []map[string]any {
	tools := cloudAgentTools(req)
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	add := func(name, description string, properties map[string]any, required ...string) {
		parameters := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
		cloudAgentToolParameterContracts(name, parameters)
		cloudAgentRetiredToolContractsForTest(name, parameters)
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": parameters}})
	}
	readFields := append([]string{"durationSeconds"}, cloudAgentStoryboardTextFields...)
	add("canvas_read_storyboard", "frozen v17 test contract", map[string]any{
		"operationIds": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "uniqueItems": true, "items": map[string]any{"type": "string", "minLength": 8, "maxLength": 128}},
		"nodeId":       str(cloudAgentToolText("parameter_014")), "offset": map[string]any{"type": "integer", "minimum": 0},
		"view": map[string]any{"type": "string", "enum": []string{"creative", "summary", "bindings", "full"}}, "mode": map[string]any{"type": "string", "enum": []string{"image", "video"}},
		"rows": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "rowIds": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": str("真实行 ID")},
		"fields":           map[string]any{"type": "array", "minItems": 1, "maxItems": len(readFields), "uniqueItems": true, "items": map[string]any{"type": "string", "enum": readFields}},
		"missingAnyFields": map[string]any{"type": "array", "minItems": 1, "maxItems": len(readFields), "uniqueItems": true, "items": map[string]any{"type": "string", "enum": readFields}},
	}, "nodeId")
	add("canvas_create_storyboard", "frozen v17 test contract", map[string]any{
		"references":   map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1}, "description": "全表共享输入：当前画布节点 ID 或唯一名称。逐镜角色和道具用 rows.assets；系统管理实际引用和连线"},
		"snapshotHash": str(cloudAgentToolText("parameter_046")),
		"nodeId":       str("可选新分镜节点 ID，省略由系统分配"),
		"title":        str(cloudAgentToolText("parameter_048")),
		"rows":         map[string]any{"type": "array", "minItems": 1, "maxItems": maxCloudAgentStoryboardRows, "items": cloudAgentStoryboardRowSchema()},
		"referenceNodeIds": map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1},
			"description": "可选：所有镜头共享的引用输入节点；服务端据此派生连接，不要另外连线"},
		"x": map[string]any{"type": "number"},
		"y": map[string]any{"type": "number"},
	}, "snapshotHash", "title", "rows")
	add("canvas_edit_storyboard", "frozen v17 test contract", map[string]any{
		"updates":            map[string]any{"type": "array", "minItems": 1, "maxItems": maxCloudAgentStoryboardRows, "items": map[string]any{"type": "object", "properties": map[string]any{"rowId": map[string]any{"type": "string", "minLength": 1}, "patch": cloudAgentStoryboardPatchSchema()}, "required": []string{"rowId", "patch"}, "additionalProperties": false}, "description": "batch_update：每个真实 rowId 对应自己的 patch；全部校验后一次提交，最多100项"},
		"rows":               map[string]any{"type": "array", "minItems": 1, "maxItems": maxCloudAgentStoryboardRows, "items": cloudAgentStoryboardRowSchema(), "description": "append_rows：批量追加不同镜头，服务端分配行 ID；追加后总行数最多100"},
		"rowIds":             map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1}, "description": "update/remove/set_assets 可选多个真实行 ID，整批校验后一次写入；与 rowId 互斥"},
		"assets":             cloudAgentStoryboardAssetsSchema(),
		"operationId":        map[string]any{"type": "string", "minLength": 8, "maxLength": 128, "description": "可选稳定批次标识；中断重试必须保持相同标识和相同操作参数。相同标识不重复执行，不同参数拒绝；新批次使用新标识"},
		"storyboardRevision": map[string]any{"type": "string", "minLength": 1, "maxLength": 96, "description": "可选：复制该表读取/成功编辑回执的版本，仅用于纯创作字段 update/batch_update。包含全表、依赖资产和实体世代；无关布局不阻塞。同表或依赖变化仍冲突，省略沿用全画布 snapshotHash"},
		"references":         map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1}, "description": "set_references 整体替换共享输入；各行继承输入随之更新，显式行资产保留。可用唯一完整名称"},
		"snapshotHash":       str(cloudAgentToolText("parameter_049")),
		"nodeId":             str(cloudAgentToolText("parameter_014")),
		"action":             map[string]any{"type": "string", "enum": []string{"append", "append_rows", "update", "batch_update", "remove", "set_assets", "add_assets", "remove_assets", "upsert_assets", "set_references", "add_references", "remove_references", "bind_output", "unbind_output"}, "description": "append/append_rows 追加；update 同值补丁，batch_update 每行不同补丁；add_assets 增加且保留原资产，remove_assets 按节点及用途移除，upsert_assets 增加或改用途，set_assets 明确替换整行资产；add/remove_references 增减共享输入，set_references 明确替换。整批原子"},
		"rowId":              str(cloudAgentToolText("parameter_051")),
		"patch":              cloudAgentStoryboardPatchSchema(),
		"referenceNodeIds": map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{"type": "string", "minLength": 1},
			"description": "set_references 与 references 二选一：整体替换共享列表并重建派生连线，显式行资产保留；[] 清空共享输入"},
		"outputMode":   map[string]any{"type": "string", "enum": []string{"image", "video"}, "description": "绑定或解除产物归属的槽位"},
		"outputNodeId": str("bind_output 必填：已有产物节点 ID；所属关系由服务端派生连接，不重写产物内容"),
	}, "snapshotHash", "nodeId", "action")
	return tools
}

func cloudAgentRetiredToolContractsForTest(name string, schema map[string]any) {
	switch name {
	case "canvas_read_storyboard":
		schema["allOf"] = []map[string]any{
			{"oneOf": []map[string]any{
				{"required": []string{"view"}, "properties": map[string]any{"view": map[string]any{"const": "full"}, "rows": map[string]any{"maximum": 20}}},
				{"properties": map[string]any{"view": map[string]any{"enum": []string{"creative", "summary", "bindings"}}}},
			}},
			{"anyOf": []map[string]any{
				{"properties": cloudAgentForbiddenFields("rowIds")},
				{"properties": map[string]any{"offset": map[string]any{"const": 0}}},
			}},
		}
	case "canvas_create_storyboard":
		schema["anyOf"] = []map[string]any{{"properties": cloudAgentForbiddenFields("references")}, {"properties": cloudAgentForbiddenFields("referenceNodeIds")}}
	case "canvas_edit_storyboard":
		selection := []map[string]any{
			{"required": []string{"rowId"}, "properties": cloudAgentForbiddenFields("rowIds")},
			{"required": []string{"rowIds"}, "properties": cloudAgentForbiddenFields("rowId")},
		}
		update := cloudAgentActionBranch("update", []string{"patch"}, nil, "references", "referenceNodeIds", "assets", "outputMode", "outputNodeId", "updates", "rows")
		update["oneOf"] = selection
		remove := cloudAgentActionBranch("remove", nil, nil, "patch", "references", "referenceNodeIds", "assets", "outputMode", "outputNodeId", "updates", "rows")
		remove["oneOf"] = selection
		assets := cloudAgentActionBranch("set_assets", []string{"assets"}, nil, "patch", "references", "referenceNodeIds", "outputMode", "outputNodeId", "updates", "rows")
		assets["oneOf"] = selection
		refs := cloudAgentActionBranch("set_references", nil, nil, "rowId", "rowIds", "patch", "assets", "outputMode", "outputNodeId", "updates", "rows")
		refs["oneOf"] = []map[string]any{
			{"required": []string{"references"}, "properties": cloudAgentForbiddenFields("referenceNodeIds")},
			{"required": []string{"referenceNodeIds"}, "properties": cloudAgentForbiddenFields("references")},
		}
		schema["oneOf"] = []map[string]any{
			cloudAgentActionBranch("append", []string{"patch"}, map[string]any{"patch": cloudAgentStoryboardRowSchema()}, "rowId", "rowIds", "references", "referenceNodeIds", "assets", "outputMode", "outputNodeId", "updates", "rows"),
			cloudAgentActionBranch("batch_update", []string{"updates"}, nil, "rowId", "rowIds", "patch", "references", "referenceNodeIds", "assets", "outputMode", "outputNodeId", "rows"),
			cloudAgentActionBranch("append_rows", []string{"rows"}, nil, "rowId", "rowIds", "patch", "references", "referenceNodeIds", "assets", "outputMode", "outputNodeId", "updates"),
			update, remove, assets, refs,
			cloudAgentActionBranch("bind_output", []string{"rowId", "outputMode", "outputNodeId"}, map[string]any{"outputNodeId": map[string]any{"type": "string", "minLength": 1}}, "rowIds", "patch", "references", "referenceNodeIds", "assets", "updates", "rows"),
			cloudAgentActionBranch("unbind_output", []string{"rowId", "outputMode"}, nil, "rowIds", "patch", "references", "referenceNodeIds", "assets", "outputNodeId", "updates", "rows"),
		}
		branches := schema["oneOf"].([]map[string]any)
		for _, action := range []string{"add_assets", "remove_assets", "upsert_assets"} {
			assetSchema := cloudAgentStoryboardAssetsSchema()
			assetSchema["minItems"] = 1
			branch := cloudAgentActionBranch(action, []string{"assets"}, map[string]any{"assets": assetSchema}, "patch", "references", "referenceNodeIds", "outputMode", "outputNodeId", "updates", "rows")
			branch["oneOf"] = selection
			branches = append(branches, branch)
		}
		for _, action := range []string{"add_references", "remove_references"} {
			branch := cloudAgentActionBranch(action, nil, map[string]any{"references": map[string]any{"type": "array", "minItems": 1}, "referenceNodeIds": map[string]any{"type": "array", "minItems": 1}}, "rowId", "rowIds", "patch", "assets", "outputMode", "outputNodeId", "updates", "rows")
			branch["oneOf"] = refs["oneOf"]
			branches = append(branches, branch)
		}
		schema["oneOf"] = branches
		for _, branch := range branches {
			properties := branch["properties"].(map[string]any)
			action := properties["action"].(map[string]any)["const"]
			if action != "update" && action != "batch_update" {
				properties["storyboardRevision"] = map[string]any{"not": map[string]any{}}
			} else {
				pure := map[string]any{"patch": map[string]any{"properties": cloudAgentForbiddenFields("assets")}}
				if action == "batch_update" {
					pure = map[string]any{"updates": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": pure}}}
				}
				branch["anyOf"] = []map[string]any{{"properties": cloudAgentForbiddenFields("storyboardRevision")}, {"properties": pure}}
			}
		}

	}
}
