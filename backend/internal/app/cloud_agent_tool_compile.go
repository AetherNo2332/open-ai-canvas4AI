package app

import "yingce/backend/internal/model"

// cloudAgentToolCompiler builds the ordered tool list. Tool order is part of the
// contract (schema artifact and prompt-cache prefix), so groups are added in a
// fixed sequence by compileCloudAgentTools.
type cloudAgentToolCompiler struct {
	tools []map[string]any
}

func (c *cloudAgentToolCompiler) add(name, description string, properties map[string]any, required ...string) {
	if required == nil {
		required = []string{}
	}
	parameters := map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	cloudAgentToolParameterContracts(name, parameters)
	if name == "generate_media" {
		description += " " + cloudAgentModelSelectionDescription
	}
	if cloudAgentWrite(name) {
		description += " 本次模型响应只提交一个写工具调用（ops 可含多项），等待回执后再写。快照冲突先重读；成功后使用回执新快照或重新读取，不复用旧快照。"
	}
	c.tools = append(c.tools, map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": parameters}})
}

func cloudAgentToolStringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func compileCloudAgentTools(req CloudAgentRequest) []map[string]any {
	c := &cloudAgentToolCompiler{tools: []map[string]any{}}
	str := cloudAgentToolStringSchema
	if req.WebSearchEnabled {
		c.add("web_search", cloudAgentToolText("web_search"), map[string]any{"query": map[string]any{"type": "string", "minLength": 1, "maxLength": 500, "description": "要联网查询的问题或关键词"}}, "query")
	}
	c.addControlTools()
	if len(req.ContextScope) > 0 {
		c.addCanvasReadTools(req)
	}
	c.add("task_get", cloudAgentToolText("task_get"), map[string]any{"taskId": str(cloudAgentToolText("parameter_022"))}, "taskId")
	if req.VisionEnabled && len(req.ContextScope) > 0 {
		c.add("canvas_inspect_image", cloudAgentToolText("canvas_inspect_image"), map[string]any{"nodeId": str(cloudAgentToolText("parameter_023")), "refresh": map[string]any{"type": "boolean", "description": "true：在每图和本轮预算内重新附送真实图片，单图也有效；已保存摘要仍可重看，重复时 repeat=true"}, "purpose": map[string]any{"type": "string", "enum": []string{"inspect", "compare"}, "description": "默认 inspect 读取指定节点；compare 在同一工具批次逐个读取待对比节点，仍遵守图片数量和成本预算"}}, "nodeId")
	}
	if req.PermissionMode != "read_only" && req.subagent == nil && (req.MemoryMode == model.AgentMemoryDocumentModeAuto || req.MemoryMode == model.AgentMemoryDocumentModeReview) {
		c.addMemoryTool()
	}
	if len(req.ContextScope) > 0 && !req.StoryboardDisabled {
		c.addStoryboardTools(req)
	}
	if req.PermissionMode != "read_only" && len(req.ContextScope) > 0 {
		c.addCanvasWriteTools()
		c.addGenerationTools()
	}
	if req.SubagentEnabled && req.subagent == nil {
		c.addSubagentParentTools()
	}
	if req.subagent != nil {
		c.addSubagentChildTools(req.subagent.WriteGranted)
	}
	return c.tools
}

// addControlTools adds plan, question and finish tools present in every run.
func (c *cloudAgentToolCompiler) addControlTools() {
	str := cloudAgentToolStringSchema
	c.add("plan_update", cloudAgentToolText("plan_update"),
		map[string]any{"items": map[string]any{"type": "array", "maxItems": 20, "items": map[string]any{"type": "object", "properties": map[string]any{"id": str(cloudAgentToolText("parameter_001")), "title": str(cloudAgentToolText("parameter_002")), "status": map[string]any{"type": "string", "enum": []string{"pending", "doing", "done"}}}, "required": []string{"id", "title", "status"}, "additionalProperties": false}}},
		"items")
	c.add("ask_user", cloudAgentToolText("ask_user"),
		map[string]any{
			"question": str(cloudAgentToolText("parameter_003")),
			"options": map[string]any{"type": "array", "minItems": 2, "maxItems": 6, "items": map[string]any{
				"type":       "object",
				"properties": map[string]any{"label": str(cloudAgentToolText("parameter_004")), "detail": str(cloudAgentToolText("parameter_005"))},
				"required":   []string{"label"}, "additionalProperties": false,
			}},
			"allowFreeform": map[string]any{"type": "boolean", "description": cloudAgentToolText("parameter_006")},
		},
		"question", "options")
	c.add("finish_run", cloudAgentToolText("finish_run"),
		map[string]any{"summary": str(cloudAgentToolText("parameter_007"))},
		"summary")
}

// addCanvasReadTools adds the canvas read tools; director is read-only under read_only permission.
func (c *cloudAgentToolCompiler) addCanvasReadTools(req CloudAgentRequest) {
	str := cloudAgentToolStringSchema
	directorReadOnly := req.PermissionMode == "read_only"
	c.add("director", cloudAgentToolText("director"), cloudAgentDirectorToolSchema(directorReadOnly), "action")
	c.add("canvas_list_node_types", "查询可创建节点和可编辑字段。传 nodeType 返回该类型的完整字段 schema；编辑复杂内容前先查字段合同。", map[string]any{"nodeType": str("可选节点类型；空值列出摘要")})
	c.add("canvas_list_assets", "搜索当前用户素材库，返回可用于绑定的 assetId、标题和类型，不暴露下载凭证。", map[string]any{"query": str("可选搜索文字"), "kind": map[string]any{"type": "string", "enum": []string{"image", "video", "audio", "text"}}, "page": map[string]any{"type": "integer", "minimum": 1}})
	c.add("canvas_read_content", "分页读取节点正文或分镜行文本。分镜续读须同时指定 rowId 和 field。offset/limit 是 Unicode 字符计数；hasMore=false 才表示读到末尾。返回 snapshotHash 可用于精确编辑。", map[string]any{
		"nodeId": str("当前画布节点 ID"), "rowId": str("分镜行 ID，普通正文省略"), "field": map[string]any{"type": "string", "enum": append([]string{"content", "composerContent"}, cloudAgentStoryboardTextFields...)},
		"offset": map[string]any{"type": "integer", "minimum": 0}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 16000},
	}, "nodeId")
	c.add("canvas_search_nodes", "按标题和正文搜索当前画布节点；返回基础索引和新快照。需要编辑时继续精读目标。", map[string]any{
		"includeArchived": map[string]any{"type": "boolean", "description": "默认排除归档节点；查历史资料时设 true"},
		"query":           str("搜索文字，大小写不敏感"), "type": str("可选业务 kind 或底层存储 type，角色卡可用 character"), "offset": map[string]any{"type": "integer", "minimum": 0},
	}, "query")
	c.add("canvas_get_state", cloudAgentToolText("canvas_get_state"), map[string]any{
		"view":             map[string]any{"type": "string", "enum": []string{"catalog", "detail", "references", "edit", "media_versions"}, "description": "默认目录；指定节点默认 detail。media_versions 指定单个媒体 nodeId，返回全部版本（最多30）；references 查关系；edit 才返回 editable/richText"},
		"connections":      map[string]any{"type": "string", "enum": []string{"none", "selected", "incident"}, "description": "默认不返回连线；子图默认 selected；incident 包含所读节点的关联边"},
		"offset":           map[string]any{"type": "integer", "minimum": 0, "description": cloudAgentToolText("parameter_008")},
		"connectionOffset": map[string]any{"type": "integer", "minimum": 0, "description": cloudAgentToolText("parameter_009")},
		"storyboardOffset": map[string]any{"type": "integer", "minimum": 0, "description": cloudAgentToolText("parameter_010")},
		"nodeIds":          map[string]any{"type": "array", "maxItems": 8, "items": str(cloudAgentToolText("parameter_011")), "description": cloudAgentToolText("parameter_012")},
		"focusNodeIds":     map[string]any{"type": "array", "maxItems": 8, "items": str(cloudAgentToolText("parameter_083")), "description": cloudAgentToolText("parameter_084")},
		"depth":            map[string]any{"type": "integer", "minimum": 0, "maximum": 3, "description": cloudAgentToolText("parameter_085")},
		"includeRelated":   map[string]any{"type": "boolean", "description": cloudAgentToolText("parameter_086")},
	})
	c.add("canvas_read_batch_table", cloudAgentToolText("canvas_read_batch_table"), map[string]any{"nodeId": str(cloudAgentToolText("parameter_013")), "offset": map[string]any{"type": "integer", "minimum": 0}}, "nodeId")
}

// addMemoryTool adds memory_update when long-term memory is writable for this run.
func (c *cloudAgentToolCompiler) addMemoryTool() {
	str := cloudAgentToolStringSchema
	c.add("memory_update", cloudAgentToolText("memory_update"),
		map[string]any{
			"operations": map[string]any{"type": "array", "minItems": 1, "maxItems": agentMemoryOpsMax, "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action":  map[string]any{"type": "string", "enum": []string{"add", "replace", "remove"}},
					"id":      str(cloudAgentToolText("memory_update_id")),
					"section": map[string]any{"type": "string", "enum": []string{model.AgentMemorySectionUser, model.AgentMemorySectionNotes}, "description": cloudAgentToolText("memory_update_section")},
					"text":    map[string]any{"type": "string", "maxLength": agentMemoryEntryMaxRunes, "description": cloudAgentToolText("memory_update_text")},
				},
				"required": []string{"action"}, "additionalProperties": false,
			}},
			"reason": map[string]any{"type": "string", "maxLength": agentMemoryReasonMaxRunes, "description": cloudAgentToolText("memory_update_reason")},
		},
		"operations")
}

// addStoryboardTools adds storyboard plus history, asset binding and model listing tools.
func (c *cloudAgentToolCompiler) addStoryboardTools(req CloudAgentRequest) {
	str := cloudAgentToolStringSchema
	readFields := append([]string{"durationSeconds", "locked", "mediaReview"}, cloudAgentStoryboardTextFields...)
	c.add("storyboard", "统一分镜：read 读 Markdown、分页与引用；split/resplit 异步调用服务端从剧情、风格和相邻镜头拆镜，不手填整表。写回后在同一运行的下一模型步骤通知，依赖结果用 wait_generations。update 小范围改行，lock/unlock 保护镜头；资产和绑定 action 维护引用。写动作拒绝锁定目标，unlock 除外。", map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"read", "split", "resplit", "update", "lock", "unlock", "set_assets", "add_assets", "remove_assets", "upsert_assets", "set_references", "add_references", "remove_references", "bind_output", "unbind_output"}},
		"nodeId": str("分镜节点 ID"), "snapshotHash": str("update 或资产/绑定写入使用的最新画布快照"),
		"rowId": str("单个真实 rowId"), "rowIds": map[string]any{"type": "array", "maxItems": 100, "uniqueItems": true, "items": str("真实分镜 rowId")},
		"view": map[string]any{"type": "string", "enum": []string{"creative", "summary", "bindings", "full"}}, "mode": map[string]any{"type": "string", "enum": []string{"image", "video"}},
		"offset": map[string]any{"type": "integer", "minimum": 0}, "rows": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		"fields":           map[string]any{"type": "array", "minItems": 1, "maxItems": len(readFields), "uniqueItems": true, "items": map[string]any{"type": "string", "enum": readFields}},
		"missingAnyFields": map[string]any{"type": "array", "minItems": 1, "maxItems": len(readFields), "uniqueItems": true, "items": map[string]any{"type": "string", "enum": readFields}},
		"request":          map[string]any{"type": "string", "maxLength": 4000, "description": "resplit 的修改意见，尽量使用用户原话"},
		"shotCount":        map[string]any{"type": "integer", "minimum": 0, "maximum": 100}, "shotDurationSeconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 60},
		"selectionId": str("model_list 返回的一次性系统模型选择凭证"), "logicalModelId": str("系统逻辑模型 ID"), "channelId": str("系统渠道 ID"), "channelModelKey": str("系统渠道模型键"),
		"operationId": map[string]any{"type": "string", "minLength": 8, "maxLength": 128}, "operationIds": map[string]any{"type": "array", "minItems": 1, "maxItems": 20, "uniqueItems": true, "items": map[string]any{"type": "string", "minLength": 8, "maxLength": 128}}, "storyboardRevision": str("read 返回的表级版本"),
		"updates": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"type": "object", "properties": map[string]any{"rowId": str("真实镜头 ID"), "patch": cloudAgentStoryboardPatchSchema()}, "required": []string{"rowId", "patch"}, "additionalProperties": false}},
		"patch":   cloudAgentStoryboardPatchSchema(), "assets": cloudAgentStoryboardAssetsSchema(), "references": map[string]any{"type": "array", "maxItems": 100, "items": str("画布节点 ID 或唯一名称")}, "referenceNodeIds": map[string]any{"type": "array", "maxItems": 100, "items": str("画布节点 ID")},
		"outputMode": map[string]any{"type": "string", "enum": []string{"image", "video"}}, "outputNodeId": str("绑定或解除产物时的节点 ID"),
	}, "action", "nodeId")
	if req.PermissionMode == "read_only" {
		c.tools[len(c.tools)-1]["function"].(map[string]any)["parameters"].(map[string]any)["properties"].(map[string]any)["action"] = map[string]any{"type": "string", "enum": []string{"read"}}
	}
	if req.PermissionMode != "read_only" {
		for _, name := range []string{"canvas_undo", "canvas_redo"} {
			c.add(name, "撤销或重做本次运行的最新画布变更；画布有后续修改时返回冲突。已提交生成任务和费用不可撤销。", map[string]any{"snapshotHash": str("当前画布快照")}, "snapshotHash")
		}
		c.add("canvas_bind_asset", "用当前用户素材库的 assetId 替换匹配类型节点的内容；先读取画布并查询素材，不接受任意 URL。运行中的生成节点不能替换。", map[string]any{"snapshotHash": str("最新画布快照"), "nodeId": str("目标节点 ID"), "assetId": str("素材库 ID")}, "snapshotHash", "nodeId", "assetId")
		c.add("model_list", cloudAgentToolText("model_list")+" 分镜可传 storyboardRowIds 批量查询；相同实际输入能力合并为一份模型列表，逐行返回问题。", map[string]any{"mode": map[string]any{"type": "string", "enum": cloudAgentGenerationModeNames()}, "referenceNodeIds": map[string]any{"type": "array", "maxItems": 16, "items": str(cloudAgentToolText("parameter_045"))}, "storyboardNodeId": str("分镜表节点 ID，与 storyboardRowId 或 storyboardRowIds 同时传入，输入从行读取"), "storyboardRowId": str("分镜行 ID"), "storyboardRowIds": map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "uniqueItems": true, "items": map[string]any{"type": "string", "minLength": 1}}})
	}
}

// addCanvasWriteTools adds character, batch table, canvas ops and arrange tools.
func (c *cloudAgentToolCompiler) addCanvasWriteTools() {
	str := cloudAgentToolStringSchema
	c.add("canvas_create_character", "把就绪图片打包成角色卡并保存媒体来源；自动分配节点 ID 和位置。可分别指定定妆主图和三视图，单图时明确复用。按权限审批。已有同名卡先复用；definition 只填有依据的设定。", cloudAgentCharacterCreateSchema(), "name")
	c.add("canvas_edit_batch_table", cloudAgentToolText("canvas_edit_batch_table"), map[string]any{
		"snapshotHash": str(cloudAgentToolText("parameter_052")),
		"nodeId":       str(cloudAgentToolText("parameter_013")),
		"action":       map[string]any{"type": "string", "enum": []string{"append", "update", "remove", "set_operation", "set_concurrency", "add_reference_column", "remove_reference_column", "set_global_prompt"}},
		"rowId":        str(cloudAgentToolText("parameter_054")),
		"patch":        cloudAgentBatchTablePatchSchema(),
		"operation":    map[string]any{"type": "string", "enum": []string{"try_on", "creative"}},
		"concurrency":  map[string]any{"type": "integer", "enum": []int{1, 5, 10}},
		"globalPrompt": str(cloudAgentToolText("parameter_055")),
	}, "snapshotHash", "nodeId", "action")
	opProperties := map[string]any{
		"type":         map[string]any{"type": "string", "enum": []string{"add_node", "update_node", "delete_node", "duplicate_node", "set_parent", "replace_text", "reorder_nodes", "reorder_rows", "use_media_version", "extract_media_version"}, "description": "创建、修改、删除、复制节点，维护分组和排序，或精确替换文本；use_media_version 切换节点到已有版本，extract_media_version 把版本取出为右侧独立节点，均无生成费用；输入引用写引用字段"},
		"versionId":    str("media_versions 返回的版本 id"),
		"nodeId":       str("extract_media_version 新节点 ID"),
		"id":           str(cloudAgentToolText("parameter_057")),
		"nodeType":     map[string]any{"type": "string", "enum": cloudAgentNodeTypeNames()},
		"title":        str(cloudAgentToolText("parameter_058")),
		"content":      str(cloudAgentToolText("parameter_059")),
		"patch":        cloudAgentPatchSchema(),
		"x":            map[string]any{"type": "number"},
		"y":            map[string]any{"type": "number"},
		"sourceNodeId": str("复制来源节点；id 是副本的新 ID"),
		"parentId":     str("父容器 ID；空字符串表示脱离容器"),
		"match":        map[string]any{"type": "string", "minLength": 1, "maxLength": 16000},
		"replacement":  map[string]any{"type": "string", "maxLength": 16000},
		"nodeIds":      map[string]any{"type": "array", "maxItems": 2048, "items": map[string]any{"type": "string"}},
		"rowIds":       map[string]any{"type": "array", "maxItems": 2048, "items": map[string]any{"type": "string"}},
	}
	opItem := map[string]any{
		"type":                 "object",
		"properties":           opProperties,
		"required":             []string{"type", "id"},
		"additionalProperties": false,
		"oneOf": []map[string]any{
			{"properties": map[string]any{"type": map[string]any{"const": "add_node"}}, "required": []string{"nodeType"}},
			{"properties": map[string]any{"type": map[string]any{"const": "update_node"}}, "required": []string{"patch"}},
			{"properties": map[string]any{"type": map[string]any{"const": "delete_node"}}},
			{"properties": map[string]any{"type": map[string]any{"const": "duplicate_node"}}, "required": []string{"sourceNodeId"}},
			{"properties": map[string]any{"type": map[string]any{"const": "set_parent"}}, "required": []string{"parentId"}},
			{"properties": map[string]any{"type": map[string]any{"const": "replace_text"}}, "required": []string{"match", "replacement"}},
			{"properties": map[string]any{"type": map[string]any{"const": "reorder_nodes"}}, "required": []string{"nodeIds"}},
			{"properties": map[string]any{"type": map[string]any{"const": "reorder_rows"}}, "required": []string{"rowIds"}},
			{"properties": map[string]any{"type": map[string]any{"const": "use_media_version"}}, "required": []string{"versionId"}},
			{"properties": map[string]any{"type": map[string]any{"const": "extract_media_version"}}, "required": []string{"versionId", "nodeId"}},
		},
	}
	c.add("canvas_apply_ops", cloudAgentToolText("canvas_apply_ops"), map[string]any{"snapshotHash": str(cloudAgentToolText("parameter_062")), "ops": map[string]any{"type": "array", "maxItems": 20, "items": opItem}}, "snapshotHash", "ops")
	c.add("canvas_arrange_nodes", cloudAgentToolText("canvas_arrange_nodes"), map[string]any{
		"snapshotHash": str(cloudAgentToolText("parameter_063")),
		"nodeIds":      map[string]any{"type": "array", "maxItems": cloudAgentArrangeMaxNodes, "items": str(cloudAgentToolText("parameter_064"))},
		"mode":         map[string]any{"type": "string", "enum": []any{"auto", "flow", "byType", "row", "column", "grid"}, "description": cloudAgentToolText("parameter_065")},
		"groups": map[string]any{"type": "array", "maxItems": cloudAgentArrangeMaxGroups, "items": map[string]any{
			"type": "object", "properties": map[string]any{
				"label":   str(cloudAgentToolText("parameter_066")),
				"nodeIds": map[string]any{"type": "array", "maxItems": cloudAgentArrangeMaxNodes, "items": str(cloudAgentToolText("parameter_067"))},
				"mode":    map[string]any{"type": "string", "enum": []any{"byType", "flow", "row", "column", "grid"}},
			}, "required": []string{"nodeIds"}, "additionalProperties": false,
		}},
		"align":  map[string]any{"type": "string", "enum": []any{"left", "centerX", "right", "top", "centerY", "bottom", "distributeX", "distributeY"}},
		"gap":    map[string]any{"type": "number", "minimum": 0, "maximum": cloudAgentArrangeMaxGap, "description": cloudAgentToolText("parameter_068")},
		"dryRun": map[string]any{"type": "boolean", "description": cloudAgentToolText("parameter_069")},
	}, "snapshotHash")
}

// addGenerationTools adds media generation and the generation wait tool.
func (c *cloudAgentToolCompiler) addGenerationTools() {
	str := cloudAgentToolStringSchema
	c.add("generate_media", cloudAgentToolText("generate_media"), map[string]any{
		"intent":           map[string]any{"type": "string", "enum": []string{"create", "new_version"}, "description": "默认 create；已有媒体须明确 new_version，在原节点生成新版本，期间旧版可用；成功自动采用，期间用户手动换过版本时只进历史，回执给 versionId 与 adopted。原任务未完成、接单未知或待保存时先恢复原任务，不重付生成"},
		"storyboardNodeId": str("分镜表节点 ID；分镜模式不传 prompt、引用和时长，由服务端读取行字段"), "storyboardRowId": str("分镜行 ID，与 storyboardNodeId 同时传入"),
		"mode": map[string]any{"type": "string", "enum": cloudAgentGenerationModeNames()}, "prompt": str(cloudAgentToolText("parameter_070")),
		"selectionId": str(cloudAgentToolText("model_selection_id")), "logicalModelId": str(cloudAgentToolText("parameter_071")), "channelId": str(cloudAgentToolText("parameter_038")), "channelModelKey": str(cloudAgentToolText("parameter_039")),
		"durationSeconds": map[string]any{"type": "integer", "minimum": 0}, "size": str(cloudAgentToolText("parameter_074")), "quality": str(cloudAgentToolText("parameter_075")), "videoGenerateAudio": map[string]any{"type": "boolean", "description": cloudAgentToolText("parameter_076")},
		"snapshotHash": str(cloudAgentToolText("parameter_077")), "nodeId": str(cloudAgentToolText("parameter_078")), "title": str(cloudAgentToolText("parameter_079")), "sourceNodeId": str(cloudAgentToolText("parameter_080")), "referenceNodeIds": map[string]any{"type": "array", "maxItems": 16, "items": str(cloudAgentToolText("parameter_081"))}, "referenceTransientIds": map[string]any{"type": "array", "maxItems": 4, "items": str(cloudAgentToolText("parameter_082"))},
	}, "mode")
	c.add("wait_generations", cloudAgentToolText("wait_generations"), map[string]any{})
}

// addSubagentParentTools adds the tools a parent Agent uses to manage children.
func (c *cloudAgentToolCompiler) addSubagentParentTools() {
	c.add("spawn_subagent", "Create one independent child Agent with a name, role label and concrete objective. write=true 申请写入权限：仅当父运行权限为 auto/request_approval 且画布开启子代理写授权时授予；授予后子代理继承父权限模式，预算按父剩余积分切分（maxCredits 可进一步收紧）。", map[string]any{
		"name":         map[string]any{"type": "string", "maxLength": maxDynamicSubagentNameRunes},
		"role":         map[string]any{"type": "string", "maxLength": maxDynamicSubagentRoleRunes},
		"objective":    map[string]any{"type": "string", "maxLength": maxDynamicSubagentObjectiveRunes},
		"instructions": map[string]any{"type": "string", "maxLength": maxDynamicSubagentObjectiveRunes},
		"maxSteps":     map[string]any{"type": "integer", "minimum": 0, "maximum": 20},
		"write":        map[string]any{"type": "boolean", "description": "申请写入权限；默认 false（只读）。回执 writeGranted/writeReason 说明授予结果"},
		"maxCredits":   map[string]any{"type": "number", "minimum": 0, "maximum": 1e7, "description": "可选：为子代理单独收紧积分预算上限，不超过父当前可划份额"},
	}, "name", "role", "objective")
	c.add("wait_subagents", "Wait until the parent Agent's active children report a result.", map[string]any{})
	c.add("message_subagent", "Send a bounded instruction or clarification to one child Agent.", map[string]any{
		"linkId": map[string]any{"type": "string"}, "text": map[string]any{"type": "string", "maxLength": 4000},
	}, "linkId", "text")
	c.add("subagent_status", "Read the current status of the parent Agent's children.", map[string]any{})
}

// addSubagentChildTools adds child-to-parent tools and removes tools a child may
// not call. 写型子代理（WriteGranted）保留画布写入与生成工具，其余照旧剔除。
func (c *cloudAgentToolCompiler) addSubagentChildTools(writeGranted bool) {
	c.add("send_parent_message", "Send a structured progress, question or result message to the parent Agent.", map[string]any{
		"kind": map[string]any{"type": "string", "enum": []any{"progress", "question", "partial_result", "final_result", "error"}},
		"text": map[string]any{"type": "string", "maxLength": 4000},
	}, "kind", "text")
	c.add("finish_subagent", "Finish this child Agent task and report the final result to the parent.", map[string]any{
		"summary": map[string]any{"type": "string", "maxLength": 4000},
	}, "summary")
	filtered := c.tools[:0]
	for _, tool := range c.tools {
		name := stringField(tool["function"].(map[string]any), "name")
		if name == "finish_run" || name == "ask_user" || name == "spawn_subagent" || name == "wait_subagents" || name == "message_subagent" || name == "subagent_status" {
			continue
		}
		if !writeGranted && cloudAgentWrite(name) {
			continue
		}
		filtered = append(filtered, tool)
	}
	c.tools = filtered
}
