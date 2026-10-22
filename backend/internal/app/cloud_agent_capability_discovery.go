package app

// cloudAgentReferenceContractNotes 是模型可见的能力回执里唯一的连线权威说明：
// 关闭 Agent 连线操作后，连接是服务端按引用字段派生的结果，不再有可调用的边操作。
// 手动画布能力仍属于用户侧界面，Agent 只声明输入，不维护边。
func cloudAgentReferenceContractNotes() []string {
	return []string{
		"connections 是服务端按引用字段派生的只读结果；Agent 没有连接节点、改接或断开连线的操作，调用 connect_nodes／update_connection／delete_connection 会被拒绝并提示改用字段。",
		"分镜共享输入写 storyboard.referenceNodeIds（canvas_create_storyboard 顶层或 canvas_edit_storyboard 的 set_references）。",
		"分镜行素材写该行 assetBindings；镜头产物归属用 canvas_edit_storyboard 的 bind_output／unbind_output 声明已有产物，不重写产物内容。",
		"characters 中的 characterImageNodeId／characterAssetId 也属于引用字段，不能绕过历史画布迁移门禁；只有角色名字或描述不代表关联资产。",
		"普通媒体草稿写 generationSpec.referenceBindings；compare、panorama、config 等无生成合同的输入节点写 metadata.inputBindings；引用元素沿用同一个 ReferenceBinding 结构。",
		"托管归属标记（managedBy／referenceOwnerNodeId／referenceBindingId）只由服务端写入，通用 patch 不接受 connections 或这些字段。",
	}
}

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
		// 只与连线操作有关的 canGraphSource／canGraphTarget 已从能力回执删除：留着它们等于
		// 暗示模型还能连节点。是否可作为引用输入仍由 canReference 与接受输入类型表达。
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
			item["contentHint"] = "绘图内容由画布编辑器维护；需要图像理解时使用已保存的参考图，需要新图或视觉编辑时使用 generate_media。"
		}
		filtered = append(filtered, item)
	}
	if nodeType != "" && len(filtered) == 0 {
		return nil, BadAuthRequest("节点类型未注册")
	}
	result["nodes"] = filtered
	result["referenceContract"] = cloudAgentReferenceContractNotes()
	return result, nil
}
