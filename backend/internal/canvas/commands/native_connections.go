package commands

import (
	"math"
	"strings"
)

func (p *planner) nativeConnectionRow(doc Document, op Operation) (map[string]any, string, error) {
	from, to := entity(doc, "node:"+op.FromNodeID), entity(doc, "node:"+op.ToNodeID)
	if from == nil || to == nil {
		return nil, "", BadAuthRequest("连线端点不存在")
	}
	switch op.Relation {
	case "":
		if op.StoryboardRowID != "" {
			return nil, "", BadAuthRequest("行绑定需要明确的连线关系")
		}
	case "batch-output":
		if text(from["type"]) != "batch-table" || text(to["type"]) != "image" || op.FromHandleID != "" || op.ToHandleID != "" {
			return nil, "", BadAuthRequest("批量输出连线端点无效")
		}
		table, _ := p.canvasNodeMetadata(from)["batchTable"].(map[string]any)
		for _, row := range maps(table["rows"]) {
			if text(row["id"]) == op.StoryboardRowID {
				return row, "outputNodeId", nil
			}
		}
		return nil, "", BadAuthRequest("批量输出行不存在")
	case "storyboard-output":
		row := p.canvasConnectionRow(from, op.StoryboardRowID)
		kind := text(to["type"])
		if text(from["type"]) != "script" || row == nil || op.FromHandleID != "row:"+op.StoryboardRowID || op.ToHandleID != "" || (kind != "image" && kind != "video") {
			return nil, "", BadAuthRequest("分镜输出连线无效")
		}
		return row, kind + "NodeId", nil
	case "storyboard-asset-reference":
		if op.StoryboardRowID == "" || op.FromHandleID != "" || op.ToHandleID != "" {
			return nil, "", BadAuthRequest("分镜素材引用无效")
		}
		matches := 0
		for _, node := range maps(doc["nodes"]) {
			if text(node["type"]) != "script" {
				continue
			}
			row := p.canvasConnectionRow(node, op.StoryboardRowID)
			if row != nil && (text(row["imageNodeId"]) == op.ToNodeID || text(row["videoNodeId"]) == op.ToNodeID) {
				matches++
			}
		}
		if matches != 1 {
			return nil, "", BadAuthRequest("分镜素材引用必须指向唯一关联输出")
		}
	case "batch-input":
		if text(to["type"]) != "batch-table" || op.StoryboardRowID != "" {
			return nil, "", BadAuthRequest("批量输入连线无效")
		}
	default:
		return nil, "", BadAuthRequest("不支持的连线关系")
	}
	return nil, "", nil
}
func (p *planner) validateNativeConnection(doc Document, op Operation) error {
	if strings.HasPrefix(op.ToHandleID, "batch-text:") {
		node := entity(doc, "node:"+op.ToNodeID)
		if node == nil || text(node["type"]) != "batch-table" {
			return BadAuthRequest("文字列连接目标无效")
		}
		table, _ := p.canvasNodeMetadata(node)["batchTable"].(map[string]any)
		found := false
		for _, column := range maps(table["textColumns"]) {
			if text(column["id"]) == strings.TrimPrefix(op.ToHandleID, "batch-text:") {
				found = true
			}
		}
		if !found {
			return BadAuthRequest("批量表文字列不存在")
		}
	}
	for _, ratio := range []*float64{op.FromAnchorRatio, op.ToAnchorRatio} {
		if ratio != nil && (math.IsNaN(*ratio) || math.IsInf(*ratio, 0) || *ratio < 0 || *ratio > 1) {
			return BadAuthRequest("连线锚点比例必须介于 0 和 1")
		}
	}
	row, field, err := p.nativeConnectionRow(doc, op)
	if err != nil {
		return err
	}
	if row != nil && text(row[field]) != "" && text(row[field]) != op.ToNodeID {
		return BadAuthRequest("该行已绑定另一输出，请先移除旧连线")
	}
	return nil
}

// Endpoint changes retain unknown extensions, while omitted known optional
// fields are cleared. Row bindings are derived here, never accepted as task state.
func (p *planner) applyNativeConnection(doc Document, op Operation) ([]OperationSummary, error) {
	old := entity(doc, "connection:"+op.ID)
	if op.Type == "update_connection" && old == nil {
		return nil, BadAuthRequest("修改连线不存在")
	}
	p.detachNativeFields(doc, old)
	var items []OperationSummary
	var err error
	if strings.HasPrefix(op.ToHandleID, "batch-text:") && op.Type != "delete_connection" {
		items, err = p.applyNativeBatchTextEdge(doc, op, old)
	} else {
		items, err = p.applyCanvasCanvasPlanInPlace(doc, []Operation{op})
	}
	if err != nil {
		return nil, err
	}
	if old != nil {
		p.detachNativeOutput(doc, old)
	}
	if op.Type == "delete_connection" {
		return items, nil
	}
	edge := entity(doc, "connection:"+op.ID)
	if old != nil {
		for key, value := range old {
			switch key {
			case "id", "fromNodeId", "toNodeId", "fromHandleId", "toHandleId", "fromAnchorRatio", "toAnchorRatio", "relation", "storyboardRowId":
			default:
				edge[key] = value
			}
		}
	}
	if op.FromAnchorRatio != nil {
		edge["fromAnchorRatio"] = *op.FromAnchorRatio
	}
	if op.ToAnchorRatio != nil {
		edge["toAnchorRatio"] = *op.ToAnchorRatio
	}
	if op.Relation != "" {
		edge["relation"] = op.Relation
	}
	if op.StoryboardRowID != "" {
		edge["storyboardRowId"] = op.StoryboardRowID
	}
	row, field, err := p.nativeConnectionRow(doc, op)
	if err != nil {
		return nil, err
	}
	if row != nil {
		row[field] = op.ToNodeID
	}
	p.attachNativeFields(doc, op)
	return items, nil
}

func (p *planner) detachNativeFields(doc Document, edge map[string]any) {
	if edge == nil {
		return
	}
	p.detachBatchTextColumn(doc, edge)
	p.detachStoryboardAsset(doc, edge)
}

func (p *planner) attachNativeFields(doc Document, op Operation) {
	if op.Type == "delete_connection" {
		return
	}
	p.attachBatchTextColumn(doc, op.ToNodeID, op.ToHandleID, op.FromNodeID)
	p.attachStoryboardAsset(doc, op)
}

func (p *planner) applyNativeBatchTextEdge(doc Document, op Operation, old map[string]any) ([]OperationSummary, error) {
	edges := maps(doc["connections"])
	if op.Type == "connect_nodes" && old != nil {
		return nil, BadAuthRequest("连线 ID 重复")
	}
	edge := map[string]any{"id": op.ID, "fromNodeId": op.FromNodeID, "toNodeId": op.ToNodeID, "toHandleId": op.ToHandleID}
	if op.FromHandleID != "" {
		edge["fromHandleId"] = op.FromHandleID
	}
	if err := p.canvasValidateEdgeHandles(maps(doc["nodes"]), edge); err != nil {
		return nil, err
	}
	if old == nil {
		edges = append(edges, edge)
	} else {
		edges[p.canvasNodeIndex(edges, op.ID)] = edge
	}
	doc["connections"] = p.mapsAsAny(edges)
	return []OperationSummary{{Operation: op.Type, NodeID: op.FromNodeID, TargetNodeID: op.ToNodeID, Summary: "更新批量表文字输入连线"}}, nil
}

// 批量文字列的行输入是生成真源；连线命令必须同步写入 rows[].textNodeIds，
// 否则字段文档保存后只剩一条不再持久化的显示边。
func (p *planner) attachBatchTextColumn(doc Document, tableID, handle, sourceID string) {
	table, _, index, ok := p.batchTextColumn(doc, tableID, handle)
	if !ok {
		return
	}
	columns := maps(table["textColumns"])
	rows := maps(table["rows"])
	for _, row := range rows {
		ids := p.canvasStrings(row["textNodeIds"])
		for len(ids) < len(columns) {
			ids = append(ids, "")
		}
		if ids[index] == sourceID {
			continue
		}
		ids[index] = sourceID
		row["textNodeIds"] = stringListAsAny(ids)
	}
	table["textColumns"], table["rows"] = p.mapsAsAny(columns), p.mapsAsAny(rows)
}

func (p *planner) detachBatchTextColumn(doc Document, edge map[string]any) {
	if text(edge["relation"]) != "batch-input" && !strings.HasPrefix(text(edge["toHandleId"]), "batch-text:") {
		return
	}
	table, _, index, ok := p.batchTextColumn(doc, text(edge["toNodeId"]), text(edge["toHandleId"]))
	if !ok {
		return
	}
	sourceID := text(edge["fromNodeId"])
	rows := maps(table["rows"])
	for _, row := range rows {
		ids := p.canvasStrings(row["textNodeIds"])
		if index >= len(ids) || ids[index] != sourceID {
			continue
		}
		ids[index] = ""
		row["textNodeIds"] = stringListAsAny(ids)
	}
	table["rows"] = p.mapsAsAny(rows)
}

func (p *planner) batchTextColumn(doc Document, tableID, handle string) (map[string]any, string, int, bool) {
	columnID, ok := strings.CutPrefix(handle, "batch-text:")
	if !ok {
		return nil, "", -1, false
	}
	node := entity(doc, "node:"+tableID)
	if node == nil || text(node["type"]) != "batch-table" {
		return nil, "", -1, false
	}
	table, _ := p.canvasNodeMetadata(node)["batchTable"].(map[string]any)
	if table == nil {
		return nil, "", -1, false
	}
	columns := maps(table["textColumns"])
	for index, column := range columns {
		if text(column["id"]) == columnID {
			return table, columnID, index, true
		}
	}
	return nil, "", -1, false
}

// 分镜素材引用同样以行字段为真源：Agent 连线后必须写入 assetBindings，
// 否则字段文档保存时会丢弃显示边，重开就看不到这条素材。
func (p *planner) attachStoryboardAsset(doc Document, op Operation) {
	if op.Relation != "storyboard-asset-reference" {
		return
	}
	row, ok := p.storyboardAssetRow(doc, op.StoryboardRowID, op.ToNodeID)
	if !ok {
		return
	}
	source := entity(doc, "node:"+op.FromNodeID)
	role := storyboardAssetRole(source)
	if role == "" {
		return
	}
	bindings := maps(row["assetBindings"])
	for _, binding := range bindings {
		if text(binding["nodeId"]) == op.FromNodeID {
			return
		}
	}
	bindings = append(bindings, map[string]any{"nodeId": op.FromNodeID, "role": role, "priority": storyboardAssetPriority(role)})
	row["assetBindings"] = p.mapsAsAny(bindings)
}

func (p *planner) detachStoryboardAsset(doc Document, edge map[string]any) {
	if text(edge["relation"]) != "storyboard-asset-reference" {
		return
	}
	row, ok := p.storyboardAssetRow(doc, text(edge["storyboardRowId"]), text(edge["toNodeId"]))
	if !ok {
		return
	}
	sourceID := text(edge["fromNodeId"])
	bindings := maps(row["assetBindings"])
	kept := make([]map[string]any, 0, len(bindings))
	for _, binding := range bindings {
		if text(binding["nodeId"]) != sourceID {
			kept = append(kept, binding)
		}
	}
	row["assetBindings"] = p.mapsAsAny(kept)
}

func (p *planner) storyboardAssetRow(doc Document, rowID, outputID string) (map[string]any, bool) {
	if rowID == "" || outputID == "" {
		return nil, false
	}
	for _, node := range maps(doc["nodes"]) {
		if text(node["type"]) != "script" {
			continue
		}
		row := p.canvasConnectionRow(node, rowID)
		if row != nil && (text(row["imageNodeId"]) == outputID || text(row["videoNodeId"]) == outputID) {
			return row, true
		}
	}
	return nil, false
}

func storyboardAssetRole(node map[string]any) string {
	metadata, _ := node["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
	}
	switch {
	case metadata["workflowKind"] == "character" || metadata["assetCategory"] == "character":
		return "character"
	case node["type"] == "audio":
		return "audio"
	case node["type"] == "video":
		return "motion"
	case metadata["assetCategory"] == "environment" || metadata["assetCategory"] == "prop" || metadata["assetCategory"] == "wardrobe" || metadata["assetCategory"] == "weapon":
		return text(metadata["assetCategory"])
	case node["type"] == "image" || node["type"] == "drawing":
		return "style"
	default:
		return ""
	}
}

func storyboardAssetPriority(role string) float64 {
	switch role {
	case "character":
		return 100
	case "environment":
		return 90
	case "prop", "weapon", "wardrobe":
		return 80
	case "motion", "audio":
		return 70
	default:
		return 60
	}
}

func stringListAsAny(items []string) []any {
	out := make([]any, len(items))
	for index, item := range items {
		out[index] = item
	}
	return out
}
func (p *planner) detachNativeOutput(doc Document, old map[string]any) {
	relation := text(old["relation"])
	if relation != "batch-output" && relation != "storyboard-output" {
		return
	}
	from, to, rowID := text(old["fromNodeId"]), text(old["toNodeId"]), text(old["storyboardRowId"])
	for _, edge := range maps(doc["connections"]) {
		if text(edge["id"]) != text(old["id"]) && text(edge["relation"]) == relation && text(edge["fromNodeId"]) == from && text(edge["toNodeId"]) == to && text(edge["storyboardRowId"]) == rowID {
			return
		}
	}
	source := entity(doc, "node:"+from)
	if source == nil {
		return
	}
	if relation == "batch-output" {
		table, _ := p.canvasNodeMetadata(source)["batchTable"].(map[string]any)
		for _, row := range maps(table["rows"]) {
			if text(row["id"]) == rowID && text(row["outputNodeId"]) == to {
				delete(row, "outputNodeId")
			}
		}
	} else {
		row := p.canvasConnectionRow(source, rowID)
		for _, field := range []string{"imageNodeId", "videoNodeId"} {
			if row != nil && text(row[field]) == to {
				delete(row, field)
			}
		}
	}
}
