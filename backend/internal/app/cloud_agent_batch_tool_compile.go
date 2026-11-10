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
	c.add("generate_media_batch", "一次审批批量生成同一模式的占位节点：items 传节点ID，提示词与参考连线取自节点。一张审批卡列明细、合计积分、并发与重试额度；批准后后台并发提交、逐条回写，结束后回执交回你。先用相同参数调 canvas_preflight；视频依赖关键帧时先跑关键帧批次。", batch, "mode", "items")
	c.add("media_batch_control", "在已批准的批次额度内操作，不再审批：continue 放行试跑后的其余条目；retry 重跑已结束条目（可改 prompt，规格不变，成功条目在旁新建节点）；cancel 取消未提交条目；status 查看进度。", map[string]any{
		"action": map[string]any{"type": "string", "enum": []string{"continue", "retry", "cancel", "status"}},
		"items": map[string]any{"type": "array", "maxItems": cloudAgentMediaBatchMaxItems, "items": map[string]any{
			"type": "object", "properties": map[string]any{
				"index": map[string]any{"type": "integer", "minimum": 0}, "nodeId": str("条目节点ID"), "prompt": str("可选：新提示词"),
			}, "additionalProperties": false,
		}},
	}, "action")
	c.add("canvas_build_board", "按分镜一次搭好制作看板：总控说明、资产三列（人物/场景/道具占位）、每行关键帧与视频占位、按幕着色的背板，以及资产→关键帧、行→关键帧/视频、关键帧→视频首帧连线。只建占位不收费；提示词取行的 imageGenerationPrompt/videoMotionPrompt，缺省 plotDescription。", map[string]any{
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
	c.add("canvas_preflight", "批量生成前只读体检，参数同 generate_media_batch：检查节点能否提交、提示词、参考就绪与 @标签；带模型时再做无扣费准入，核对能力、报价和剩余预算。返回失败/警告数与逐条问题。", batchProperties(), "mode", "items")
}
