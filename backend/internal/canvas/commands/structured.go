package commands

import (
	"fmt"
	"infinite-canvas/backend/internal/canvas/capability"
	"infinite-canvas/backend/internal/canvas/connection"
	"infinite-canvas/backend/internal/canvas/references"
	"strings"
	"unicode/utf8"
)

func (p *planner) applyOperation(doc Document, op Operation, ctx ValidationContext) ([]OperationSummary, error) {
	if err := validateCanvasID(op.ID, "操作目标 ID", 80); err != nil {
		return nil, err
	}
	if op.Type == "connect_nodes" || op.Type == "update_connection" {
		if err := p.validateNativeConnection(doc, op); err != nil {
			return nil, err
		}
		edges := []map[string]any{}
		for _, edge := range maps(doc["connections"]) {
			if op.Type == "update_connection" && text(edge["id"]) == op.ID {
				continue
			}
			edges = append(edges, edge)
		}
		if err := p.validateCanvasCanvasConnection(maps(doc["nodes"]), edges, op); err != nil {
			return nil, err
		}
		nodes := p.canvasConnectionNodes(maps(doc["nodes"]))
		existing := []connection.Edge{}
		for _, edge := range edges {
			existing = append(existing, p.canvasConnectionEdge(edge))
		}
		mode := ""
		for _, n := range nodes {
			if n.ID == op.ToNodeID {
				mode = connection.Mode(n)
			}
		}
		if limits, ok := ctx.ModelInputLimits[mode]; ok {
			counts := connection.Inputs(nodes, existing, connection.Edge{From: op.FromNodeID, To: op.ToNodeID, FromHandle: op.FromHandleID, ToHandle: op.ToHandleID})
			for kind, count := range map[string]int{"image": counts.Image + counts.Character, "video": counts.Video, "audio": counts.Audio} {
				if count > limits[kind] {
					return nil, canvasFieldError("operations", "invalid_connection", fmt.Sprintf("%s 模型最多支持 %d 个 %s 参考输入", mode, limits[kind], kind))
				}
			}
		}
	}
	switch op.Type {
	case "duplicate_node":
		return p.duplicateNativeNode(doc, op)
	case "select_node_version":
		if err := p.selectNodeVersion(doc, op); err != nil {
			return nil, err
		}
	case "edit_timeline":
		if op.ID != text(doc["id"]) {
			return nil, BadAuthRequest("时间线画布目标无效")
		}
		if err := p.editTimeline(doc, op, ctx); err != nil {
			return nil, err
		}
	case "connect_nodes", "update_connection", "delete_connection":
		return p.applyNativeConnection(doc, op)
	case "edit_canvas_settings":
		if op.ID != text(doc["id"]) {
			return nil, BadAuthRequest("画布目标 ID 无效")
		}
		if err := editCanvasSettings(doc, op.Patch); err != nil {
			return nil, err
		}
	case "reorder_connections":
		if op.ID != text(doc["id"]) {
			return nil, BadAuthRequest("画布目标 ID 无效")
		}
		next, err := p.canvasReorderItems(maps(doc["connections"]), op.NodeIDs)
		if err != nil {
			return nil, err
		}
		doc["connections"] = p.mapsAsAny(next)
	case "rename_canvas":
		if op.ID != text(doc["id"]) || op.Title == nil || utf8.RuneCountInString(*op.Title) > 240 || strings.TrimSpace(*op.Title) == "" {
			return nil, BadAuthRequest("画布标题或目标 ID 无效")
		}
		doc["title"] = *op.Title
	case "delete_canvas":
		if op.ID != text(doc["id"]) {
			return nil, BadAuthRequest("画布目标 ID 无效")
		}
		doc["_deleted"] = true
	case "edit_drawing":
		if err := p.applyNativeDrawing(doc, op); err != nil {
			return nil, err
		}
	case "edit_storyboard":
		if err := p.editStoryboard(doc, op); err != nil {
			return nil, err
		}
	case "bind_storyboard_output":
		if err := references.BindStoryboardOutput(doc, op.ID, op.RowID, op.OutputMode, op.SourceNodeID); err != nil {
			return nil, err
		}
	case "edit_batch_table":
		if err := p.editBatchTable(doc, op); err != nil {
			return nil, err
		}
	case "create_previs_scene", "edit_previs_scene", "delete_previs_scene":
		var err error
		if len(op.PrevisEntityOperations) > 0 || op.Action != "" {
			err = p.editNativePrevis(doc, op, ctx)
		} else {
			err = p.editPrevis(doc, op)
		}
		if err != nil {
			return nil, err
		}
	case "bind_asset":
		if err := p.bindAsset(doc, op, ctx); err != nil {
			return nil, err
		}
	default:
		return p.applyCanvasCanvasPlanInPlace(doc, []Operation{op})
	}
	return []OperationSummary{{Operation: op.Type, NodeID: op.ID, Summary: "画布内容已更新"}}, nil
}
func (p *planner) editStoryboard(doc Document, op Operation) error {
	_, board, rows, err := p.storyboardNodeFromDocument(doc, op.ID)
	if err != nil {
		return err
	}
	if op.Action == "set_references" {
		field := capability.PatchField{Kind: "array", Items: &capability.PatchField{Kind: "string", MaxRunes: 120}, MaxItems: 100}
		if op.RowID != "" || len(op.Patch) != 1 {
			return BadAuthRequest("共享引用只能使用 referenceNodeIds")
		}
		if err := capability.ValidateEditableFields(map[string]capability.PatchField{"referenceNodeIds": field}, op.Patch); err != nil {
			return BadAuthRequest(err.Error())
		}
		board["referenceNodeIds"] = op.Patch["referenceNodeIds"]
		_, err := references.Resolve(doc, references.Scope{Kind: "storyboard-context", NodeID: op.ID}, p.registry)
		return err
	}
	index := p.canvasNodeIndex(rows, op.RowID)
	switch op.Action {
	case "append", "update":
		if err := validateCanvasID(op.RowID, "分镜行 ID", 120); err != nil {
			return err
		}
		if len(op.Patch) == 0 {
			return BadAuthRequest("分镜操作需要 patch")
		}
		if err := capability.ValidateEditableFields(capability.StoryboardRowFields(), op.Patch); err != nil {
			return BadAuthRequest(err.Error())
		}
		if err := p.validateCanvasStoryboardReferences(doc, op.Patch); err != nil {
			return err
		}
		if op.Action == "append" {
			if index >= 0 {
				return BadAuthRequest("分镜行 ID 重复")
			}
			if len(rows) >= maxCanvasStoryboardRows {
				return BadAuthRequest("单个分镜表最多 100 个镜头")
			}
			if err := p.validateCanvasStoryboardRow(op.Patch, true); err != nil {
				return err
			}
			row := p.canvasStoryboardRowDefaults()
			for key, value := range op.Patch {
				row[key] = value
			}
			row["id"] = op.RowID
			rows = append(rows, row)
		} else {
			if index < 0 {
				return failure("entity_deleted", "分镜行不存在")
			}
			candidate := p.mergeCreationMaps(rows[index], op.Patch)
			if err := p.validateCanvasStoryboardSourceRange(candidate); err != nil {
				return err
			}
			rows[index] = candidate
		}
	case "remove":
		if index < 0 {
			return failure("entity_deleted", "分镜行不存在")
		}
		if len(op.Patch) != 0 {
			return BadAuthRequest("删除分镜行不接受 patch")
		}
		rows = append(rows[:index:index], rows[index+1:]...)
	default:
		return BadAuthRequest("分镜操作必须是 append、update 或 remove")
	}
	for i, row := range rows {
		row["shotNumber"] = float64(i + 1)
	}
	board["rows"] = p.mapsAsAny(rows)
	return nil
}
func (p *planner) editBatchTable(doc Document, op Operation) error {
	_, table, rows, columns, err := p.batchTableNodeFromDocument(doc, op.ID)
	if err != nil {
		return err
	}
	index := p.canvasNodeIndex(rows, op.RowID)
	switch op.Action {
	case "append", "update":
		if err := validateCanvasID(op.RowID, "批量创作行 ID", 120); err != nil {
			return err
		}
		if op.Action == "update" && len(op.Patch) == 0 {
			return BadAuthRequest("修改批量创作行必须提供 patch")
		}
		patch, _, err := p.validateCanvasBatchRowPatch(doc, op.Patch, len(columns), table)
		if err != nil {
			return err
		}
		if op.Action == "append" {
			if index >= 0 {
				return BadAuthRequest("批量创作行 ID 重复")
			}
			if len(rows) >= maxCanvasBatchRows {
				return BadAuthRequest("单个批量创作表最多 500 行")
			}
			row := map[string]any{"id": op.RowID, "enabled": true, "prompt": "", "inputNodeIds": []any{}, "status": "idle"}
			for key, value := range patch {
				row[key] = value
			}
			rows = append(rows, row)
		} else {
			if index < 0 {
				return failure("entity_deleted", "批量创作行不存在")
			}
			rows[index] = p.mergeCreationMaps(rows[index], patch)
		}
		table["rows"] = p.mapsAsAny(rows)
	case "remove":
		if index < 0 {
			return failure("entity_deleted", "批量创作行不存在")
		}
		if len(op.Patch) != 0 || op.BatchOperation != "" || op.Concurrency != 0 {
			return BadAuthRequest("删除行不接受其他修改参数")
		}
		rows = append(rows[:index:index], rows[index+1:]...)
		table["rows"] = p.mapsAsAny(rows)
	case "set_operation":
		if op.BatchOperation != "try_on" && op.BatchOperation != "creative" {
			return BadAuthRequest("任务类型必须是 try_on 或 creative")
		}
		if op.RowID != "" || len(op.Patch) != 0 || op.Concurrency != 0 {
			return BadAuthRequest("任务类型不接受行级参数")
		}
		table["operation"] = op.BatchOperation
	case "set_concurrency":
		if !p.canvasBatchConcurrencyAllowed(op.Concurrency) {
			return BadAuthRequest("并发数必须是 1、5 或 10")
		}
		if op.RowID != "" || len(op.Patch) != 0 || op.BatchOperation != "" {
			return BadAuthRequest("并发设置不接受行级参数")
		}
		table["concurrency"] = float64(op.Concurrency)
	case "add_reference_column":
		if len(columns) >= maxCanvasBatchReferences {
			return BadAuthRequest("最多 10 组参考图")
		}
		if err := validateCanvasID(op.ColumnID, "参考图列 ID", 120); err != nil {
			return err
		}
		if p.canvasNodeIndex(columns, op.ColumnID) >= 0 {
			return BadAuthRequest("参考图列 ID 重复")
		}
		columns = append(columns, map[string]any{"id": op.ColumnID, "label": fmt.Sprintf("参考图 %d", len(columns)+1)})
		table["referenceColumns"] = p.mapsAsAny(columns)
	case "remove_reference_column":
		if len(columns) <= 1 {
			return BadAuthRequest("至少保留 1 组参考图")
		}
		idx := p.canvasNodeIndex(columns, op.ColumnID)
		if idx < 0 {
			return failure("entity_deleted", "参考图列不存在")
		}
		removed := text(columns[idx]["id"])
		columns = append(columns[:idx:idx], columns[idx+1:]...)
		for i, column := range columns {
			column["label"] = fmt.Sprintf("参考图 %d", i+1)
		}
		for _, row := range rows {
			inputs, _ := row["inputNodeIds"].([]any)
			if idx < len(inputs) {
				inputs = append(inputs[:idx:idx], inputs[idx+1:]...)
			}
			row["inputNodeIds"] = inputs
			if cells, ok := row["cells"].(map[string]any); ok {
				delete(cells, removed)
			}
		}
		table["referenceColumns"], table["rows"] = p.mapsAsAny(columns), p.mapsAsAny(rows)
		// Remove graph handles for this column together with their entity changes.
		kept := []map[string]any{}
		for _, edge := range maps(doc["connections"]) {
			if text(edge["toNodeId"]) == op.ID && text(edge["toHandleId"]) == "batch-reference:"+removed {
				continue
			}
			kept = append(kept, edge)
		}
		doc["connections"] = kept
	case "set_global_prompt":
		if utf8.RuneCountInString(op.GlobalPrompt) > maxCanvasBatchPromptRunes {
			return BadAuthRequest("全局提示词超过 20000 字")
		}
		if op.RowID != "" || len(op.Patch) != 0 || op.BatchOperation != "" || op.Concurrency != 0 {
			return BadAuthRequest("全局提示词不接受其他修改参数")
		}
		if strings.TrimSpace(op.GlobalPrompt) == "" {
			delete(table, "globalPrompt")
		} else {
			table["globalPrompt"] = op.GlobalPrompt
		}
	default:
		return BadAuthRequest("批量创作表操作无效")
	}
	return nil
}
func (p *planner) editPrevis(doc Document, op Operation) error {
	scenes := maps(doc["previsScenes"])
	index := p.canvasNodeIndex(scenes, op.ID)
	switch op.Type {
	case "create_previs_scene":
		if index >= 0 {
			return BadAuthRequest("预演场景 ID 重复")
		}
		if len(scenes) >= canvasPrevisMaxScenes {
			return BadAuthRequest("预演场景数量超限")
		}
		title := "未命名场景"
		if op.Title != nil {
			title = *op.Title
		}
		scene, err := p.canvasPrevisSceneCreateTemplate(canvasPrevisSceneCreateArgs{SceneID: op.ID, Title: title, TemplateID: op.TemplateID})
		if err != nil {
			return err
		}
		scenes = append(scenes, scene)
	case "edit_previs_scene":
		if index < 0 {
			return failure("entity_deleted", "预演场景不存在")
		}
		if len(op.PrevisOperations) < 1 || len(op.PrevisOperations) > canvasPrevisMaxOperations {
			return BadAuthRequest("预演操作需包含 1 至 32 项")
		}
		for i, patch := range op.PrevisOperations {
			if err := p.canvasPrevisValidateCharacterBindingInCanvas(doc, patch); err != nil {
				return err
			}
			if _, err := p.canvasPrevisApplyPatchOperation(scenes[index], patch, i); err != nil {
				return err
			}
		}
		if err := p.canvasPrevisValidateScene(scenes[index]); err != nil {
			return err
		}
	case "delete_previs_scene":
		if index < 0 {
			return failure("entity_deleted", "预演场景不存在")
		}
		scenes = append(scenes[:index:index], scenes[index+1:]...)
	}
	if err := p.canvasPrevisScenesSize(scenes); err != nil {
		return err
	}
	doc["previsScenes"] = scenes
	return nil
}
func (p *planner) bindAsset(doc Document, op Operation, ctx ValidationContext) error {
	node := entity(doc, "node:"+op.ID)
	if node == nil {
		return failure("entity_deleted", "绑定节点不存在")
	}
	descriptor, ok := p.canvasNodeCapabilityForNode(node)
	if !ok || descriptor.Variant != nil {
		return BadAuthRequest("角色卡或未知节点不能替换为普通素材")
	}
	kind := text(node["type"])
	if kind != "image" && kind != "video" && kind != "audio" && kind != "text" {
		return BadAuthRequest("节点不支持素材替换")
	}
	if ctx.Asset == nil {
		return failure("resource_not_owned", "未提供已授权素材")
	}
	asset, err := ctx.Asset(op.AssetID)
	if err != nil {
		return err
	}
	if asset.ID != op.AssetID || asset.Kind != kind || asset.Status == "archived" {
		return BadAuthRequest("素材类型不匹配或已归档")
	}
	metadata := p.canvasNodeMetadata(node)
	switch text(metadata["status"]) {
	case "loading", "running", "pending":
		return BadAuthRequest("等待节点任务结束后替换素材")
	}
	if kind == "text" {
		if utf8.RuneCountInString(asset.Content) > 1<<20 {
			return BadAuthRequest("文本素材内容过长")
		}
		metadata["content"] = asset.Content
		delete(metadata, "richText")
	} else {
		if asset.ResourceID == "" || ctx.ResourceAllowed == nil || !ctx.ResourceAllowed(asset.ResourceID) {
			return failure("resource_not_owned", "素材资源未授权")
		}
		p.canvasResetCopiedMetadata(metadata, "", nil)
		for _, field := range []string{"generationContinuation", "generationSubmitted", "generationEffect", "generationEffects", "generationEffectKeys", "producedModel", "previewContent", "previewStorageKey", "videoPreview", "durationMs", "hasAudio", "naturalWidth", "naturalHeight", "intrinsicWidth", "intrinsicHeight", "bytes", "mimeType", "taskId", "taskStatus", "taskProgress", "taskError", "generationAppliedKeys", "generationPersistence", "errorDetails"} {
			delete(metadata, field)
		}
		metadata["content"], metadata["storageKey"], metadata["status"] = "/api/resources/"+asset.ResourceID+"/file", "resource:"+asset.ResourceID, "success"
		metadata["mimeType"], metadata["bytes"] = asset.MimeType, asset.Size
		if asset.Width > 0 {
			metadata["naturalWidth"] = asset.Width
		}
		if asset.Height > 0 {
			metadata["naturalHeight"] = asset.Height
		}
		if asset.DurationMs > 0 {
			metadata["durationMs"] = asset.DurationMs
		}
	}
	metadata["assetId"] = asset.ID
	return nil
}
