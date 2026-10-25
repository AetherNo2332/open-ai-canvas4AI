package commands

func (p *planner) editImageBatch(doc Document, op Operation) error {
	root := entity(doc, "node:"+op.ID)
	if root == nil || text(root["type"]) != "image" {
		return BadAuthRequest("图片批次根节点不存在")
	}
	metadata := p.canvasNodeMetadata(root)
	switch op.Action {
	case "set_children":
		if len(op.NodeIDs) > 100 || text(metadata["batchRootId"]) != "" {
			return BadAuthRequest("图片批次大小或层级无效")
		}
		seen := map[string]bool{}
		for _, id := range op.NodeIDs {
			child := entity(doc, "node:"+id)
			if child == nil || text(child["type"]) != "image" || id == op.ID || seen[id] {
				return BadAuthRequest("图片批次子节点无效或重复")
			}
			cm := p.canvasNodeMetadata(child)
			if owner := text(cm["batchRootId"]); owner != "" && owner != op.ID {
				return BadAuthRequest("图片已属于其他批次")
			}
			if len(stringList(cm["batchChildIds"])) > 0 {
				return BadAuthRequest("图片批次不能嵌套")
			}
			seen[id] = true
		}
		for _, node := range maps(doc["nodes"]) {
			cm, _ := node["metadata"].(map[string]any)
			if text(cm["batchRootId"]) == op.ID && !seen[text(node["id"])] {
				delete(cm, "batchRootId")
			}
		}
		if len(op.NodeIDs) == 0 {
			for _, key := range []string{"isBatchRoot", "batchChildIds", "primaryImageId", "batchFailedCount", "batchUsesReferenceImages", "imageBatchExpanded"} {
				delete(metadata, key)
			}
		} else {
			ids := []any{}
			for _, id := range op.NodeIDs {
				ids = append(ids, id)
				p.canvasNodeMetadata(entity(doc, "node:"+id))["batchRootId"] = op.ID
			}
			metadata["isBatchRoot"], metadata["batchChildIds"] = true, ids
			if id := text(metadata["primaryImageId"]); id != "" && !seen[id] {
				delete(metadata, "primaryImageId")
			}
		}
	case "select_primary":
		child := entity(doc, "node:"+op.SourceNodeID)
		if child == nil || text(child["type"]) != "image" || text(p.canvasNodeMetadata(child)["batchRootId"]) != op.ID {
			return BadAuthRequest("主图不属于当前图片批次")
		}
		found := false
		for _, id := range stringList(metadata["batchChildIds"]) {
			if id == op.SourceNodeID {
				found = true
			}
		}
		if !found {
			return BadAuthRequest("图片不在当前批次中")
		}
		cm := p.canvasNodeMetadata(child)
		if text(cm["content"]) == "" && text(cm["storageKey"]) == "" {
			return BadAuthRequest("主图尚无可用媒体")
		}
		for _, key := range []string{"content", "storageKey", "assetId", "status", "naturalWidth", "naturalHeight", "bytes", "mimeType", "errorDetails", "generationErrorCode", "failedPromptFingerprint", "freeResize"} {
			if value, ok := cm[key]; ok {
				metadata[key] = value
			} else {
				delete(metadata, key)
			}
		}
		root["width"], root["height"], metadata["primaryImageId"] = child["width"], child["height"], op.SourceNodeID
	default:
		return BadAuthRequest("图片批次操作无效")
	}
	return nil
}
