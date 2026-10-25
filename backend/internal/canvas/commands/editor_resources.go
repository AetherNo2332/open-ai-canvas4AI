package commands

import (
	"infinite-canvas/backend/internal/assets"
	"strings"
)

func (p *planner) bindResource(doc Document, op Operation, ctx ValidationContext) error {
	node := entity(doc, "node:"+op.ID)
	if node == nil {
		return failure("entity_deleted", "绑定节点不存在")
	}
	descriptor, ok := p.canvasNodeCapabilityForNode(node)
	if !ok || descriptor.Variant != nil {
		return BadAuthRequest("角色卡或未知节点不能替换为普通资源")
	}
	resource, ok := ctx.Resources[op.ResourceID]
	if !ok || assets.ValidID(op.ResourceID) == "" || resource.ID != op.ResourceID || ctx.ResourceAllowed == nil || !ctx.ResourceAllowed(op.ResourceID) {
		return failure("resource_not_owned", "资源不存在、未就绪或未授权")
	}
	kind := text(node["type"])
	if kind != "image" && kind != "video" && kind != "audio" && kind != "text" {
		return BadAuthRequest("节点不支持资源绑定")
	}
	if kind == "text" {
		if resource.Kind != "file" || (resource.MimeType != "text/plain" && resource.MimeType != "text/markdown") {
			return BadAuthRequest("文本节点需要文本文件资源")
		}
	} else if resource.Kind != kind || !strings.HasPrefix(resource.MimeType, kind+"/") {
		return BadAuthRequest("资源类型与节点不匹配")
	}
	metadata := p.canvasNodeMetadata(node)
	switch text(metadata["status"]) {
	case "loading", "running", "pending":
		return BadAuthRequest("等待节点任务结束后替换资源")
	}
	if kind != "text" {
		p.canvasResetCopiedMetadata(metadata, "", nil)
		for _, key := range []string{"assetId", "copiedFromNodeId", "videoPreview", "previewContent", "previewStorageKey", "naturalWidth", "naturalHeight", "durationMs", "hasAudio", "producedModel", "generationContinuation", "generationSubmitted", "generationEffect", "generationEffects", "generationEffectKeys"} {
			delete(metadata, key)
		}
		metadata["content"] = assets.FileURL(resource.ID)
		if resource.Width > 0 {
			metadata["naturalWidth"] = resource.Width
		}
		if resource.Height > 0 {
			metadata["naturalHeight"] = resource.Height
		}
		if resource.DurationMs > 0 {
			metadata["durationMs"] = resource.DurationMs
		}
	}
	metadata["storageKey"], metadata["bytes"], metadata["mimeType"], metadata["status"] = "resource:"+resource.ID, resource.Size, resource.MimeType, "success"
	return nil
}

func (p *planner) validateBatchConfigurationReferences(doc Document, table map[string]any) error {
	columns := map[string]bool{}
	for _, field := range []string{"referenceColumns", "textColumns"} {
		for _, column := range maps(table[field]) {
			id := text(column["id"])
			if validateCanvasID(id, "列 ID", 120) != nil || columns[id] || strings.TrimSpace(text(column["label"])) == "" {
				return BadAuthRequest("表格列无效或重复")
			}
			columns[id] = true
		}
	}
	inputs, _ := table["referenceColumnInputs"].(map[string]any)
	for column, raw := range inputs {
		found := false
		for _, ref := range maps(table["referenceColumns"]) {
			if text(ref["id"]) == column {
				found = true
			}
		}
		if !found {
			return BadAuthRequest("列输入引用了不存在的参考图列")
		}
		if err := p.validateCanvasBatchInputNodes(doc, p.canvasStrings(raw)); err != nil {
			return err
		}
	}
	return nil
}
