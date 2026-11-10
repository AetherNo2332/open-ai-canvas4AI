package app

func (c *cloudAgentToolCompiler) addBatchProductionTools() {
	str := cloudAgentToolStringSchema
	batchProperties := func() map[string]any {
		return map[string]any{
			"mode":           map[string]any{"type": "string", "enum": cloudAgentGenerationModeNames()},
			"logicalModelId": str("同 generate_media"), "channelId": str("同 generate_media"), "channelModelKey": str("同 generate_media"),
			"size": str("同 generate_media"), "quality": str("同 generate_media"), "imageResolution": str("同 generate_media"),
			"durationSeconds":    map[string]any{"type": "integer", "minimum": 0},
			"videoGenerateAudio": map[string]any{"type": "boolean"},
			"items": map[string]any{"type": "array", "minItems": 1, "maxItems": cloudAgentMediaBatchMaxItems, "items": map[string]any{
				"type": "object", "properties": map[string]any{
					"nodeId": str("空闲占位节点ID"), "prompt": str("可选：覆盖节点提示词"), "durationSeconds": map[string]any{"type": "integer", "minimum": 0},
				}, "required": []string{"nodeId"}, "additionalProperties": false,
			}},
		}
	}
	batch := batchProperties()
	batch["concurrency"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "默认3，受账号同时任务上限约束"}
	batch["canary"] = map[string]any{"type": "boolean", "description": "先试跑第1条，回执后用 media_batch_control 放行"}
	batch["maxRetriesPerItem"] = map[string]any{"type": "integer", "minimum": 0, "maximum": cloudAgentMediaBatchMaxRetries, "description": "默认1"}
	c.add("generate_media_batch", cloudAgentToolText("generate_media_batch"), batch, "mode", "items")
	c.add("media_batch_control", cloudAgentToolText("media_batch_control"), map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"continue", "retry", "cancel", "status"}},
		"items": map[string]any{"type": "array", "maxItems": cloudAgentMediaBatchMaxItems, "items": map[string]any{
			"type": "object", "properties": map[string]any{
				"index": map[string]any{"type": "integer", "minimum": 0}, "nodeId": str("条目节点ID"), "prompt": str("可选：新提示词"),
			}, "additionalProperties": false,
		}},
	}, "action")
	c.add("canvas_build_board", cloudAgentToolText("canvas_build_board"), map[string]any{
		"snapshotHash":     str("最近一次画布读取的 snapshotHash"),
		"storyboardNodeId": str("分镜脚本节点ID"),
		"overview":         str("可选：总控 Markdown"),
		"assets": map[string]any{"type": "array", "maxItems": cloudAgentBoardMaxAssets, "items": map[string]any{
			"type": "object", "properties": map[string]any{
				"id": str("新节点ID"), "category": map[string]any{"type": "string", "enum": []string{"character", "environment", "prop"}},
				"title": str("资产名"), "prompt": str("资产图提示词"),
				"rowIds": map[string]any{"type": "array", "maxItems": maxCloudAgentStoryboardRows, "items": str("用到它的 rowId")},
			}, "required": []string{"id", "category", "title", "prompt"}, "additionalProperties": false,
		}},
		"keyframes": map[string]any{"type": "boolean", "description": "默认 true"},
		"videos":    map[string]any{"type": "boolean", "description": "默认 true"},
		"acts": map[string]any{"type": "array", "maxItems": cloudAgentBoardMaxActs, "items": map[string]any{
			"type": "object", "properties": map[string]any{
				"label":  str("幕名"),
				"rowIds": map[string]any{"type": "array", "minItems": 1, "maxItems": maxCloudAgentStoryboardRows, "items": str("rowId")},
			}, "required": []string{"label", "rowIds"}, "additionalProperties": false,
		}},
	}, "snapshotHash", "storyboardNodeId")
	c.add("canvas_preflight", cloudAgentToolText("canvas_preflight"), batchProperties(), "mode", "items")
}
