// Adapted from Canary f1d8ee00 pure canvas declarations; AGPL-3.0.
package commands

import (
	sha256 "crypto/sha256"
	base64 "encoding/base64"
	binary "encoding/binary"
	hex "encoding/hex"
	json "encoding/json"
	fmt "fmt"
	assets "infinite-canvas/backend/internal/assets"
	capability "infinite-canvas/backend/internal/canvas/capability"
	connection "infinite-canvas/backend/internal/canvas/connection"
	layout "infinite-canvas/backend/internal/canvas/layout"
	references "infinite-canvas/backend/internal/canvas/references"
	kernel "infinite-canvas/backend/internal/kernel"
	math "math"
	url "net/url"
	regexp "regexp"
	sort "sort"
	strconv "strconv"
	strings "strings"
	time "time"
	unicode "unicode"
	utf8 "unicode/utf8"
)

type creationNodeSpec struct {
	Type         string         `json:"type"`
	ID           string         `json:"id,omitempty"`
	NodeType     string         `json:"nodeType,omitempty"`
	Title        string         `json:"title,omitempty"`
	Position     map[string]any `json:"position,omitempty"`
	X            *float64       `json:"x,omitempty"`
	Y            *float64       `json:"y,omitempty"`
	Width        *float64       `json:"width,omitempty"`
	Height       *float64       `json:"height,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Patch        map[string]any `json:"patch,omitempty"`
	FromNodeID   string         `json:"fromNodeId,omitempty"`
	ToNodeID     string         `json:"toNodeId,omitempty"`
	FromHandleID string         `json:"fromHandleId,omitempty"`
	ToHandleID   string         `json:"toHandleId,omitempty"`
	IDs          []string       `json:"ids,omitempty"`
}

func (p *planner) applyCanvasCanvasPlanInPlace(doc map[string]any, ops []Operation) ([]OperationSummary, error) {
	nodes := p.creationMaps(doc["nodes"])
	edges := p.creationMaps(doc["connections"])
	items := make([]OperationSummary, 0, len(ops))
	for opIndex, op := range ops {
		title, content := "", ""
		if op.Title != nil {
			title = *op.Title
		}
		if op.Content != nil {
			content = *op.Content
		}
		if err := validateCanvasID(op.ID, "节点或连线 ID", 80); err != nil {
			return nil, err
		}
		if op.Type == "add_node" && (utf8.RuneCountInString(content) > 1<<20 || utf8.RuneCountInString(title) > 240) {
			return nil, BadAuthRequest("节点标题或正文超出限制")
		}
		index := p.canvasNodeIndex(nodes, op.ID)
		switch op.Type {
		case "add_node":
			if index >= 0 {
				return nil, BadAuthRequest("新增节点ID重复")
			}
			capability, ok := p.canvasNodeCapabilityForType(op.NodeType)
			if strings.TrimSpace(op.NodeType) == "" {
				return nil, BadAuthRequest("新增节点缺少 nodeType")
			}
			if !ok {
				return nil, BadAuthRequest("不支持的节点类型")
			}
			if field, exists := capability.PatchFields["content"]; exists && field.MaxRunes > 0 && utf8.RuneCountInString(content) > field.MaxRunes {
				return nil, BadAuthRequest("该类型正文超过字段限制")
			}
			x, y := op.X, op.Y
			if x == nil || y == nil {

				pending := p.canvasLayoutNodes(map[string]any{"nodes": nodes})
				var hint *layout.Position
				if x != nil || y != nil {
					hint = &layout.Position{}
					if x != nil {
						hint.X = *x
					}
					if y != nil {
						hint.Y = *y
					}
				}
				if slot, ok := p.canvasArrangeAddNodePosition(doc, pending, op, ops, hint); ok {
					if x == nil {
						value := slot.X
						x = &value
					}
					if y == nil {
						value := slot.Y
						y = &value
					}
				}
			}
			metadata := capability.Metadata(content)
			if op.CopiedFromNodeID != "" {
				if err := validateCanvasID(op.CopiedFromNodeID, "复制来源节点 ID", 80); err != nil {
					return nil, err
				}
				source := p.copiedNodeSources[op.CopiedFromNodeID]
				if source == nil {
					sourceIndex := p.canvasNodeIndex(nodes, op.CopiedFromNodeID)
					if sourceIndex >= 0 {
						source = nodes[sourceIndex]
					}
				}
				if source == nil || p.stringValue(source["type"]) != op.NodeType {
					return nil, BadAuthRequest("复制来源必须是当前画布中同类型的节点")
				}
				sourceMetadata, _ := source["metadata"].(map[string]any)
				if sourceMetadata == nil {
					sourceMetadata = map[string]any{}
				}
				if op.NodeType == "drawing" && p.stringValue(sourceMetadata["drawingId"]) != "" && sourceMetadata["drawingDocument"] == nil {
					return nil, BadAuthRequest("绘图文档尚未同步，不能只复制预览节点")
				}
				cloned, err := cloneDocument(Document(sourceMetadata))
				if err != nil {
					return nil, err
				}
				metadata = map[string]any(cloned)
				copiedNodeIDs := make(map[string]string, len(p.copiedNodeIDs)+1)
				for sourceID, copiedID := range p.copiedNodeIDs {
					copiedNodeIDs[sourceID] = copiedID
				}
				for _, candidate := range ops {
					if candidate.Type == "add_node" && candidate.CopiedFromNodeID != "" {
						copiedNodeIDs[candidate.CopiedFromNodeID] = candidate.ID
					}
				}
				copiedNodeIDs[op.CopiedFromNodeID] = op.ID
				p.canvasResetCopiedMetadata(metadata, op.ID, copiedNodeIDs)
				p.canvasRemapCopiedAddReferences(metadata, copiedNodeIDs)
				if op.NodeType == "image" || op.NodeType == "video" || op.NodeType == "audio" {
					metadata["generationResultPlacement"] = "replace-node"
				}
				if op.NodeType == "drawing" {
					for _, key := range []string{"drawingId", "drawingRevision", "drawingUpdatedAt", "drawingShapeCount", "drawingPageCount"} {
						delete(metadata, key)
					}
					if document, ok := metadata["drawingDocument"].(map[string]any); ok {
						document["revision"], document["updatedAt"] = float64(1), p.now().UTC().Format(time.RFC3339Nano)
						metadata["drawingId"], metadata["drawingEngine"] = op.ID+"-document", document["engine"]
						metadata["drawingRevision"], metadata["drawingUpdatedAt"] = document["revision"], document["updatedAt"]
						metadata["drawingShapeCount"], metadata["drawingPageCount"] = document["shapeCount"], document["pageCount"]
					}
				}
				if op.Content != nil && content != p.stringValue(metadata["content"]) {
					return nil, BadAuthRequest("副本内容与复制来源不一致")
				}
				metadata["copiedFromNodeId"] = op.CopiedFromNodeID
			}
			node := p.creationAddedNode(creationNodeSpec{Type: op.Type, ID: op.ID, NodeType: op.NodeType, Title: title, X: x, Y: y, Metadata: metadata})
			nodes = append(nodes, node)
			nodeTitle := p.canvasApprovalNodeTitle(node, capability.Label)
			items = append(items, OperationSummary{
				Operation: "add_node", NodeID: op.ID, NodeTitle: nodeTitle,
				NodeType: capability.Type, NodeTypeLabel: capability.Label,
				Summary: fmt.Sprintf("新增%s《%s》", capability.Label, nodeTitle),
			})
		case "connect_nodes":
			if err := validateCanvasID(op.FromNodeID, "来源节点 ID", 80); err != nil {
				return nil, err
			}
			if err := validateCanvasID(op.ToNodeID, "目标节点 ID", 80); err != nil {
				return nil, err
			}
			fromIndex, toIndex := p.canvasNodeIndex(nodes, op.FromNodeID), p.canvasNodeIndex(nodes, op.ToNodeID)
			if fromIndex < 0 || toIndex < 0 || op.FromNodeID == op.ToNodeID {
				return nil, BadAuthRequest("连线端点不存在或指向自身")
			}
			if err := p.validateCanvasConnection(nodes, op.FromNodeID, op.ToNodeID, edges); err != nil {
				return nil, canvasFieldError(fmt.Sprintf("ops[%d]", opIndex), "invalid_connection", canvasSafeToolError(err))
			}
			for _, edge := range edges {
				if p.stringValue(edge["id"]) == op.ID || (p.stringValue(edge["fromNodeId"]) == op.FromNodeID && p.stringValue(edge["toNodeId"]) == op.ToNodeID && p.stringValue(edge["fromHandleId"]) == op.FromHandleID && p.stringValue(edge["toHandleId"]) == op.ToHandleID) {
					return nil, BadAuthRequest("连线重复")
				}
			}
			fromCapability, _ := p.canvasNodeCapabilityForNode(nodes[fromIndex])
			toCapability, _ := p.canvasNodeCapabilityForNode(nodes[toIndex])
			fromTitle := p.canvasApprovalNodeTitle(nodes[fromIndex], fromCapability.Label)
			toTitle := p.canvasApprovalNodeTitle(nodes[toIndex], toCapability.Label)
			edge := map[string]any{"id": op.ID, "fromNodeId": op.FromNodeID, "toNodeId": op.ToNodeID}
			if op.FromHandleID != "" {
				edge["fromHandleId"] = op.FromHandleID
			}
			if op.ToHandleID != "" {
				edge["toHandleId"] = op.ToHandleID
			}
			if err := p.canvasValidateEdgeHandles(nodes, edge); err != nil {
				return nil, err
			}
			edges = append(edges, edge)
			if err := p.canvasAttachStoryboardEdge(nodes, edge); err != nil {
				return nil, err
			}
			items = append(items, OperationSummary{
				Operation: "connect_nodes", NodeID: op.FromNodeID, NodeTitle: fromTitle,
				NodeType: fromCapability.Type, NodeTypeLabel: fromCapability.Label,
				TargetNodeID: op.ToNodeID, TargetNodeTitle: toTitle, TargetNodeType: toCapability.Type,
				Summary: fmt.Sprintf("建立《%s》→《%s》的引用连线", fromTitle, toTitle),
			})
		case "update_node":
			if len(op.Patch) == 0 {

				return nil, &ArgumentError{BadAuthRequest("更新节点必须提供 patch")}
			}
			if index < 0 {
				return nil, BadAuthRequest("只能更新现有且受 画布命令 支持的节点")
			}
			capability, ok := p.canvasNodeCapabilityForNode(nodes[index])
			if !ok || !capability.CanUpdate {
				return nil, BadAuthRequest("该节点类型不支持 画布命令 更新")
			}
			metadata, _ := nodes[index]["metadata"].(map[string]any)
			if metadata["locked"] == true && p.canvasPatchMovesNode(op.Patch) {
				return nil, BadAuthRequest("不能修改锁定节点的位置或尺寸，请先解锁")
			}
			beforeTitle := p.canvasApprovalNodeTitle(nodes[index], capability.Label)
			fields := p.canvasApprovalPatchLabels(capability.PatchFields, op.Patch)
			if err := capability.ApplyPatch(nodes[index], op.Patch); err != nil {
				return nil, BadAuthRequest(err.Error())
			}
			if _, changed := op.Patch["content"]; changed && capability.Type == "text" && op.Patch["richText"] == nil {
				delete(p.canvasNodeMetadata(nodes[index]), "richText")
			}
			afterTitle := p.canvasApprovalNodeTitle(nodes[index], capability.Label)
			resultTitle := ""
			if afterTitle != beforeTitle {
				resultTitle = afterTitle
			}
			items = append(items, OperationSummary{
				Operation: "update_node", NodeID: op.ID, NodeTitle: beforeTitle, ResultTitle: resultTitle,
				NodeType: capability.Type, NodeTypeLabel: capability.Label, Fields: fields,
				Summary: fmt.Sprintf("修改%s《%s》的%s", capability.Label, beforeTitle, strings.Join(fields, "、")),
			})
		case "delete_node", "delete_connection", "update_connection", "duplicate_node", "set_parent", "replace_text", "reorder_nodes", "reorder_rows":
			var item OperationSummary
			var err error
			nodes, edges, item, err = p.applyCanvasExtendedOp(nodes, edges, op)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		default:
			return nil, BadAuthRequest("不支持的画布写操作")
		}
	}
	doc["nodes"] = nodes
	doc["connections"] = edges
	return items, nil
}

func (p *planner) applyCanvasExtendedOp(nodes, edges []map[string]any, op Operation) ([]map[string]any, []map[string]any, OperationSummary, error) {
	item := OperationSummary{Operation: op.Type, NodeID: op.ID, NodeTitle: op.ID}
	fail := func(message string) ([]map[string]any, []map[string]any, OperationSummary, error) {
		return nodes, edges, item, BadAuthRequest(message)
	}
	index := p.canvasNodeIndex(nodes, op.ID)
	if index >= 0 {
		item.NodeTitle = p.stringValue(nodes[index]["title"])
		item.NodeType = p.stringValue(nodes[index]["type"])
	}
	switch op.Type {
	case "delete_node":
		if index < 0 {
			return fail("删除节点不存在")
		}
		removed := map[string]bool{op.ID: true}
		for _, id := range p.canvasStrings(p.canvasNodeMetadata(nodes[index])["batchChildIds"]) {
			removed[id] = true
		}
		next := make([]map[string]any, 0, len(nodes))
		for _, node := range nodes {
			if !removed[p.stringValue(node["id"])] {
				p.canvasCleanNodeReferences(node, removed)
				next = append(next, node)
			}
		}
		kept := make([]map[string]any, 0, len(edges))
		for _, edge := range edges {
			if !removed[p.stringValue(edge["fromNodeId"])] && !removed[p.stringValue(edge["toNodeId"])] {
				kept = append(kept, edge)
			}
		}
		item.Summary = fmt.Sprintf("删除《%s》及其关联引用，保留素材库原文件", item.NodeTitle)
		return next, kept, item, nil
	case "delete_connection":
		edgeIndex := p.canvasNodeIndex(edges, op.ID)
		if edgeIndex < 0 {
			return fail("删除连线不存在")
		}
		item.NodeID = p.stringValue(edges[edgeIndex]["toNodeId"])
		item.TargetNodeID = p.stringValue(edges[edgeIndex]["fromNodeId"])
		removed := edges[edgeIndex]
		next := append(edges[:edgeIndex:edgeIndex], edges[edgeIndex+1:]...)
		p.canvasRemoveEdgeReference(nodes, removed, next)
		item.Summary = "移除引用连线"
		return nodes, next, item, nil
	case "update_connection":
		edgeIndex := p.canvasNodeIndex(edges, op.ID)
		if edgeIndex < 0 {
			return fail("修改连线不存在")
		}
		other := append([]map[string]any{}, edges[:edgeIndex]...)
		other = append(other, edges[edgeIndex+1:]...)
		if err := p.validateCanvasConnection(nodes, op.FromNodeID, op.ToNodeID, other); err != nil {
			return nodes, edges, item, err
		}
		old := edges[edgeIndex]
		edge := map[string]any{"id": op.ID, "fromNodeId": op.FromNodeID, "toNodeId": op.ToNodeID}
		if op.FromHandleID != "" {
			edge["fromHandleId"] = op.FromHandleID
		}
		if op.ToHandleID != "" {
			edge["toHandleId"] = op.ToHandleID
		}
		if err := p.canvasValidateEdgeHandles(nodes, edge); err != nil {
			return nodes, edges, item, err
		}
		for _, existing := range other {
			if p.stringValue(existing["fromNodeId"]) == op.FromNodeID && p.stringValue(existing["toNodeId"]) == op.ToNodeID && p.stringValue(existing["fromHandleId"]) == op.FromHandleID && p.stringValue(existing["toHandleId"]) == op.ToHandleID {
				return fail("连线重复")
			}
		}
		edges[edgeIndex] = edge
		p.canvasRemoveEdgeReference(nodes, old, edges)
		if err := p.canvasAttachStoryboardEdge(nodes, edge); err != nil {
			return nodes, edges, item, err
		}
		item.NodeID = op.FromNodeID
		item.TargetNodeID = op.ToNodeID
		item.Summary = "修改引用连线端点"
		return nodes, edges, item, nil
	case "duplicate_node":
		return p.canvasCopyGraph(nodes, edges, op)
	case "set_parent":
		if index < 0 {
			return fail("分组节点不存在")
		}
		if op.ParentID == "" {
			delete(nodes[index], "parentId")
		} else {
			parent := p.canvasNodeIndex(nodes, op.ParentID)
			if parent < 0 || p.stringValue(nodes[parent]["type"]) != "frame" {
				return fail("父节点必须是当前画布的背板或文件夹")
			}
			if p.stringValue(nodes[index]["type"]) == "frame" {
				return fail("容器不能嵌套到其他容器中")
			}
			if op.ParentID == op.ID {
				return fail("分组不能指向自身")
			}
			nodes[index]["parentId"] = op.ParentID
		}
		item.Summary = "调整节点的容器归属"
		return nodes, edges, item, nil
	case "replace_text":
		if index < 0 {
			return fail("编辑节点不存在")
		}
		descriptor, ok := p.canvasNodeCapabilityForNode(nodes[index])
		if !ok {
			return fail("节点类型不支持正文编辑")
		}
		field, ok := descriptor.PatchFields["content"]
		if !ok || field.Kind != "string" {
			return fail("该节点没有可编辑文本正文")
		}
		parts := strings.Split(field.Path, ".")
		if len(parts) != 2 || parts[0] != "metadata" {
			return fail("节点正文编辑路径无效")
		}
		metadata := p.canvasNodeMetadata(nodes[index])
		text := p.stringValue(metadata[parts[1]])
		if op.Match == "" || strings.Count(text, op.Match) != 1 {
			return fail("替换片段必须在完整正文中恰好匹配一次；本次未修改")
		}
		next := strings.Replace(text, op.Match, op.Replacement, 1)
		if len([]rune(next)) > 1<<20 {
			return fail("编辑后的正文超出画布文本限制")
		}
		if err := descriptor.ApplyPatch(nodes[index], map[string]any{"content": next}); err != nil {
			return nodes, edges, item, BadAuthRequest(err.Error())
		}
		if descriptor.Type == "text" {
			delete(metadata, "richText")
		}
		item.Fields = []string{field.Label}
		item.Summary = "精确修改正文片段，保留其余内容"
		return nodes, edges, item, nil
	case "reorder_nodes":
		next, err := p.canvasReorderItems(nodes, op.NodeIDs)
		if err != nil {
			return nodes, edges, item, err
		}
		item.Summary = "调整节点堆叠顺序"
		return next, edges, item, nil
	case "reorder_rows":
		if index < 0 {
			return fail("排序表格不存在")
		}
		meta := p.canvasNodeMetadata(nodes[index])
		var table map[string]any
		switch p.stringValue(nodes[index]["type"]) {
		case "script":
			table, _ = meta["storyboard"].(map[string]any)
		case "batch-table":
			table, _ = meta["batchTable"].(map[string]any)
		}
		if table == nil {
			return fail("该节点没有可排序的结构化行")
		}
		rows := p.creationMaps(table["rows"])
		next, err := p.canvasReorderItems(rows, op.RowIDs)
		if err != nil {
			return nodes, edges, item, err
		}
		table["rows"] = next
		item.Summary = "调整结构化行顺序，保留行 ID 和内容"
		return nodes, edges, item, nil
	}
	return fail("不支持的画布操作")
}

func (p *planner) batchTableNodeFromDocument(doc map[string]any, nodeID string, pendingRows ...bool) (map[string]any, map[string]any, []map[string]any, []map[string]any, error) {
	for _, node := range p.creationMaps(doc["nodes"]) {
		if p.stringValue(node["id"]) != nodeID {
			continue
		}
		if p.stringValue(node["type"]) != "batch-table" {
			return nil, nil, nil, nil, BadAuthRequest("目标节点不是批量创作表节点")
		}
		metadata, ok := node["metadata"].(map[string]any)
		if !ok {
			return nil, nil, nil, nil, BadAuthRequest("批量创作表节点数据格式无效")
		}
		table, ok := metadata["batchTable"].(map[string]any)
		if !ok {
			return nil, nil, nil, nil, BadAuthRequest("批量创作表节点缺少结构化数据")
		}
		operation := p.stringValue(table["operation"])
		if operation != "try_on" && operation != "creative" {
			return nil, nil, nil, nil, BadAuthRequest("批量创作表包含无效的任务类型")
		}
		concurrency, ok := p.canvasInteger(table["concurrency"])
		if !ok || !p.canvasBatchConcurrencyAllowed(concurrency) {
			return nil, nil, nil, nil, BadAuthRequest("批量创作表包含无效的并发数")
		}
		if rawPrompt, exists := table["globalPrompt"]; exists {
			globalPrompt, ok := rawPrompt.(string)
			if !ok || utf8.RuneCountInString(globalPrompt) > maxCanvasBatchPromptRunes {
				return nil, nil, nil, nil, BadAuthRequest("批量创作表包含无效或过长的全局提示词")
			}
		}
		columns := p.creationMaps(table["referenceColumns"])
		if len(columns) < 1 || len(columns) > maxCanvasBatchReferences {
			return nil, nil, nil, nil, BadAuthRequest("批量创作表必须包含 1 到 10 个参考图列")
		}
		columnIDs := map[string]bool{}
		for _, column := range columns {
			id, label := p.stringValue(column["id"]), p.stringValue(column["label"])
			if validateCanvasID(id, "参考图列ID", 120) != nil || strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > 120 || columnIDs[id] {
				return nil, nil, nil, nil, BadAuthRequest("批量创作表包含无效或重复的参考图列")
			}
			columnIDs[id] = true
		}
		rows := p.creationMaps(table["rows"])
		if len(rows) > maxCanvasBatchRows {
			return nil, nil, nil, nil, BadAuthRequest("单个批量创作表最多500行")
		}
		if len(pendingRows) > 0 && pendingRows[0] {
			return node, table, rows, columns, nil
		}
		rowIDs := map[string]bool{}
		for _, row := range rows {
			rowID := p.stringValue(row["id"])
			if validateCanvasID(rowID, "批量创作行ID", 120) != nil || rowIDs[rowID] {
				return nil, nil, nil, nil, BadAuthRequest("批量创作表包含无效或重复的行ID")
			}
			rowIDs[rowID] = true
			if _, ok := row["enabled"].(bool); !ok {
				return nil, nil, nil, nil, BadAuthRequest("批量创作表包含无效的启用状态")
			}
			prompt, ok := row["prompt"].(string)
			if !ok || utf8.RuneCountInString(prompt) > maxCanvasBatchPromptRunes {
				return nil, nil, nil, nil, BadAuthRequest("批量创作表包含无效或过长的提示词")
			}
			if _, err := p.canvasBatchInputNodeIDs(row["inputNodeIds"], len(columns)); err != nil {
				return nil, nil, nil, nil, err
			}
			if outputNodeID := p.stringValue(row["outputNodeId"]); outputNodeID != "" && validateCanvasID(outputNodeID, "输出节点ID", 120) != nil {
				return nil, nil, nil, nil, BadAuthRequest("批量创作表包含无效的输出节点ID")
			}
		}
		return node, table, rows, columns, nil
	}
	return nil, nil, nil, nil, BadAuthRequest("未找到当前画布的批量创作表节点")
}

func (p *planner) boolValue(value any, fallback bool) bool {
	if result, ok := value.(bool); ok {
		return result
	}
	return fallback
}

func (p *planner) canvasAddedNodeSize(nodeType string) (float64, float64) {
	if capability, ok := p.canvasNodeCapabilityForType(nodeType); ok {
		return capability.DefaultWidth, capability.DefaultHeight
	}
	return 340, 240
}

func (p *planner) canvasApprovalNodeTitle(node map[string]any, typeLabel string) string {
	title := p.truncateRunes(strings.TrimSpace(p.stringValue(node["title"])), 120)
	if title != "" {
		return title
	}
	return "未命名" + typeLabel
}

func (p *planner) canvasApprovalPatchLabels(fields map[string]capability.PatchField, patch map[string]any) []string {
	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := fields[keys[i]], fields[keys[j]]
		if left.Order != right.Order {
			return left.Order < right.Order
		}
		return keys[i] < keys[j]
	})
	labels := make([]string, 0, len(keys))
	for _, key := range keys {
		if field, ok := fields[key]; ok {
			labels = append(labels, field.Label)
		}
	}
	return labels
}

type OperationSummary struct {
	Operation       string   `json:"operation"`
	NodeID          string   `json:"nodeId,omitempty"`
	NodeTitle       string   `json:"nodeTitle,omitempty"`
	ResultTitle     string   `json:"resultTitle,omitempty"`
	NodeType        string   `json:"nodeType,omitempty"`
	NodeTypeLabel   string   `json:"nodeTypeLabel,omitempty"`
	TargetNodeID    string   `json:"targetNodeId,omitempty"`
	TargetNodeTitle string   `json:"targetNodeTitle,omitempty"`
	TargetNodeType  string   `json:"targetNodeType,omitempty"`
	Fields          []string `json:"fields,omitempty"`
	Details         []string `json:"details,omitempty"`
	Summary         string   `json:"summary"`
}

func (p *planner) canvasArrangeAddNodePosition(doc map[string]any, pending []layout.Node, op Operation, ops []Operation, hint *layout.Position) (layout.Position, bool) {
	lane, supported := p.canvasLayoutLane(op.NodeType)
	if !supported {
		return layout.Position{}, false
	}
	width, height := p.canvasAddedNodeSize(op.NodeType)
	occupied := append([]layout.Node{}, p.canvasLayoutNodes(doc)...)
	occupied = append(occupied, pending...)

	preferred := hint
	for _, candidate := range ops {
		if candidate.Type != "connect_nodes" {
			continue
		}
		if preferred != nil {
			break
		}
		switch {
		case candidate.ToNodeID == op.ID:
			if source, ok := p.findLayoutNode(occupied, candidate.FromNodeID); ok {
				preferred = &layout.Position{X: source.X + source.Width + layout.PlacementGap, Y: source.Y}
			}
		case candidate.FromNodeID == op.ID:
			if target, ok := p.findLayoutNode(occupied, candidate.ToNodeID); ok {
				preferred = &layout.Position{X: target.X - width - layout.PlacementGap, Y: target.Y}
			}
		}
	}
	return layout.FreeSlot(width, height, lane, occupied, preferred, layout.PlacementGap), true
}

func (p *planner) canvasAttachStoryboardEdge(nodes []map[string]any, edge map[string]any) error {
	for _, side := range []string{"from", "to"} {
		handle := p.stringValue(edge[side+"HandleId"])
		if !strings.HasPrefix(handle, "row:") && handle != "storyboard:context" {
			continue
		}
		otherSide := "from"
		if side == "from" {
			otherSide = "to"
		}
		index := p.canvasNodeIndex(nodes, p.stringValue(edge[side+"NodeId"]))
		other := p.canvasNodeIndex(nodes, p.stringValue(edge[otherSide+"NodeId"]))
		if index < 0 || other < 0 || p.stringValue(nodes[index]["type"]) != "script" {
			continue
		}
		board, _ := p.canvasNodeMetadata(nodes[index])["storyboard"].(map[string]any)
		if board == nil {
			continue
		}
		linked := nodes[other]
		linkedID := p.stringValue(linked["id"])
		if handle == "storyboard:context" {
			refs := p.canvasStrings(board["referenceNodeIds"])
			found := false
			for _, id := range refs {
				if id == linkedID {
					found = true
				}
			}
			if !found {
				refs = append(refs, linkedID)
			}
			board["referenceNodeIds"] = refs
			continue
		}
		rows := p.creationMaps(board["rows"])
		rowIndex := p.canvasNodeIndex(rows, strings.TrimPrefix(handle, "row:"))
		if rowIndex < 0 {
			continue
		}
		row := rows[rowIndex]
		if side == "from" {
			switch p.stringValue(linked["type"]) {
			case "image":
				row["imageNodeId"] = linkedID
			case "video":
				row["videoNodeId"] = linkedID
				prompt := p.stringValue(row["videoMotionPrompt"])
				if strings.TrimSpace(prompt) == "" {
					prompt = p.stringValue(row["plotDescription"])
				}
				meta := p.canvasNodeMetadata(linked)
				meta["workflowKind"], meta["shotIndex"], meta["generationMode"] = "shot", row["shotNumber"], "video"
				patch := map[string]any{"content": prompt}
				if seconds := row["durationSeconds"]; seconds != nil {
					patch["seconds"] = fmt.Sprint(seconds)
				}
				descriptor, _ := p.canvasNodeCapabilityForNode(linked)
				if err := descriptor.ApplyPatch(linked, patch); err != nil {
					return BadAuthRequest("分镜提示词或时长与目标视频配置不一致：" + err.Error())
				}
			}
		} else {
			meta := p.canvasNodeMetadata(linked)
			role := ""
			switch {
			case meta["workflowKind"] == "character" || meta["assetCategory"] == "character":
				role = "character"
			case linked["type"] == "audio":
				role = "audio"
			case linked["type"] == "video":
				role = "motion"
			case meta["assetCategory"] == "environment" || meta["assetCategory"] == "prop":
				role = p.stringValue(meta["assetCategory"])
			case linked["type"] == "image" || linked["type"] == "drawing":
				role = "style"
			}
			if role != "" {
				bindings := p.creationMaps(row["assetBindings"])
				exists := false
				for _, binding := range bindings {
					if binding["nodeId"] == linkedID {
						exists = true
					}
				}
				if !exists {
					priority := 60
					switch role {
					case "character":
						priority = 100
					case "environment":
						priority = 90
					case "prop":
						priority = 80
					case "motion", "audio":
						priority = 70
					}
					bindings = append(bindings, map[string]any{"nodeId": linkedID, "role": role, "priority": priority})
					row["assetBindings"] = p.mapsAsAny(bindings)
				}
			}
		}
	}
	return nil
}

func (p *planner) canvasBatchConcurrencyAllowed(value int) bool {
	return value == 1 || value == 5 || value == 10
}

func (p *planner) canvasBatchInputIDs(value any, limit int) []any {
	if limit <= 0 || limit > maxCanvasBatchReferences {
		limit = maxCanvasBatchReferences
	}
	items, _ := value.([]any)
	out := make([]any, 0, min(len(items), limit))
	seen := map[string]bool{}
	for _, item := range items {
		id := p.truncateRunes(p.stringValue(item), 120)
		if id == "" || seen[id] || len(out) == limit {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func (p *planner) canvasBatchInputNodeIDs(value any, maxItems int) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]string); typedOK {
			items = make([]any, len(typed))
			for index, item := range typed {
				items[index] = item
			}
		} else {
			return nil, BadAuthRequest("批量创作行的 inputNodeIds 必须是图片节点ID数组")
		}
	}
	if len(items) > maxItems || len(items) > maxCanvasBatchReferences {
		return nil, BadAuthRequest("每行参考图数量不能超过当前参考图列数")
	}
	result := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		id, ok := item.(string)
		if !ok || (id != "" && (validateCanvasID(id, "参考图片节点ID", 80) != nil || seen[id])) {
			return nil, BadAuthRequest("批量创作行包含无效或重复的图片节点ID")
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

func (p *planner) canvasBatchTextNodeIDs(doc map[string]any, value any) ([]any, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, BadAuthRequest("textNodeIds 必须是文本节点ID数组")
	}
	nodes := map[string]map[string]any{}
	for _, node := range p.creationMaps(doc["nodes"]) {
		nodes[p.stringValue(node["id"])] = node
	}
	seen := map[string]bool{}
	ids := make([]any, 0, len(items))
	for _, item := range items {
		id, ok := item.(string)
		if ok && id == "" {
			ids = append(ids, id)
			continue
		}
		node := nodes[id]
		if !ok || validateCanvasID(id, "文本节点ID", 80) != nil || seen[id] || node == nil || p.stringValue(node["type"]) != "text" || p.canvasCharacterNode(node) {
			return nil, BadAuthRequest("批量创作行只能引用当前画布内唯一的普通文本节点")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

func (p *planner) canvasCharacterNode(node map[string]any) bool {
	meta, _ := node["metadata"].(map[string]any)
	return p.stringValue(node["type"]) == "text" && p.stringValue(meta["workflowKind"]) == "character"
}

func (p *planner) canvasCleanNodeReferences(node map[string]any, removed map[string]bool) {
	if removed[p.stringValue(node["parentId"])] {
		delete(node, "parentId")
	}
	meta := p.canvasNodeMetadata(node)
	p.canvasCleanReferenceObject(meta, removed)
}

func (p *planner) canvasCleanReferenceObject(object map[string]any, removed map[string]bool) {
	for key, value := range object {
		switch typed := value.(type) {
		case map[string]any:
			p.canvasCleanReferenceObject(typed, removed)
		case []any:
			values := make([]any, 0, len(typed))
			for _, entry := range typed {
				if key == "inputNodeIds" || key == "textNodeIds" {
					if id, ok := entry.(string); ok && removed[id] {
						values = append(values, "")
						continue
					}
				}
				if s, ok := entry.(string); ok && (strings.HasSuffix(key, "NodeIds") || key == "batchChildIds") && removed[s] {
					continue
				}
				if entryMap, ok := entry.(map[string]any); ok {
					// Every declared binding array is dropped as a whole entry: leaving the
					// entry behind with a dangling nodeId keeps a deleted node referenced.
					if (key == "assetBindings" || key == "referenceBindings" || key == "inputBindings") && removed[p.stringValue(entryMap["nodeId"])] {
						continue
					}
					p.canvasCleanReferenceObject(entryMap, removed)
				}
				values = append(values, entry)
			}
			if len(values) != len(typed) && (key == "referenceBindings" || key == "inputBindings") {
				sort.SliceStable(values, func(i, j int) bool {
					left, _ := values[i].(map[string]any)
					right, _ := values[j].(map[string]any)
					return p.numberValue(left["order"], 0) < p.numberValue(right["order"], 0)
				})
				for index, entry := range values {
					if entryMap, ok := entry.(map[string]any); ok {
						entryMap["order"] = float64(index)
					}
				}
			}
			object[key] = values
		case string:
			if (strings.HasSuffix(key, "NodeId") || key == "primaryImageId" || key == "batchRootId") && removed[typed] {
				delete(object, key)
			}
		}
	}
}

func (p *planner) canvasConnectionEdge(edge map[string]any) connection.Edge {
	return connection.Edge{From: p.stringValue(edge["fromNodeId"]), To: p.stringValue(edge["toNodeId"]), FromHandle: p.stringValue(edge["fromHandleId"]), ToHandle: p.stringValue(edge["toHandleId"])}
}

func (p *planner) canvasConnectionNodes(nodes []map[string]any) []connection.Node {
	result := make([]connection.Node, 0, len(nodes))
	for _, n := range nodes {
		m, _ := n["metadata"].(map[string]any)
		result = append(result, connection.Node{ID: p.stringValue(n["id"]), Type: p.stringValue(n["type"]), WorkflowKind: p.stringValue(m["workflowKind"]), Mode: p.stringValue(m["generationMode"])})
	}
	return result
}

func (p *planner) canvasConnectionRow(node map[string]any, rowID string) map[string]any {
	metadata, _ := node["metadata"].(map[string]any)
	storyboard, _ := metadata["storyboard"].(map[string]any)
	for _, r := range p.creationMaps(storyboard["rows"]) {
		if p.stringValue(r["id"]) == rowID {
			return r
		}
	}
	return nil
}

func (p *planner) canvasCopyGraph(nodes, edges []map[string]any, op Operation) ([]map[string]any, []map[string]any, OperationSummary, error) {
	item := OperationSummary{Operation: "duplicate_node", NodeID: op.ID}
	sourceIndex := p.canvasNodeIndex(nodes, op.SourceNodeID)
	if sourceIndex < 0 || p.canvasNodeIndex(nodes, op.ID) >= 0 {
		return nodes, edges, item, BadAuthRequest("复制来源不存在或副本 ID 重复")
	}
	source := nodes[sourceIndex]
	sources := []map[string]any{source}
	frame := p.stringValue(source["type"]) == "frame"
	if frame {
		for _, node := range nodes {
			if p.stringValue(node["parentId"]) == op.SourceNodeID {
				sources = append(sources, node)
			}
		}
	}
	idMap := map[string]string{op.SourceNodeID: op.ID}
	for i, node := range sources[1:] {
		id := fmt.Sprintf("%s-child-%d", op.ID, i+1)
		if err := validateCanvasID(id, "副本节点 ID", 80); err != nil {
			return nodes, edges, item, err
		}
		if p.canvasNodeIndex(nodes, id) >= 0 {
			return nodes, edges, item, BadAuthRequest("副本成员 ID 已存在")
		}
		idMap[p.stringValue(node["id"])] = id
	}
	sourcePosition, _ := source["position"].(map[string]any)
	dx, dy := 36.0, 36.0
	if op.X != nil {
		value, _ := sourcePosition["x"].(float64)
		dx = *op.X - value
	}
	if op.Y != nil {
		value, _ := sourcePosition["y"].(float64)
		dy = *op.Y - value
	}
	copies := []map[string]any{}
	copiedRows := map[string]string{}
	for _, node := range sources {
		if _, ok := p.canvasNodeCapabilityForNode(node); !ok {
			return nodes, edges, item, BadAuthRequest("复制包含不支持的节点类型")
		}
		if p.stringValue(node["type"]) == "drawing" && p.stringValue(p.canvasNodeMetadata(node)["drawingId"]) != "" && p.canvasNodeMetadata(node)["drawingDocument"] == nil {
			return nodes, edges, item, BadAuthRequest("绘图文档尚未同步，不能只复制预览节点")
		}
		raw, _ := json.Marshal(node)
		var copy map[string]any
		if err := json.Unmarshal(raw, &copy); err != nil {
			return nodes, edges, item, err
		}
		oldID := p.stringValue(node["id"])
		copy["id"] = idMap[oldID]
		if parent := p.stringValue(copy["parentId"]); idMap[parent] != "" {
			copy["parentId"] = idMap[parent]
		}
		position, _ := copy["position"].(map[string]any)
		if position == nil {
			position = map[string]any{}
			copy["position"] = position
		}
		for axis, delta := range map[string]float64{"x": dx, "y": dy} {
			value, _ := position[axis].(float64)
			value += delta
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e6 {
				return nodes, edges, item, BadAuthRequest("副本位置无效")
			}
			position[axis] = value
		}
		metadata := p.canvasNodeMetadata(copy)
		for source, derived := range p.canvasResetCopiedMetadata(metadata, p.stringValue(copy["id"]), idMap) {
			copiedRows[source] = derived
		}
		p.canvasRemapCopiedReferences(metadata, idMap)
		metadata["copiedFromNodeId"] = oldID
		switch p.stringValue(node["type"]) {
		case "image", "video", "audio":
			metadata["generationResultPlacement"] = "replace-node"
		}
		if p.stringValue(node["type"]) == "drawing" {
			metadata["drawingId"] = idMap[oldID] + "-document"
			metadata["drawingRevision"] = float64(0)
			metadata["drawingShapeCount"] = float64(0)
			metadata["drawingPageCount"] = float64(1)
			delete(metadata, "drawingUpdatedAt")
			if document, ok := metadata["drawingDocument"].(map[string]any); ok {
				document["revision"] = float64(1)
				document["updatedAt"] = p.now().UTC().Format(time.RFC3339Nano)
				metadata["drawingEngine"] = document["engine"]
				metadata["drawingRevision"] = document["revision"]
				metadata["drawingUpdatedAt"] = document["updatedAt"]
				metadata["drawingShapeCount"] = document["shapeCount"]
				metadata["drawingPageCount"] = document["pageCount"]
			}
		}
		if oldID == op.SourceNodeID {
			copy["title"] = p.canvasCopyTitle(p.stringValue(node["title"]), nodes)
			if op.Title != nil {
				copy["title"] = *op.Title
			}
			if len([]rune(p.stringValue(copy["title"]))) > 240 {
				return nodes, edges, item, BadAuthRequest("副本标题超出限制")
			}
			item.NodeTitle = p.stringValue(node["title"])
			item.NodeType = p.stringValue(node["type"])
			item.ResultTitle = p.stringValue(copy["title"])
		}
		copies = append(copies, copy)
	}
	nextEdges := append([]map[string]any{}, edges...)
	edgeIndex := 0
	for _, edge := range edges {
		// Managed edges are derived from declared fields. Copying one would carry the
		// source owner's markers onto the copy, so the target scope re-coordinates them
		// from the copied fields instead.
		if p.stringValue(edge["managedBy"]) == references.ManagedBy {
			continue
		}
		from, to := p.stringValue(edge["fromNodeId"]), p.stringValue(edge["toNodeId"])
		if idMap[to] == "" || (idMap[from] == "" && frame) {
			continue
		}
		edgeIndex++
		edgeID := fmt.Sprintf("%s-edge-%d", op.ID, edgeIndex)
		if err := validateCanvasID(edgeID, "副本连线 ID", 80); err != nil {
			return nodes, edges, item, err
		}
		if p.canvasNodeIndex(nextEdges, edgeID) >= 0 {
			return nodes, edges, item, BadAuthRequest("副本连线 ID 已存在")
		}
		raw, _ := json.Marshal(edge)
		var copy map[string]any
		_ = json.Unmarshal(raw, &copy)
		copy["id"] = edgeID
		copy["toNodeId"] = idMap[to]
		if idMap[from] != "" {
			copy["fromNodeId"] = idMap[from]
		}
		if row := p.stringValue(copy["storyboardRowId"]); row != "" {
			if derived := copiedRows[row]; derived != "" {
				copy["storyboardRowId"] = derived
			} else {
				delete(copy, "storyboardRowId")
			}
		}
		nextEdges = append(nextEdges, copy)
	}
	item.Summary = fmt.Sprintf("复制 %d 个节点及关联连线，并隔离生成任务身份", len(copies))
	return append(nodes, copies...), nextEdges, item, nil
}

var canvasCopySuffix = regexp.MustCompile(`(?i)^(.*)_copy([0-9]+)$`)

func (p *planner) canvasCopyTitle(title string, nodes []map[string]any) string {
	base := strings.TrimSpace(canvasLegacyCopySuffix.ReplaceAllString(title, ""))
	if match := canvasCopySuffix.FindStringSubmatch(title); match != nil {
		base = match[1]
	}
	base = strings.TrimSpace(base)
	if base == "" {
		base = strings.TrimSpace(title)
	}
	if base == "" {
		base = "未命名节点"
	}
	maxIndex := 0
	for _, node := range nodes {
		match := canvasCopySuffix.FindStringSubmatch(p.stringValue(node["title"]))
		if len(match) == 3 && match[1] == base {
			index, err := strconv.Atoi(match[2])
			if err == nil && index <= 9007199254740991 {
				maxIndex = max(maxIndex, index)
			}
		}
	}
	return fmt.Sprintf("%s_copy%d", base, maxIndex+1)
}

type canvasDrawingArgs struct {
	SnapshotHash string            `json:"snapshotHash"`
	NodeID       string            `json:"nodeId"`
	Engine       string            `json:"engine"`
	Operations   []canvasDrawingOp `json:"operations"`
}

type canvasDrawingOp struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Record map[string]any `json:"record,omitempty"`
}

func (p *planner) canvasDrawingRecords(wrapper map[string]any) (map[string]map[string]any, error) {
	result := map[string]map[string]any{}
	snapshot, _ := wrapper["snapshot"].(map[string]any)
	if snapshot == nil {
		return nil, BadAuthRequest("绘图原生文档无效")
	}
	if p.stringValue(wrapper["engine"]) == "excalidraw" {
		for _, item := range p.creationMaps(snapshot["elements"]) {
			id := p.stringValue(item["id"])
			if id == "" || result[id] != nil {
				return nil, BadAuthRequest("绘图元素 ID 无效或重复")
			}
			result[id] = item
		}
		files, _ := snapshot["files"].(map[string]any)
		for id, value := range files {
			item, ok := value.(map[string]any)
			if !ok || result[id] != nil || p.stringValue(item["id"]) != id {
				return nil, BadAuthRequest("绘图文件 ID 无效")
			}
			result[id] = item
		}
	} else if p.stringValue(wrapper["engine"]) == "tldraw" {
		content := snapshot
		if document, ok := snapshot["document"].(map[string]any); ok {
			content = document
		}
		store, ok := content["store"].(map[string]any)
		if !ok || content["schema"] == nil {
			return nil, BadAuthRequest("tldraw 需要先在编辑器同步原生文档及 schema")
		}
		for id, value := range store {
			item, ok := value.(map[string]any)
			if !ok || p.stringValue(item["id"]) != id {
				return nil, BadAuthRequest("tldraw 记录 ID 无效")
			}
			result[id] = item
		}
	} else {
		return nil, BadAuthRequest("不支持的绘图引擎")
	}
	return result, nil
}

func (p *planner) canvasGraphMaxInputs(kind string) int {
	if kind == "media-conversion" {
		return 1
	}
	return 0
}

func (p *planner) canvasInteger(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case int64:
		return int(number), int64(int(number)) == number
	case float64:
		integer := int(number)
		return integer, float64(integer) == number
	default:
		return 0, false
	}
}

func (p *planner) canvasLayoutLane(nodeType string) (string, bool) {
	switch nodeType {
	case "video":
		return "video", true
	case "audio":
		return "audio", true
	case "image":
		return "image", true
	case "text", "markdown", "script", "batch-table":
		return "text", true
	default:

		return "", false
	}
}

func (p *planner) canvasLayoutNodes(doc map[string]any) []layout.Node {
	raw := p.creationMaps(doc["nodes"])
	nodes := make([]layout.Node, 0, len(raw))
	for _, node := range raw {
		id := p.stringValue(node["id"])
		if id == "" {
			continue
		}
		lane, supported := p.canvasLayoutLane(p.stringValue(node["type"]))
		item := layout.Node{ID: id, Type: p.stringValue(node["type"]), Lane: lane, NoMove: !supported}
		if position, ok := node["position"].(map[string]any); ok {
			if value, ok := p.canvasSafeNumber(position["x"]); ok {
				item.X, _ = value.(float64)
			}
			if value, ok := p.canvasSafeNumber(position["y"]); ok {
				item.Y, _ = value.(float64)
			}
		}
		if value, ok := p.canvasSafeNumber(node["width"]); ok {
			item.Width, _ = value.(float64)
		}
		if value, ok := p.canvasSafeNumber(node["height"]); ok {
			item.Height, _ = value.(float64)
		}
		if meta, ok := node["metadata"].(map[string]any); ok {
			item.Locked = meta["locked"] == true
		}
		nodes = append(nodes, item)
	}
	return nodes
}

var canvasLegacyCopySuffix = regexp.MustCompile(`(?i) Copy$`)

func (p *planner) canvasManualGraphInputError(nodes []map[string]any, from, to map[string]any, fromKind string, connections []map[string]any) error {
	targetType := p.stringValue(to["type"])
	if targetType == "media-conversion" && p.stringValue(from["type"]) != "image" && p.stringValue(from["type"]) != "video" {
		return BadAuthRequest("转换节点只接受图片或视频节点")
	}
	if targetType == "batch-table" && fromKind != "image" {
		return BadAuthRequest("批量创作表只接受图片输入")
	}
	mode := ""
	switch targetType {
	case "image", "video", "audio", "text":
		mode = targetType
	case "script":
		mode = "text"
	case "config":
		mode = p.stringValue(p.canvasNodeMetadata(to)["generationMode"])
		if mode == "" {
			mode = "image"
		}
	}
	if mode == "image" && (fromKind == "video" || fromKind == "audio") {
		return BadAuthRequest("图片生成节点不能连接参考视频或音频")
	}
	if mode == "text" && fromKind == "audio" {
		return BadAuthRequest("文本生成节点不能连接参考音频")
	}
	if mode == "audio" {
		if fromKind != "text" && fromKind != "character" {
			return BadAuthRequest("音频生成只接受文本或角色卡")
		}
		if fromKind == "character" {
			ids := map[string]bool{p.stringValue(from["id"]): true}
			for _, edge := range connections {
				if p.stringValue(edge["toNodeId"]) == p.stringValue(to["id"]) {
					ids[p.stringValue(edge["fromNodeId"])] = true
				}
			}
			count := 0
			for id := range ids {
				index := p.canvasNodeIndex(nodes, id)
				if index >= 0 {
					descriptor, known := p.canvasNodeCapabilityForNode(nodes[index])
					if known && descriptor.InputKind == "character" {
						count++
					}
				}
			}
			if count > 1 {
				return BadAuthRequest("角色配音一次只能连接一个角色卡")
			}
		}
	}
	return nil
}

type canvasNodeCapability = capability.Descriptor

func (p *planner) canvasNodeCapabilityForNode(node map[string]any) (canvasNodeCapability, bool) {
	meta, _ := node["metadata"].(map[string]any)
	return p.registry.ResolveNode(p.stringValue(node["type"]), p.stringValue(meta["workflowKind"]))
}

func (p *planner) canvasNodeCapabilityForType(nodeType string) (canvasNodeCapability, bool) {
	return p.registry.Resolve(nodeType)
}

func (p *planner) canvasNodeIndex(nodes []map[string]any, id string) int {
	for index, node := range nodes {
		if p.stringValue(node["id"]) == id {
			return index
		}
	}
	return -1
}

func (p *planner) canvasNodeMetadata(node map[string]any) map[string]any {
	meta, _ := node["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		node["metadata"] = meta
	}
	return meta
}

func (p *planner) canvasPatchMovesNode(patch map[string]any) bool {
	for _, key := range []string{"x", "y", "width", "height"} {
		if _, ok := patch[key]; ok {
			return true
		}
	}
	return false
}

func (p *planner) canvasPrevisApplyCharacterBinding(item map[string]any, operation canvasPrevisPatchOperation) error {
	provided := operation.CharacterAssetID != nil || operation.CharacterVersionID != nil || operation.ReferenceNodeID != nil || operation.CharacterName != nil
	if !provided {
		return nil
	}
	if operation.CharacterAssetID == nil || operation.CharacterVersionID == nil || operation.ReferenceNodeID == nil {
		return BadAuthRequest("角色绑定需要 characterAssetId、characterVersionId 和 referenceNodeId")
	}
	for value, label := range map[*string]string{
		operation.CharacterAssetID:   "characterAssetId",
		operation.CharacterVersionID: "characterVersionId",
		operation.ReferenceNodeID:    "referenceNodeId",
	} {
		if value == nil || strings.TrimSpace(*value) == "" {
			return BadAuthRequest(label + " 不能为空")
		}
		if err := validateCanvasID(*value, label, 120); err != nil {
			return err
		}
	}
	binding := map[string]any{
		"characterAssetId":   *operation.CharacterAssetID,
		"characterVersionId": *operation.CharacterVersionID,
		"referenceNodeId":    *operation.ReferenceNodeID,
	}
	if operation.CharacterName != nil {
		if err := p.canvasPrevisValidateText(*operation.CharacterName, "characterName", canvasPrevisTextLimit, true); err != nil {
			return err
		}
		binding["characterName"] = *operation.CharacterName
	}
	item["characterBinding"] = binding
	item["sourceNodeId"] = *operation.ReferenceNodeID
	return nil
}

func (p *planner) canvasPrevisApplyName(item map[string]any, value *string, label string) error {
	if value == nil {
		return nil
	}
	if err := p.canvasPrevisValidateText(*value, label, canvasPrevisTextLimit, true); err != nil {
		return err
	}
	item["name"] = *value
	return nil
}

func (p *planner) canvasPrevisApplyObjectPatch(objects *[]map[string]any, operation canvasPrevisPatchOperation, index int) (OperationSummary, error) {
	items := *objects
	byID, err := p.canvasPrevisIDSet(items, "导演对象 ID")
	if err != nil {
		return OperationSummary{}, err
	}
	if err := validateCanvasID(operation.ID, fmt.Sprintf("operations[%d].id", index), 80); err != nil {
		return OperationSummary{}, err
	}
	existing := byID[operation.ID]
	summary := ""
	switch operation.Type {
	case "object_add":
		if existing != nil {
			return OperationSummary{}, BadAuthRequest("新增对象 ID 已存在")
		}
		if operation.Kind != "primitive" && operation.Kind != "actor" {
			return OperationSummary{}, BadAuthRequest("新增对象 kind 只支持 primitive 或 actor")
		}
		primitive := operation.Primitive
		if primitive == "" {
			primitive = "box"
		}
		if primitive != "box" && primitive != "sphere" && primitive != "cylinder" && primitive != "plane" && primitive != "character" {
			return OperationSummary{}, BadAuthRequest("新增对象 primitive 不受支持")
		}
		name := "新对象"
		if operation.Name != nil {
			name = *operation.Name
		}
		if err := p.canvasPrevisValidateText(name, "对象名称", canvasPrevisTextLimit, true); err != nil {
			return OperationSummary{}, err
		}
		position, rotation, scale := []float64{0, 0.5, 0}, []float64{0, 0, 0}, []float64{1, 1, 1}
		if operation.Position != nil {
			position = *operation.Position
		}
		if operation.Rotation != nil {
			rotation = *operation.Rotation
		}
		if operation.Scale != nil {
			scale = *operation.Scale
		}
		if err := p.canvasPrevisValidateVec3(position, "position"); err != nil {
			return OperationSummary{}, err
		}
		if err := p.canvasPrevisValidateVec3(rotation, "rotation"); err != nil {
			return OperationSummary{}, err
		}
		if err := p.canvasPrevisValidateVec3(scale, "scale"); err != nil {
			return OperationSummary{}, err
		}
		if operation.Color != nil {
			if err := p.canvasPrevisValidateColor(*operation.Color, "对象颜色"); err != nil {
				return OperationSummary{}, err
			}
		}
		if operation.Pose != "" {
			if err := p.canvasPrevisValidatePose(operation.Pose); err != nil {
				return OperationSummary{}, err
			}
		}
		if operation.ParentID != nil && *operation.ParentID != "" {
			if err := validateCanvasID(*operation.ParentID, "对象 parentId", 80); err != nil {
				return OperationSummary{}, err
			}
		}
		defaultColor := "#8795a5"
		if operation.Kind == "actor" {

			actorCount := 0
			for _, obj := range items {
				if p.stringValue(obj["kind"]) == "actor" {
					actorCount++
				}
			}
			actorColors := []string{"#2f7de1", "#d84949", "#dfae3f", "#34a276", "#8b5cf6", "#e879f9"}
			defaultColor = actorColors[actorCount%len(actorColors)]
		}
		item := map[string]any{"id": operation.ID, "name": name, "kind": operation.Kind, "primitive": primitive, "transform": map[string]any{"position": p.canvasPrevisVec3(position), "rotation": p.canvasPrevisVec3(rotation), "scale": p.canvasPrevisVec3(scale)}, "color": defaultColor, "visible": true, "castShadow": true, "receiveShadow": true, "keyframes": []any{}}
		if operation.Color != nil {
			item["color"] = *operation.Color
		}
		if operation.Visible != nil {
			item["visible"] = *operation.Visible
		}
		if operation.Pose != "" {
			item["pose"] = operation.Pose
		}
		if operation.Kind == "actor" && operation.Pose == "" {
			item["pose"] = "stand"
		}
		if operation.Kind == "actor" {
			item["archetype"] = "adult"
			item["boneTracks"] = []any{}
			item["boneOverrides"] = map[string]any{}
			item["motionClips"] = []any{}
		}
		if operation.Archetype != "" {
			validArchetypes := map[string]bool{"adult": true, "child": true, "elderly": true, "man": true, "woman": true, "monster": true}
			if !validArchetypes[operation.Archetype] {
				return OperationSummary{}, BadAuthRequest("archetype 值不受支持")
			}
			item["archetype"] = operation.Archetype
		}
		if operation.ParentID != nil && *operation.ParentID != "" {
			item["parentId"] = *operation.ParentID
		}
		if err := p.canvasPrevisApplyCharacterBinding(item, operation); err != nil {
			return OperationSummary{}, err
		}
		items = append(items, item)
		summary = fmt.Sprintf("新增对象《%s》", name)
	case "object_update":
		if existing == nil {
			return OperationSummary{}, BadAuthRequest("要更新的对象不存在")
		}
		if err := p.canvasPrevisApplyName(existing, operation.Name, "对象名称"); err != nil {
			return OperationSummary{}, err
		}
		if operation.Color != nil {
			if err := p.canvasPrevisValidateColor(*operation.Color, "对象颜色"); err != nil {
				return OperationSummary{}, err
			}
			existing["color"] = *operation.Color
		}
		if operation.Visible != nil {
			existing["visible"] = *operation.Visible
		}
		if operation.Pose != "" {
			if err := p.canvasPrevisValidatePose(operation.Pose); err != nil {
				return OperationSummary{}, err
			}
			existing["pose"] = operation.Pose
		}
		if operation.ParentID != nil {
			if *operation.ParentID == "" {
				delete(existing, "parentId")
			} else {
				if err := validateCanvasID(*operation.ParentID, "对象 parentId", 80); err != nil {
					return OperationSummary{}, err
				}
				existing["parentId"] = *operation.ParentID
			}
		}
		if err := p.canvasPrevisApplyCharacterBinding(existing, operation); err != nil {
			return OperationSummary{}, err
		}
		if err := p.canvasPrevisApplyTransform(existing, operation); err != nil {
			return OperationSummary{}, err
		}
		summary = fmt.Sprintf("修改对象《%s》", p.stringValue(existing["name"]))
	case "object_remove":
		if existing == nil {
			return OperationSummary{}, BadAuthRequest("要删除的对象不存在")
		}
		for _, candidate := range items {
			if p.stringValue(candidate["parentId"]) == operation.ID {
				return OperationSummary{}, BadAuthRequest("存在子对象时不能删除父对象")
			}
		}
		for i, candidate := range items {
			if p.stringValue(candidate["id"]) == operation.ID {
				items = append(items[:i], items[i+1:]...)
				break
			}
		}
		summary = fmt.Sprintf("删除对象《%s》", p.stringValue(existing["name"]))
	default:
		return OperationSummary{}, nil
	}
	*objects = items
	nodeTitle := p.stringValue(existing["name"])
	if nodeTitle == "" && operation.Type == "object_add" {
		nodeTitle = "新对象"
		if operation.Name != nil {
			nodeTitle = *operation.Name
		}
	}
	return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.truncateRunes(nodeTitle, canvasPrevisTextLimit), Summary: summary}, nil
}

func (p *planner) canvasPrevisApplyPatchOperation(scene map[string]any, operation canvasPrevisPatchOperation, index int) (OperationSummary, error) {
	if err := p.canvasPrevisValidateText(operation.Type, fmt.Sprintf("operations[%d].type", index), 40, true); err != nil {
		return OperationSummary{}, err
	}
	if err := validateCanvasID(operation.ID, fmt.Sprintf("operations[%d].id", index), 80); err != nil {
		return OperationSummary{}, err
	}
	if operation.Type == "scene_update" && operation.ID != p.stringValue(scene["id"]) {
		return OperationSummary{}, BadAuthRequest("scene_update 的 id 必须是当前场景 ID")
	}
	objects, cameras, lights, shots := p.creationMaps(scene["objects"]), p.creationMaps(scene["cameras"]), p.creationMaps(scene["lights"]), p.creationMaps(scene["shots"])
	switch operation.Type {
	case "scene_update":
		if operation.Title != nil {
			if err := p.canvasPrevisValidateText(*operation.Title, "场景标题", canvasPrevisTextLimit, true); err != nil {
				return OperationSummary{}, err
			}
			scene["title"] = *operation.Title
		}
		if operation.Background != nil {
			if err := p.canvasPrevisValidateColor(*operation.Background, "场景背景色"); err != nil {
				return OperationSummary{}, err
			}
			scene["background"] = *operation.Background
		}
		if operation.EnvironmentIntensity != nil {
			if err := p.canvasPrevisFiniteInRange(*operation.EnvironmentIntensity, "environmentIntensity", 0, 100); err != nil {
				return OperationSummary{}, err
			}
			scene["environmentIntensity"] = *operation.EnvironmentIntensity
		}
		if operation.GridVisible != nil {
			scene["gridVisible"] = *operation.GridVisible
		}
		if operation.ActiveShotID != nil {
			if *operation.ActiveShotID == "" {
				return OperationSummary{}, BadAuthRequest("activeShotId 不能为空")
			}
			if err := validateCanvasID(*operation.ActiveShotID, "activeShotId", 80); err != nil {
				return OperationSummary{}, err
			}
			scene["activeShotId"] = *operation.ActiveShotID
		}
		return OperationSummary{Operation: operation.Type, NodeID: p.stringValue(scene["id"]), NodeTitle: p.stringValue(scene["title"]), Summary: "修改场景级设置"}, nil
	case "object_add", "object_update", "object_remove":
		item, err := p.canvasPrevisApplyObjectPatch(&objects, operation, index)
		if err == nil {
			scene["objects"] = objects
		}
		return item, err
	case "object_keyframe_upsert", "object_keyframe_remove", "object_motion_path_set", "object_motion_path_clear":
		object := p.canvasPrevisFindByID(objects, operation.ID)
		if object == nil {
			return OperationSummary{}, BadAuthRequest("目标对象不存在")
		}
		duration := p.canvasPrevisShotDuration(scene)
		var err error
		switch operation.Type {
		case "object_keyframe_upsert":
			err = p.canvasPrevisUpsertKeyframe(object, operation, duration)
		case "object_keyframe_remove":
			err = p.canvasPrevisRemoveKeyframe(object, operation)
		case "object_motion_path_set":
			err = p.canvasPrevisSetMotionPath(object, operation, duration)
		case "object_motion_path_clear":
			delete(object, "motionPath")
		}
		if err != nil {
			return OperationSummary{}, err
		}
		scene["objects"] = objects
		return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.stringValue(object["name"]), Summary: fmt.Sprintf("修改对象《%s》的动画", p.stringValue(object["name"]))}, nil
	case "shot_add":
		if p.canvasPrevisFindByID(shots, operation.ID) != nil {
			return OperationSummary{}, BadAuthRequest("新增镜头 ID 已存在")
		}
		cameraID := ""
		if operation.CameraID != nil {
			if err := validateCanvasID(*operation.CameraID, "摄影机 ID", 80); err != nil {
				return OperationSummary{}, err
			}
			cameraID = *operation.CameraID
		} else if len(cameras) > 0 {
			cameraID = p.stringValue(cameras[0]["id"])
		}
		if p.canvasPrevisFindByID(cameras, cameraID) == nil {
			return OperationSummary{}, BadAuthRequest("新增镜头必须引用现有摄影机")
		}
		name := "新镜头"
		if operation.Name != nil {
			name = *operation.Name
		}
		if err := p.canvasPrevisValidateText(name, "镜头名称", canvasPrevisTextLimit, true); err != nil {
			return OperationSummary{}, err
		}
		duration, fps := 5.0, 24
		if operation.Duration != nil {
			duration = *operation.Duration
		}
		if operation.FPS != nil {
			fps = *operation.FPS
		}
		if err := p.canvasPrevisFiniteInRange(duration, "镜头时长", 0.1, 60); err != nil {
			return OperationSummary{}, err
		}
		if fps < 1 || fps > 60 {
			return OperationSummary{}, BadAuthRequest("镜头帧率超出限制")
		}
		shotSize, cameraMove := operation.ShotSize, operation.CameraMove
		if shotSize == "" {
			shotSize = "medium"
		}
		if cameraMove == "" {
			cameraMove = "static"
		}
		if err := p.canvasPrevisValidateEnum(shotSize, "镜头景别", "extreme_wide", "wide", "full", "medium", "close_up", "extreme_close_up"); err != nil {
			return OperationSummary{}, err
		}
		if err := p.canvasPrevisValidateEnum(cameraMove, "镜头运镜", "static", "push_in", "pull_out", "pan_left", "pan_right", "tilt_up", "tilt_down", "orbit_left", "orbit_right", "handheld"); err != nil {
			return OperationSummary{}, err
		}
		shot := map[string]any{"id": operation.ID, "name": name, "cameraId": cameraID, "duration": duration, "fps": fps, "shotSize": shotSize, "cameraMove": cameraMove, "prompt": "", "cuts": []any{}}
		if operation.Prompt != nil {
			if err := p.canvasPrevisValidateText(*operation.Prompt, "镜头提示词", 2000, false); err != nil {
				return OperationSummary{}, err
			}
			shot["prompt"] = *operation.Prompt
		}
		scene["shots"] = append(shots, shot)
		if p.stringValue(scene["activeShotId"]) == "" {
			scene["activeShotId"] = operation.ID
		}
		return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: name, Summary: fmt.Sprintf("新增镜头《%s》", name)}, nil
	case "shot_update", "shot_remove":
		shot := p.canvasPrevisFindByID(shots, operation.ID)
		if shot == nil {
			return OperationSummary{}, BadAuthRequest("目标镜头不存在")
		}
		if operation.Type == "shot_remove" {
			if len(shots) <= 1 {
				return OperationSummary{}, BadAuthRequest("预演场景至少保留一个镜头")
			}
			next := make([]map[string]any, 0, len(shots)-1)
			for _, item := range shots {
				if p.stringValue(item["id"]) != operation.ID {
					next = append(next, item)
				}
			}
			scene["shots"] = next
			if p.stringValue(scene["activeShotId"]) == operation.ID {
				scene["activeShotId"] = p.stringValue(next[0]["id"])
			}
			return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.stringValue(shot["name"]), Summary: fmt.Sprintf("删除镜头《%s》", p.stringValue(shot["name"]))}, nil
		}
		if err := p.canvasPrevisApplyName(shot, operation.Name, "镜头名称"); err != nil {
			return OperationSummary{}, err
		}
		if operation.CameraID != nil {
			if err := validateCanvasID(*operation.CameraID, "摄影机 ID", 80); err != nil {
				return OperationSummary{}, err
			}
			if p.canvasPrevisFindByID(cameras, *operation.CameraID) == nil {
				return OperationSummary{}, BadAuthRequest("镜头引用的摄影机不存在")
			}
			shot["cameraId"] = *operation.CameraID
		}
		if operation.Duration != nil {
			if err := p.canvasPrevisFiniteInRange(*operation.Duration, "镜头时长", 0.1, 60); err != nil {
				return OperationSummary{}, err
			}
			shot["duration"] = *operation.Duration
		}
		if operation.FPS != nil {
			if *operation.FPS < 1 || *operation.FPS > 60 {
				return OperationSummary{}, BadAuthRequest("镜头帧率超出限制")
			}
			shot["fps"] = *operation.FPS
		}
		if operation.ShotSize != "" {
			if err := p.canvasPrevisValidateEnum(operation.ShotSize, "镜头景别", "extreme_wide", "wide", "full", "medium", "close_up", "extreme_close_up"); err != nil {
				return OperationSummary{}, err
			}
			shot["shotSize"] = operation.ShotSize
		}
		if operation.CameraMove != "" {
			if err := p.canvasPrevisValidateEnum(operation.CameraMove, "镜头运镜", "static", "push_in", "pull_out", "pan_left", "pan_right", "tilt_up", "tilt_down", "orbit_left", "orbit_right", "handheld"); err != nil {
				return OperationSummary{}, err
			}
			shot["cameraMove"] = operation.CameraMove
		}
		if operation.Prompt != nil {
			if err := p.canvasPrevisValidateText(*operation.Prompt, "镜头提示词", 2000, false); err != nil {
				return OperationSummary{}, err
			}
			shot["prompt"] = *operation.Prompt
		}
		return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.stringValue(shot["name"]), Summary: fmt.Sprintf("修改镜头《%s》", p.stringValue(shot["name"]))}, nil
	case "camera_keyframe_upsert", "camera_keyframe_remove":
		camera := p.canvasPrevisFindByID(cameras, operation.ID)
		if camera == nil {
			return OperationSummary{}, BadAuthRequest("目标摄影机不存在")
		}
		duration := p.canvasPrevisShotDuration(scene)
		var err error
		if operation.Type == "camera_keyframe_upsert" {
			err = p.canvasPrevisUpsertKeyframe(camera, operation, duration)
		} else {
			err = p.canvasPrevisRemoveKeyframe(camera, operation)
		}
		if err != nil {
			return OperationSummary{}, err
		}
		return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.stringValue(camera["name"]), Summary: fmt.Sprintf("修改摄影机《%s》的动画", p.stringValue(camera["name"]))}, nil
	case "camera_add":
		if p.canvasPrevisFindByID(cameras, operation.ID) != nil {
			return OperationSummary{}, BadAuthRequest("新增摄影机 ID 已存在")
		}
		name := "摄影机"
		if operation.Name != nil {
			name = *operation.Name
		}
		if err := p.canvasPrevisValidateText(name, "摄影机名称", canvasPrevisTextLimit, true); err != nil {
			return OperationSummary{}, err
		}
		camPos := []float64{4.8, 2.7, 6.8}
		camRot := []float64{0, 0, 0}
		camScale := []float64{1, 1, 1}
		camTarget := []float64{0, 1, 0}
		if operation.Position != nil {
			camPos = *operation.Position
		}
		if operation.Rotation != nil {
			camRot = *operation.Rotation
		}
		if operation.Scale != nil {
			camScale = *operation.Scale
		}
		if operation.Target != nil {
			camTarget = *operation.Target
		}
		if err := p.canvasPrevisValidateVec3(camPos, "摄影机 position"); err != nil {
			return OperationSummary{}, err
		}
		if err := p.canvasPrevisValidateVec3(camTarget, "摄影机 target"); err != nil {
			return OperationSummary{}, err
		}
		focalLength := 35.0
		if operation.FocalLength != nil {
			focalLength = *operation.FocalLength
		}
		if err := p.canvasPrevisFiniteInRange(focalLength, "摄影机焦段", 1, 300); err != nil {
			return OperationSummary{}, err
		}
		fov := 2 * math.Atan(36.0/(2*math.Max(1, focalLength))) * 180 / math.Pi
		newCamera := map[string]any{
			"id": operation.ID, "name": name,
			"transform": map[string]any{"position": p.canvasPrevisVec3(camPos), "rotation": p.canvasPrevisVec3(camRot), "scale": p.canvasPrevisVec3(camScale)},
			"target":    p.canvasPrevisVec3(camTarget), "focalLength": focalLength, "fov": fov,
			"aperture": 2.8, "focusDistance": 5.0, "near": 0.05, "far": 500.0, "keyframes": []any{},
		}
		scene["cameras"] = append(cameras, newCamera)
		return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: name, Summary: fmt.Sprintf("新增摄影机《%s》", name)}, nil
	case "camera_update", "camera_remove":
		camera := p.canvasPrevisFindByID(cameras, operation.ID)
		if camera == nil {
			return OperationSummary{}, BadAuthRequest("目标摄影机不存在")
		}
		if operation.Type == "camera_remove" {
			if len(cameras) <= 1 {
				return OperationSummary{}, BadAuthRequest("预演场景至少保留一台摄影机")
			}
			for _, shot := range shots {
				if p.stringValue(shot["cameraId"]) == operation.ID {
					return OperationSummary{}, BadAuthRequest("摄影机仍被镜头引用，不能删除")
				}
			}
			next := make([]map[string]any, 0, len(cameras)-1)
			for _, item := range cameras {
				if p.stringValue(item["id"]) != operation.ID {
					next = append(next, item)
				}
			}
			scene["cameras"] = next
			return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.stringValue(camera["name"]), Summary: fmt.Sprintf("删除摄影机《%s》", p.stringValue(camera["name"]))}, nil
		}
		if err := p.canvasPrevisApplyName(camera, operation.Name, "摄影机名称"); err != nil {
			return OperationSummary{}, err
		}
		if operation.FocalLength != nil {
			if err := p.canvasPrevisFiniteInRange(*operation.FocalLength, "摄影机焦段", 1, 300); err != nil {
				return OperationSummary{}, err
			}
			camera["focalLength"] = *operation.FocalLength
		}
		if operation.Target != nil {
			if err := p.canvasPrevisValidateVec3(*operation.Target, "target"); err != nil {
				return OperationSummary{}, err
			}
			camera["target"] = p.canvasPrevisVec3(*operation.Target)
		}
		if err := p.canvasPrevisApplyTransform(camera, operation); err != nil {
			return OperationSummary{}, err
		}
		return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.stringValue(camera["name"]), Summary: fmt.Sprintf("修改摄影机《%s》", p.stringValue(camera["name"]))}, nil
	case "light_add", "light_update", "light_remove":
		if operation.Type == "light_add" {
			if p.canvasPrevisFindByID(lights, operation.ID) != nil {
				return OperationSummary{}, BadAuthRequest("新增灯光 ID 已存在")
			}
			name := "新灯光"
			if operation.Name != nil {
				name = *operation.Name
			}
			if err := p.canvasPrevisValidateText(name, "灯光名称", canvasPrevisTextLimit, true); err != nil {
				return OperationSummary{}, err
			}
			lightType := "point"
			if operation.LightType != nil {
				lightType = *operation.LightType
			}
			if err := p.canvasPrevisValidateEnum(lightType, "灯光类型", "directional", "point", "spot", "ambient"); err != nil {
				return OperationSummary{}, err
			}
			position, rotation, scale := []float64{0, 3, 0}, []float64{0, 0, 0}, []float64{1, 1, 1}
			if operation.Position != nil {
				position = *operation.Position
			}
			if operation.Rotation != nil {
				rotation = *operation.Rotation
			}
			if operation.Scale != nil {
				scale = *operation.Scale
			}
			if err := p.canvasPrevisValidateVec3(position, "position"); err != nil {
				return OperationSummary{}, err
			}
			if err := p.canvasPrevisValidateVec3(rotation, "rotation"); err != nil {
				return OperationSummary{}, err
			}
			if err := p.canvasPrevisValidateVec3(scale, "scale"); err != nil {
				return OperationSummary{}, err
			}
			intensity := 1.0
			if operation.Intensity != nil {
				intensity = *operation.Intensity
			}
			if err := p.canvasPrevisFiniteInRange(intensity, "灯光强度", 0, 100); err != nil {
				return OperationSummary{}, err
			}
			angle, penumbra := math.Pi/4, 0.35
			if operation.Angle != nil {
				angle = *operation.Angle
			}
			if operation.Penumbra != nil {
				penumbra = *operation.Penumbra
			}
			if err := p.canvasPrevisFiniteInRange(angle, "灯光角度", 0, 6.29); err != nil {
				return OperationSummary{}, err
			}
			if err := p.canvasPrevisFiniteInRange(penumbra, "灯光 penumbra", 0, 1); err != nil {
				return OperationSummary{}, err
			}
			color := "#ffffff"
			if operation.Color != nil {
				if err := p.canvasPrevisValidateColor(*operation.Color, "灯光颜色"); err != nil {
					return OperationSummary{}, err
				}
				color = *operation.Color
			}
			castShadow := lightType != "ambient"
			if operation.CastShadow != nil {
				castShadow = *operation.CastShadow
			}
			light := map[string]any{"id": operation.ID, "name": name, "type": lightType, "transform": map[string]any{"position": p.canvasPrevisVec3(position), "rotation": p.canvasPrevisVec3(rotation), "scale": p.canvasPrevisVec3(scale)}, "color": color, "intensity": intensity, "angle": angle, "penumbra": penumbra, "castShadow": castShadow}
			scene["lights"] = append(lights, light)
			return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: name, Summary: fmt.Sprintf("新增灯光《%s》", name)}, nil
		}
		light := p.canvasPrevisFindByID(lights, operation.ID)
		if light == nil {
			return OperationSummary{}, BadAuthRequest("目标灯光不存在")
		}
		if operation.Type == "light_remove" {
			next := make([]map[string]any, 0, len(lights)-1)
			for _, item := range lights {
				if p.stringValue(item["id"]) != operation.ID {
					next = append(next, item)
				}
			}
			scene["lights"] = next
			return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.stringValue(light["name"]), Summary: fmt.Sprintf("删除灯光《%s》", p.stringValue(light["name"]))}, nil
		}
		if err := p.canvasPrevisApplyName(light, operation.Name, "灯光名称"); err != nil {
			return OperationSummary{}, err
		}
		if operation.LightType != nil {
			if err := p.canvasPrevisValidateEnum(*operation.LightType, "灯光类型", "directional", "point", "spot", "ambient"); err != nil {
				return OperationSummary{}, err
			}
			light["type"] = *operation.LightType
		}
		if operation.Intensity != nil {
			if err := p.canvasPrevisFiniteInRange(*operation.Intensity, "灯光强度", 0, 100); err != nil {
				return OperationSummary{}, err
			}
			light["intensity"] = *operation.Intensity
		}
		if operation.Color != nil {
			if err := p.canvasPrevisValidateColor(*operation.Color, "灯光颜色"); err != nil {
				return OperationSummary{}, err
			}
			light["color"] = *operation.Color
		}
		if operation.Angle != nil {
			if err := p.canvasPrevisFiniteInRange(*operation.Angle, "灯光角度", 0, 6.29); err != nil {
				return OperationSummary{}, err
			}
			light["angle"] = *operation.Angle
		}
		if operation.Penumbra != nil {
			if err := p.canvasPrevisFiniteInRange(*operation.Penumbra, "灯光 penumbra", 0, 1); err != nil {
				return OperationSummary{}, err
			}
			light["penumbra"] = *operation.Penumbra
		}
		if operation.CastShadow != nil {
			light["castShadow"] = *operation.CastShadow
		}
		if err := p.canvasPrevisApplyTransform(light, operation); err != nil {
			return OperationSummary{}, err
		}
		return OperationSummary{Operation: operation.Type, NodeID: operation.ID, NodeTitle: p.stringValue(light["name"]), Summary: fmt.Sprintf("修改灯光《%s》", p.stringValue(light["name"]))}, nil
	default:
		return OperationSummary{}, BadAuthRequest("不支持的预演台语义操作")
	}
}

func (p *planner) canvasPrevisApplyTransform(item map[string]any, operation canvasPrevisPatchOperation) error {
	if err := p.canvasPrevisOptionalVec3(operation.Position, "position"); err != nil {
		return err
	}
	if err := p.canvasPrevisOptionalVec3(operation.Rotation, "rotation"); err != nil {
		return err
	}
	if err := p.canvasPrevisOptionalVec3(operation.Scale, "scale"); err != nil {
		return err
	}
	transform := p.canvasPrevisMapTransform(item)
	if operation.Position != nil {
		transform["position"] = p.canvasPrevisVec3(*operation.Position)
	}
	if operation.Rotation != nil {
		transform["rotation"] = p.canvasPrevisVec3(*operation.Rotation)
	}
	if operation.Scale != nil {
		transform["scale"] = p.canvasPrevisVec3(*operation.Scale)
	}
	return nil
}

const canvasPrevisDocumentLimit = 1 << 20

func (p *planner) canvasPrevisFindByID(items []map[string]any, id string) map[string]any {
	for _, item := range items {
		if p.stringValue(item["id"]) == id {
			return item
		}
	}
	return nil
}

func (p *planner) canvasPrevisFinite(value any) bool {
	number := p.numberValue(value, math.NaN())
	return !math.IsNaN(number) && !math.IsInf(number, 0)
}

func (p *planner) canvasPrevisFiniteInRange(value float64, label string, minimum, maximum float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < minimum || value > maximum {
		return BadAuthRequest(label + "超出限制")
	}
	return nil
}

func (p *planner) canvasPrevisIDSet(items []map[string]any, label string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(items))
	for _, item := range items {
		id := p.stringValue(item["id"])
		if err := validateCanvasID(id, label, 80); err != nil {
			return nil, err
		}
		if _, exists := result[id]; exists {
			return nil, BadAuthRequest(label + "重复")
		}
		result[id] = item
	}
	return result, nil
}

func (p *planner) canvasPrevisIsHexColor(value string) bool {
	if len(value) != 4 && len(value) != 7 && len(value) != 9 || value[0] != '#' {
		return false
	}
	for _, r := range value[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func (p *planner) canvasPrevisKeyframeTransform(item map[string]any, operation canvasPrevisPatchOperation) (map[string]any, error) {
	transform := p.canvasPrevisMapTransform(item)
	result := map[string]any{}
	for _, key := range []string{"position", "rotation", "scale"} {
		value, ok := p.canvasPrevisNumberVec3(transform[key])
		if !ok {
			return nil, BadAuthRequest("关键帧目标对象 transform 无效")
		}
		result[key] = p.canvasPrevisVec3(value)
	}
	for value, key := range map[*[]float64]string{operation.Position: "position", operation.Rotation: "rotation", operation.Scale: "scale"} {
		if value == nil {
			continue
		}
		if err := p.canvasPrevisValidateVec3(*value, key); err != nil {
			return nil, err
		}
		result[key] = p.canvasPrevisVec3(*value)
	}
	return result, nil
}

func (p *planner) canvasPrevisMapTransform(item map[string]any) map[string]any {
	if transform, ok := item["transform"].(map[string]any); ok {
		return transform
	}
	transform := map[string]any{"position": []any{0, 0, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}
	item["transform"] = transform
	return transform
}

const canvasPrevisMaxCameras = 32

const canvasPrevisMaxLights = 64

const canvasPrevisMaxObjects = 128

const canvasPrevisMaxOperations = 32

const canvasPrevisMaxScenes = 32

const canvasPrevisMaxShots = 64

func (p *planner) canvasPrevisNormalizeLegacyColor(value string) string {
	trimmed := strings.TrimSpace(value)
	if p.canvasPrevisIsHexColor(trimmed) {
		return strings.ToLower(trimmed)
	}
	lower := strings.ToLower(trimmed)
	var raw string
	var rgba bool
	switch {
	case strings.HasPrefix(lower, "rgb(") && strings.HasSuffix(lower, ")"):
		raw = trimmed[4 : len(trimmed)-1]
	case strings.HasPrefix(lower, "rgba(") && strings.HasSuffix(lower, ")"):
		raw = trimmed[5 : len(trimmed)-1]
		rgba = true
	default:
		return value
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 3 && len(parts) != 4 {
		return value
	}
	channels := make([]int, 3)
	for index := range channels {
		channel, err := strconv.Atoi(strings.TrimSpace(parts[index]))
		if err != nil || channel < 0 || channel > 255 {
			return value
		}
		channels[index] = channel
	}
	if !rgba && len(parts) == 4 {
		return value
	}
	result := fmt.Sprintf("#%02x%02x%02x", channels[0], channels[1], channels[2])
	if rgba && len(parts) == 4 {
		alpha, err := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
		if err != nil {
			return value
		}
		if alpha <= 1 {
			alpha = math.Round(alpha * 255)
		}
		if alpha < 0 || alpha > 255 {
			return value
		}
		result += fmt.Sprintf("%02x", int(alpha))
	}
	return result
}

func (p *planner) canvasPrevisNormalizeSceneColor(value, fallback string) string {
	normalized := p.canvasPrevisNormalizeLegacyColor(value)
	if p.canvasPrevisIsHexColor(normalized) {
		return normalized
	}
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (p *planner) canvasPrevisNumberVec3(value any) ([]float64, bool) {
	result := make([]float64, 3)
	switch items := value.(type) {
	case []float64:
		if len(items) != 3 {
			return nil, false
		}
		copy(result, items)
	case []any:
		if len(items) != 3 {
			return nil, false
		}
		for index, item := range items {
			result[index] = p.numberValue(item, math.NaN())
		}
	default:
		return nil, false
	}
	for _, number := range result {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, false
		}
	}
	return result, true
}

func (p *planner) canvasPrevisOptionalVec3(value *[]float64, label string) error {
	if value == nil {
		return nil
	}
	return p.canvasPrevisValidateVec3(*value, label)
}

type canvasPrevisPatchOperation struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	Name      *string `json:"name"`
	Title     *string `json:"title"`
	Kind      string  `json:"kind"`
	Primitive string  `json:"primitive"`
	Archetype string  `json:"archetype"`
	Color     *string `json:"color"`
	ParentID  *string `json:"parentId"`
	Visible   *bool   `json:"visible"`
	Pose      string  `json:"pose"`

	CharacterAssetID   *string `json:"characterAssetId"`
	CharacterVersionID *string `json:"characterVersionId"`
	ReferenceNodeID    *string `json:"referenceNodeId"`
	CharacterName      *string `json:"characterName"`

	Position *[]float64 `json:"position"`
	Rotation *[]float64 `json:"rotation"`
	Scale    *[]float64 `json:"scale"`
	Target   *[]float64 `json:"target"`

	KeyframeID   *string     `json:"keyframeId"`
	Time         *float64    `json:"time"`
	Easing       string      `json:"easing"`
	PathPoints   [][]float64 `json:"pathPoints"`
	PathSpeed    string      `json:"pathSpeed"`
	StartDelay   *float64    `json:"startDelay"`
	OrientToPath *bool       `json:"orientToPath"`

	CameraID    *string  `json:"cameraId"`
	Duration    *float64 `json:"duration"`
	FPS         *int     `json:"fps"`
	ShotSize    string   `json:"shotSize"`
	CameraMove  string   `json:"cameraMove"`
	Prompt      *string  `json:"prompt"`
	FocalLength *float64 `json:"focalLength"`

	LightType  *string  `json:"lightType"`
	Intensity  *float64 `json:"intensity"`
	Angle      *float64 `json:"angle"`
	Penumbra   *float64 `json:"penumbra"`
	CastShadow *bool    `json:"castShadow"`

	Background           *string  `json:"background"`
	EnvironmentIntensity *float64 `json:"environmentIntensity"`
	GridVisible          *bool    `json:"gridVisible"`
	ActiveShotID         *string  `json:"activeShotId"`
}

func (p *planner) canvasPrevisRemoveKeyframe(item map[string]any, operation canvasPrevisPatchOperation) error {
	if operation.KeyframeID == nil || strings.TrimSpace(*operation.KeyframeID) == "" {
		return BadAuthRequest("删除关键帧需要 keyframeId")
	}
	if err := validateCanvasID(*operation.KeyframeID, "keyframeId", 120); err != nil {
		return err
	}
	keyframes := p.creationMaps(item["keyframes"])
	filtered := make([]map[string]any, 0, len(keyframes))
	for _, keyframe := range keyframes {
		if p.stringValue(keyframe["id"]) != *operation.KeyframeID {
			filtered = append(filtered, keyframe)
		}
	}
	if len(filtered) == len(keyframes) {
		return BadAuthRequest("目标关键帧不存在")
	}
	item["keyframes"] = filtered
	return nil
}

type canvasPrevisSceneCreateArgs struct {
	CanvasSnapshotHash string `json:"canvasSnapshotHash"`
	SceneID            string `json:"sceneId"`
	Title              string `json:"title"`
	TemplateID         string `json:"templateId"`
}

func (p *planner) canvasPrevisSceneCreateTemplate(args canvasPrevisSceneCreateArgs) (map[string]any, error) {
	if err := validateCanvasID(args.SceneID, "导演场景 ID", 80); err != nil {
		return nil, err
	}
	if err := p.canvasPrevisValidateText(args.Title, "场景标题", canvasPrevisTextLimit, true); err != nil {
		return nil, err
	}
	if err := p.canvasPrevisValidateTemplateID(args.TemplateID); err != nil {
		return nil, err
	}
	now := p.now().UTC().Format(time.RFC3339Nano)
	cameraID := args.SceneID + "-camera-1"
	shotID := args.SceneID + "-shot-1"
	camera := map[string]any{
		"id": cameraID, "name": "主摄影机",
		"transform": map[string]any{"position": []any{4.8, 2.7, 6.8}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}},
		"target":    []any{0, 1, 0}, "focalLength": 35, "fov": 50, "aperture": 2.8, "focusDistance": 5, "near": 0.05, "far": 500, "keyframes": []any{},
	}
	objects := []any{}
	lights := []any{
		map[string]any{"id": args.SceneID + "-light-key", "name": "主光", "type": "directional", "transform": map[string]any{"position": []any{4, 6, 4}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#ffffff", "intensity": 2.4, "angle": 0.7853981634, "penumbra": 0.35, "castShadow": true},
		map[string]any{"id": args.SceneID + "-light-fill", "name": "轮廓光", "type": "directional", "transform": map[string]any{"position": []any{-4, 3, -2}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#ffffff", "intensity": 1.1, "angle": 0.7853981634, "penumbra": 0.35, "castShadow": true},
		map[string]any{"id": args.SceneID + "-light-ambient", "name": "环境光", "type": "ambient", "transform": map[string]any{"position": []any{0, 0, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#ffffff", "intensity": 0.65, "angle": 0.7853981634, "penumbra": 0.35, "castShadow": false},
	}
	if args.TemplateID == "action_chase" {

		objects = append(objects,
			map[string]any{"id": args.SceneID + "-actor-1", "name": "追逐者", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{0, 0, -1.2}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#2f7de1", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "run", "archetype": "man", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}},
			map[string]any{"id": args.SceneID + "-actor-2", "name": "逃跑者", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{0, 0, 2.0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#d84949", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "run", "archetype": "adult", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}},
		)
		camera["transform"] = map[string]any{"position": []any{3.8, 1.4, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}
		camera["target"] = []any{0, 1, 0.5}
		camera["focalLength"] = 50
		camera["fov"] = 39.6
	}
	if args.TemplateID == "interior" {

		objects = append(objects,
			map[string]any{"id": args.SceneID + "-wall-back", "name": "后墙", "kind": "primitive", "primitive": "plane", "transform": map[string]any{"position": []any{0, 2.0, -3.0}, "rotation": []any{0, 0, 0}, "scale": []any{6, 4, 1}}, "color": "#d8dde3", "visible": true, "castShadow": false, "receiveShadow": true, "keyframes": []any{}},
			map[string]any{"id": args.SceneID + "-wall-left", "name": "左墙", "kind": "primitive", "primitive": "plane", "transform": map[string]any{"position": []any{-3.0, 2.0, 0}, "rotation": []any{0, 1.5708, 0}, "scale": []any{6, 4, 1}}, "color": "#d8dde3", "visible": true, "castShadow": false, "receiveShadow": true, "keyframes": []any{}},
			map[string]any{"id": args.SceneID + "-sofa", "name": "沙发", "kind": "primitive", "primitive": "box", "transform": map[string]any{"position": []any{0, 0.45, -1.8}, "rotation": []any{0, 0, 0}, "scale": []any{2.2, 0.9, 0.9}}, "color": "#8795a5", "visible": true, "castShadow": true, "receiveShadow": true, "keyframes": []any{}},
			map[string]any{"id": args.SceneID + "-table", "name": "茶几", "kind": "primitive", "primitive": "box", "transform": map[string]any{"position": []any{0, 0.23, 0.2}, "rotation": []any{0, 0, 0}, "scale": []any{1.1, 0.46, 0.65}}, "color": "#6b7580", "visible": true, "castShadow": true, "receiveShadow": true, "keyframes": []any{}},
			map[string]any{"id": args.SceneID + "-actor-1", "name": "演员 1", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{-0.6, 0, 0.6}, "rotation": []any{0, -0.4, 0}, "scale": []any{1, 1, 1}}, "color": "#2f7de1", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "sit", "archetype": "adult", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}},
		)
		camera["transform"] = map[string]any{"position": []any{3.2, 1.65, 3.0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}
		camera["target"] = []any{-0.2, 1.0, 0}
		camera["focalLength"] = 35
	}
	if args.TemplateID == "crowd" {

		crowdColors := []string{"#2f7de1", "#d84949", "#dfae3f", "#34a276", "#8b5cf6", "#e879f9", "#2f7de1", "#d84949", "#dfae3f"}
		for row := 0; row < 3; row++ {
			for col := 0; col < 3; col++ {
				idx := row*3 + col
				id := fmt.Sprintf("%s-actor-%d", args.SceneID, idx+1)
				x := float64(col-1) * 1.2
				z := float64(row-1) * 1.2
				objects = append(objects, map[string]any{
					"id": id, "name": fmt.Sprintf("群众 %d", idx+1), "kind": "actor", "primitive": "character",
					"transform": map[string]any{"position": []any{x, 0, z}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}},
					"color":     crowdColors[idx], "visible": true, "castShadow": true, "receiveShadow": true,
					"pose": "stand", "archetype": "adult", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{},
				})
			}
		}
		camera["transform"] = map[string]any{"position": []any{0.5, 4.5, 6.5}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}
		camera["target"] = []any{0, 0.8, 0}
		camera["focalLength"] = 28
		camera["fov"] = 65.0
	}
	if args.TemplateID == "monologue" || args.TemplateID == "dialogue" || args.TemplateID == "blocking" {
		objects = append(objects, map[string]any{"id": args.SceneID + "-actor-1", "name": "演员 1", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{0, 0, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1, 1, 1}}, "color": "#f1f3f5", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "stand", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}})
	}
	if args.TemplateID == "dialogue" || args.TemplateID == "blocking" {
		objects = append(objects, map[string]any{"id": args.SceneID + "-actor-2", "name": "演员 2", "kind": "actor", "primitive": "character", "transform": map[string]any{"position": []any{1.8, 0, -0.8}, "rotation": []any{0, 0.2, 0}, "scale": []any{1, 1, 1}}, "color": "#2f7de1", "visible": true, "castShadow": true, "receiveShadow": true, "pose": "stand", "keyframes": []any{}, "boneTracks": []any{}, "boneOverrides": map[string]any{}, "motionClips": []any{}})
	}
	if args.TemplateID == "product" {
		objects = append(objects, map[string]any{"id": args.SceneID + "-product-1", "name": "产品", "kind": "primitive", "primitive": "box", "transform": map[string]any{"position": []any{0, 0.6, 0}, "rotation": []any{0, 0, 0}, "scale": []any{1.2, 1.2, 1.2}}, "color": "#8795a5", "visible": true, "castShadow": true, "receiveShadow": true, "keyframes": []any{}})
	}
	scene := map[string]any{
		"id": args.SceneID, "version": 1, "title": args.Title, "background": "#d8dde3", "environmentIntensity": 0.7, "gridVisible": true,
		"objects": objects, "cameras": []any{camera}, "lights": lights,
		"shots":        []any{map[string]any{"id": shotID, "name": "镜头 1", "cameraId": cameraID, "duration": 5, "fps": 24, "shotSize": "medium", "cameraMove": "static", "prompt": "", "cuts": []any{}}},
		"activeShotId": shotID, "createdAt": now, "updatedAt": now,
	}
	return scene, p.canvasPrevisValidateScene(scene)
}

func (p *planner) canvasPrevisScenesSize(scenes []map[string]any) error {
	encoded, err := json.Marshal(scenes)
	if err != nil || len(encoded) > canvasPrevisDocumentLimit {
		return BadAuthRequest("预演台场景数据超过限制")
	}
	return nil
}

func (p *planner) canvasPrevisSetMotionPath(item map[string]any, operation canvasPrevisPatchOperation, duration float64) error {
	if len(operation.PathPoints) < 2 || len(operation.PathPoints) > 64 {
		return BadAuthRequest("motion path 至少需要2个、最多64个点")
	}
	for index, point := range operation.PathPoints {
		if err := p.canvasPrevisValidateVec3(point, fmt.Sprintf("pathPoints[%d]", index)); err != nil {
			return err
		}
	}
	speed := operation.PathSpeed
	if speed == "" {
		speed = "walk"
	}
	if err := p.canvasPrevisValidateEnum(speed, "pathSpeed", "slow", "walk", "run"); err != nil {
		return err
	}
	startDelay := 0.0
	if operation.StartDelay != nil {
		startDelay = *operation.StartDelay
	}
	if err := p.canvasPrevisFiniteInRange(startDelay, "startDelay", 0, duration); err != nil {
		return err
	}
	orient := false
	if operation.OrientToPath != nil {
		orient = *operation.OrientToPath
	}
	points := make([]any, 0, len(operation.PathPoints))
	for _, point := range operation.PathPoints {
		points = append(points, p.canvasPrevisVec3(point))
	}
	item["motionPath"] = map[string]any{"points": points, "speed": speed, "startDelay": startDelay, "orientToPath": orient}
	return nil
}

func (p *planner) canvasPrevisShotDuration(scene map[string]any) float64 {
	for _, shot := range p.creationMaps(scene["shots"]) {
		if p.stringValue(shot["id"]) == p.stringValue(scene["activeShotId"]) {
			return p.numberValue(shot["duration"], 5)
		}
	}
	return 5
}

const canvasPrevisTextLimit = 160

func (p *planner) canvasPrevisUpsertKeyframe(item map[string]any, operation canvasPrevisPatchOperation, duration float64) error {
	if operation.Time == nil {
		return BadAuthRequest("关键帧操作需要 time")
	}
	if err := p.canvasPrevisFiniteInRange(*operation.Time, "关键帧 time", 0, duration); err != nil {
		return err
	}
	if err := p.canvasPrevisValidateEasing(operation.Easing); err != nil {
		return err
	}
	transform, err := p.canvasPrevisKeyframeTransform(item, operation)
	if err != nil {
		return err
	}
	keyframes := p.creationMaps(item["keyframes"])
	keyframeID := ""
	if operation.KeyframeID != nil {
		if err := validateCanvasID(*operation.KeyframeID, "keyframeId", 120); err != nil {
			return err
		}
		keyframeID = *operation.KeyframeID
	}
	match := -1
	for index, keyframe := range keyframes {
		if (keyframeID != "" && p.stringValue(keyframe["id"]) == keyframeID) || math.Abs(p.numberValue(keyframe["time"], math.NaN())-*operation.Time) < 0.001 {
			match = index
			break
		}
	}
	if match < 0 {
		if keyframeID == "" {
			keyframeID = fmt.Sprintf("%s-kf-%d", p.stringValue(item["id"]), len(keyframes)+1)
		}
		keyframe := map[string]any{"id": keyframeID, "time": *operation.Time, "transform": transform}
		if operation.Easing != "" {
			keyframe["easing"] = operation.Easing
		}
		keyframes = append(keyframes, keyframe)
	} else {
		keyframes[match]["id"] = p.keyframeIDOrExisting(keyframeID, keyframes[match])
		keyframes[match]["time"] = *operation.Time
		keyframes[match]["transform"] = transform
		if operation.Easing == "" {
			delete(keyframes[match], "easing")
		} else {
			keyframes[match]["easing"] = operation.Easing
		}
	}
	for i := range keyframes {
		for j := i + 1; j < len(keyframes); j++ {
			if p.numberValue(keyframes[j]["time"], 0) < p.numberValue(keyframes[i]["time"], 0) {
				keyframes[i], keyframes[j] = keyframes[j], keyframes[i]
			}
		}
	}
	item["keyframes"] = keyframes
	return nil
}

func (p *planner) canvasPrevisValidateCharacterBindingInCanvas(doc map[string]any, operation canvasPrevisPatchOperation) error {
	if operation.CharacterAssetID == nil && operation.CharacterVersionID == nil && operation.ReferenceNodeID == nil && operation.CharacterName == nil {
		return nil
	}
	if operation.CharacterAssetID == nil || operation.CharacterVersionID == nil || operation.ReferenceNodeID == nil {
		return BadAuthRequest("角色绑定需要 characterAssetId、characterVersionId 和 referenceNodeId")
	}
	for _, node := range p.creationMaps(doc["nodes"]) {
		if p.stringValue(node["id"]) != *operation.ReferenceNodeID {
			continue
		}
		metadata, _ := node["metadata"].(map[string]any)
		if p.stringValue(metadata["workflowKind"]) != "character" || p.stringValue(metadata["characterAssetId"]) != *operation.CharacterAssetID || p.stringValue(metadata["characterVersionId"]) != *operation.CharacterVersionID {
			return BadAuthRequest("referenceNodeId 必须是匹配 characterAssetId 和 characterVersionId 的角色卡节点")
		}
		return nil
	}
	return BadAuthRequest("referenceNodeId 不属于当前画布")
}

func (p *planner) canvasPrevisValidateColor(value, label string) error {
	if err := p.canvasPrevisValidateText(value, label, 32, true); err != nil {
		return err
	}
	if !p.canvasPrevisIsHexColor(value) {
		return BadAuthRequest(label + "必须是十六进制颜色")
	}
	return nil
}

func (p *planner) canvasPrevisValidateEasing(value string) error {
	if value == "" {
		return nil
	}
	return p.canvasPrevisValidateEnum(value, "关键帧 easing", "step", "linear", "smooth")
}

func (p *planner) canvasPrevisValidateEnum(value, label string, allowed ...string) error {
	for _, candidate := range allowed {
		if value == candidate {
			return nil
		}
	}
	return BadAuthRequest(label + "不受支持")
}

func (p *planner) canvasPrevisValidatePose(pose string) error {
	valid := map[string]bool{
		"stand": true, "sit": true, "walk": true, "run": true, "wave": true, "think": true,
		"fight": true, "kick": true, "throw": true, "push": true, "reach": true, "arms_crossed": true,
		"phone": true, "bow": true, "kneel_single": true, "kneel_double": true, "lean": true,
		"squat": true, "hands_hips": true, "neutral": true, "t_pose": true,
	}
	if !valid[pose] {
		return BadAuthRequest(fmt.Sprintf("pose 值 %q 不受支持；请使用 stand/sit/walk/run/wave/think/fight 等", pose))
	}
	return nil
}

func (p *planner) canvasPrevisValidateScene(scene map[string]any) error {
	if err := validateCanvasID(p.stringValue(scene["id"]), "导演场景 ID", 80); err != nil {
		return err
	}
	if p.numberValue(scene["version"], 0) != 1 {
		return BadAuthRequest("预演台场景版本不受支持")
	}
	if err := p.canvasPrevisValidateText(p.stringValue(scene["title"]), "场景标题", canvasPrevisTextLimit, true); err != nil {
		return err
	}
	background := p.canvasPrevisNormalizeSceneColor(p.stringValue(scene["background"]), "#111827")
	scene["background"] = background
	if err := p.canvasPrevisValidateColor(p.stringValue(scene["background"]), "场景背景色"); err != nil {
		return err
	}
	if err := p.canvasPrevisFiniteInRange(p.numberValue(scene["environmentIntensity"], math.NaN()), "环境光强度", 0, 100); err != nil {
		return err
	}
	objects := p.creationMaps(scene["objects"])
	cameras := p.creationMaps(scene["cameras"])
	lights := p.creationMaps(scene["lights"])
	shots := p.creationMaps(scene["shots"])
	if len(objects) > canvasPrevisMaxObjects || len(cameras) > canvasPrevisMaxCameras || len(lights) > canvasPrevisMaxLights || len(shots) > canvasPrevisMaxShots {
		return BadAuthRequest("预演台场景对象、摄影机、灯光或镜头数量超过限制")
	}
	objectByID, err := p.canvasPrevisIDSet(objects, "导演对象 ID")
	if err != nil {
		return err
	}
	cameraByID, err := p.canvasPrevisIDSet(cameras, "导演摄影机 ID")
	if err != nil {
		return err
	}
	_, err = p.canvasPrevisIDSet(lights, "导演灯光 ID")
	if err != nil {
		return err
	}
	shotByID, err := p.canvasPrevisIDSet(shots, "导演镜头 ID")
	if err != nil {
		return err
	}
	if len(shots) == 0 || p.stringValue(scene["activeShotId"]) == "" || shotByID[p.stringValue(scene["activeShotId"])] == nil {
		return BadAuthRequest("预演台场景必须包含有效的 activeShotId")
	}
	for id, object := range objectByID {
		if err := p.canvasPrevisValidateText(p.stringValue(object["name"]), "导演对象名称", canvasPrevisTextLimit, true); err != nil {
			return err
		}
		if err := p.canvasPrevisValidateEnum(p.stringValue(object["kind"]), "导演对象类型", "primitive", "model", "actor", "billboard"); err != nil {
			return err
		}
		if primitive := p.stringValue(object["primitive"]); primitive != "" {
			if err := p.canvasPrevisValidateEnum(primitive, "导演对象 primitive", "box", "sphere", "cylinder", "plane", "character"); err != nil {
				return err
			}
		}
		objectColor := p.canvasPrevisNormalizeSceneColor(p.stringValue(object["color"]), "#8795a5")
		object["color"] = objectColor
		if err := p.canvasPrevisValidateColor(p.stringValue(object["color"]), "导演对象颜色"); err != nil {
			return err
		}
		if err := p.canvasPrevisValidateTransform(object["transform"], "导演对象 "+id); err != nil {
			return err
		}
		if binding, ok := object["characterBinding"].(map[string]any); ok {
			for _, key := range []string{"characterAssetId", "characterVersionId", "referenceNodeId"} {
				if err := validateCanvasID(p.stringValue(binding[key]), "角色绑定 "+key, 120); err != nil {
					return err
				}
			}
			if name := p.stringValue(binding["characterName"]); name != "" {
				if err := p.canvasPrevisValidateText(name, "角色绑定 characterName", canvasPrevisTextLimit, true); err != nil {
					return err
				}
			}
		}
		if keyframes, ok := object["keyframes"]; ok {
			for _, keyframe := range p.creationMaps(keyframes) {
				if err := validateCanvasID(p.stringValue(keyframe["id"]), "对象关键帧 ID", 120); err != nil {
					return err
				}
				if err := p.canvasPrevisFiniteInRange(p.numberValue(keyframe["time"], math.NaN()), "对象关键帧 time", 0, 60); err != nil {
					return err
				}
				if err := p.canvasPrevisValidateTransform(keyframe["transform"], "对象关键帧 "+p.stringValue(keyframe["id"])); err != nil {
					return err
				}
			}
		}
		if motionPath, ok := object["motionPath"].(map[string]any); ok {
			points, pointsOK := motionPath["points"].([]any)
			if !pointsOK || len(points) < 2 || len(points) > 64 {
				return BadAuthRequest("预演台 motionPath 点数无效")
			}
			for index, point := range points {
				value, ok := p.canvasPrevisNumberVec3(point)
				if !ok {
					return BadAuthRequest(fmt.Sprintf("预演台 motionPath 点 %d 无效", index))
				}
				if err := p.canvasPrevisValidateVec3(value, "motionPath point"); err != nil {
					return err
				}
			}
			if err := p.canvasPrevisValidateEnum(p.stringValue(motionPath["speed"]), "motionPath speed", "slow", "walk", "run"); err != nil {
				return err
			}
			if err := p.canvasPrevisFiniteInRange(p.numberValue(motionPath["startDelay"], math.NaN()), "motionPath startDelay", 0, 60); err != nil {
				return err
			}
		}
		if parentID := p.stringValue(object["parentId"]); parentID != "" {
			if err := validateCanvasID(parentID, "导演对象 parentId", 80); err != nil {
				return err
			}
			if objectByID[parentID] == nil || parentID == id {
				return BadAuthRequest("预演台对象 parentId 无效")
			}
		}
	}
	for id, camera := range cameraByID {
		if err := p.canvasPrevisValidateText(p.stringValue(camera["name"]), "导演摄影机名称", canvasPrevisTextLimit, true); err != nil {
			return err
		}
		if err := p.canvasPrevisFiniteInRange(p.numberValue(camera["focalLength"], math.NaN()), "摄影机焦段", 1, 300); err != nil {
			return err
		}
		target, targetOK := p.canvasPrevisNumberVec3(camera["target"])
		if !targetOK {
			return BadAuthRequest("导演摄影机 " + id + " target 必须包含3个数字")
		}
		if err := p.canvasPrevisValidateVec3(target, "导演摄影机 "+id+" target"); err != nil {
			return err
		}
		if err := p.canvasPrevisValidateTransform(camera["transform"], "导演摄影机 "+id); err != nil {
			return err
		}
		if keyframes, ok := camera["keyframes"]; ok {
			for _, keyframe := range p.creationMaps(keyframes) {
				if err := validateCanvasID(p.stringValue(keyframe["id"]), "摄影机关键帧 ID", 120); err != nil {
					return err
				}
				if err := p.canvasPrevisFiniteInRange(p.numberValue(keyframe["time"], math.NaN()), "摄影机关键帧 time", 0, 60); err != nil {
					return err
				}
				if err := p.canvasPrevisValidateTransform(keyframe["transform"], "摄影机关键帧 "+p.stringValue(keyframe["id"])); err != nil {
					return err
				}
			}
		}
	}
	for id, shot := range shotByID {
		cameraID := p.stringValue(shot["cameraId"])
		if cameraByID[cameraID] == nil {
			return BadAuthRequest("导演镜头 " + id + " 引用了不存在的摄影机")
		}
		duration := p.numberValue(shot["duration"], math.NaN())
		fps := p.numberValue(shot["fps"], math.NaN())
		if err := p.canvasPrevisFiniteInRange(duration, "导演镜头时长", 0.1, 60); err != nil {
			return err
		}
		if err := p.canvasPrevisFiniteInRange(fps, "导演镜头帧率", 1, 60); err != nil {
			return err
		}
		if err := p.canvasPrevisValidateText(p.stringValue(shot["name"]), "导演镜头名称", canvasPrevisTextLimit, true); err != nil {
			return err
		}
		if err := p.canvasPrevisValidateEnum(p.stringValue(shot["shotSize"]), "导演景别", "extreme_wide", "wide", "full", "medium", "close_up", "extreme_close_up"); err != nil {
			return err
		}
		if err := p.canvasPrevisValidateEnum(p.stringValue(shot["cameraMove"]), "导演运镜", "static", "push_in", "pull_out", "pan_left", "pan_right", "tilt_up", "tilt_down", "orbit_left", "orbit_right", "handheld"); err != nil {
			return err
		}
		if err := p.canvasPrevisValidateText(p.stringValue(shot["prompt"]), "导演镜头提示词", 2000, false); err != nil {
			return err
		}
	}
	for _, light := range p.creationMaps(scene["lights"]) {
		lightID := p.stringValue(light["id"])
		if err := p.canvasPrevisValidateText(p.stringValue(light["name"]), "导演灯光名称", canvasPrevisTextLimit, true); err != nil {
			return err
		}
		if err := p.canvasPrevisValidateEnum(p.stringValue(light["type"]), "导演灯光类型", "directional", "point", "spot", "ambient"); err != nil {
			return err
		}
		lightColor := p.canvasPrevisNormalizeSceneColor(p.stringValue(light["color"]), "#ffffff")
		light["color"] = lightColor
		if err := p.canvasPrevisValidateColor(p.stringValue(light["color"]), "导演灯光颜色"); err != nil {
			return err
		}
		if err := p.canvasPrevisFiniteInRange(p.numberValue(light["intensity"], math.NaN()), "导演灯光强度", 0, 100); err != nil {
			return err
		}
		if angle, ok := light["angle"]; ok {
			if err := p.canvasPrevisFiniteInRange(p.numberValue(angle, math.NaN()), "导演灯光 "+lightID+" angle", 0, 6.29); err != nil {
				return err
			}
		}
		if penumbra, ok := light["penumbra"]; ok {
			if err := p.canvasPrevisFiniteInRange(p.numberValue(penumbra, math.NaN()), "导演灯光 "+lightID+" penumbra", 0, 1); err != nil {
				return err
			}
		}
		if err := p.canvasPrevisValidateTransform(light["transform"], "导演灯光 "+lightID); err != nil {
			return err
		}
	}

	for id := range objectByID {
		seen := map[string]bool{}
		for current := id; current != ""; {
			if seen[current] {
				return BadAuthRequest("预演台对象 parentId 不能形成环")
			}
			seen[current] = true
			current = p.stringValue(objectByID[current]["parentId"])
			if current != "" && objectByID[current] == nil {
				return BadAuthRequest("预演台对象 parentId 引用不存在对象")
			}
		}
	}
	return nil
}

func (p *planner) canvasPrevisValidateTemplateID(value string) error {
	valid := map[string]bool{"empty": true, "monologue": true, "dialogue": true, "blocking": true, "product": true, "action_chase": true, "interior": true, "crowd": true}
	if !valid[value] {
		return BadAuthRequest("templateId 不受支持；有效值：empty/monologue/dialogue/blocking/product/action_chase/interior/crowd")
	}
	return nil
}

func (p *planner) canvasPrevisValidateText(value, label string, maxRunes int, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return BadAuthRequest(label + "不能为空")
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return BadAuthRequest(label + "不能包含首尾空白或无效字符")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s不能超过 %d 个字符", label, maxRunes)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return BadAuthRequest(label + "不能包含控制字符")
		}
	}
	return nil
}

func (p *planner) canvasPrevisValidateTransform(value any, label string) error {
	transform, ok := value.(map[string]any)
	if !ok {
		return BadAuthRequest(label + "缺少 transform")
	}
	for _, key := range []string{"position", "rotation", "scale"} {
		items, ok := transform[key].([]any)
		if !ok || len(items) != 3 {
			return BadAuthRequest(label + "的 transform." + key + " 必须包含3个数字")
		}
		for _, item := range items {
			if !p.canvasPrevisFinite(item) {
				return BadAuthRequest(label + "的 transform 包含无效数字")
			}
		}
	}
	return nil
}

func (p *planner) canvasPrevisValidateVec3(value []float64, label string) error {
	if len(value) != 3 {
		return BadAuthRequest(label + "必须是包含3个数字的数组")
	}
	for _, number := range value {
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Abs(number) > 100000 {
			return BadAuthRequest(label + "包含无效或超出范围的数字")
		}
	}
	return nil
}

func (p *planner) canvasPrevisVec3(value []float64) []any {
	return []any{value[0], value[1], value[2]}
}

func (p *planner) canvasRemapCopiedReferenceID(object map[string]any, key string, idMap map[string]string) {
	source := p.stringValue(object[key])
	if copied := idMap[source]; source != "" && copied != "" {
		object[key] = copied
	}
}

func (p *planner) canvasRemapCopiedReferenceIDs(object map[string]any, key string, idMap map[string]string) {
	if _, exists := object[key]; !exists {
		return
	}
	next := []any{}
	seen := map[string]bool{}
	for _, source := range p.canvasStrings(object[key]) {
		copied := idMap[source]
		if copied == "" {
			copied = source
		}
		if !seen[copied] {
			next = append(next, copied)
			seen[copied] = true
		}
	}
	object[key] = next
}

func (p *planner) canvasRemapCopiedBindings(value any, idMap map[string]string) {
	for _, binding := range p.creationMaps(value) {
		p.canvasRemapCopiedReferenceID(binding, "nodeId", idMap)
	}
}

func (p *planner) canvasRemapCopiedStoryboardRowReferences(row map[string]any, idMap map[string]string, remapOutputs bool) {
	for _, character := range p.creationMaps(row["characters"]) {
		p.canvasRemapCopiedReferenceID(character, "characterImageNodeId", idMap)
	}
	p.canvasRemapCopiedBindings(row["assetBindings"], idMap)
	if remapOutputs {
		p.canvasRemapCopiedReferenceID(row, "imageNodeId", idMap)
		p.canvasRemapCopiedReferenceID(row, "videoNodeId", idMap)
	}
}

// Add-copy accepts the exact metadata produced by isolateCopiedNodeMetadata.
// Only declared canvas references are remapped; opaque plugin and extension data
// must remain byte-for-byte equivalent to the editor preview.
func (p *planner) canvasRemapCopiedAddReferences(metadata map[string]any, idMap map[string]string) {
	for _, key := range []string{"videoStartFrameNodeId", "videoEndFrameNodeId", "videoFrameSourceNodeId", "batchSourceNodeId"} {
		p.canvasRemapCopiedReferenceID(metadata, key, idMap)
	}
	p.canvasRemapCopiedReferenceIDs(metadata, "batchInputNodeIds", idMap)
	p.canvasRemapCopiedBindings(metadata["assetBindings"], idMap)
	if emotion, ok := metadata["emotionEdit"].(map[string]any); ok {
		p.canvasRemapCopiedReferenceID(emotion, "sourceNodeId", idMap)
	}
	if spec, ok := metadata["generationSpec"].(map[string]any); ok {
		p.canvasRemapCopiedBindings(spec["referenceBindings"], idMap)
	}
	p.canvasRemapCopiedBindings(metadata["inputBindings"], idMap)
	if board, ok := metadata["storyboard"].(map[string]any); ok {
		p.canvasRemapCopiedReferenceIDs(board, "referenceNodeIds", idMap)
		for _, row := range p.creationMaps(board["rows"]) {
			p.canvasRemapCopiedStoryboardRowReferences(row, idMap, false)
		}
	}
	if table, ok := metadata["batchTable"].(map[string]any); ok {
		p.canvasRemapCopiedReferenceIDs(table, "storyboardSourceNodeIds", idMap)
		for _, row := range p.creationMaps(table["rows"]) {
			p.canvasRemapCopiedReferenceIDs(row, "inputNodeIds", idMap)
			p.canvasRemapCopiedReferenceIDs(row, "textNodeIds", idMap)
		}
		for _, row := range p.creationMaps(table["storyboardRows"]) {
			p.canvasRemapCopiedStoryboardRowReferences(row, idMap, true)
		}
	}
}

func (p *planner) canvasRemapCopiedReferences(object map[string]any, idMap map[string]string) {
	for key, value := range object {
		switch typed := value.(type) {
		case map[string]any:
			p.canvasRemapCopiedReferences(typed, idMap)
		case []any:
			for i, entry := range typed {
				if nested, ok := entry.(map[string]any); ok {
					p.canvasRemapCopiedReferences(nested, idMap)
				} else if text, ok := entry.(string); ok && (strings.HasSuffix(key, "NodeIds") || key == "inputNodeIds") && idMap[text] != "" {
					typed[i] = idMap[text]
				}
			}
		case string:
			if (strings.HasSuffix(key, "NodeId") || key == "nodeId") && idMap[typed] != "" {
				object[key] = idMap[typed]
			}
		}
	}
}

func (p *planner) canvasRemoveEdgeReference(nodes []map[string]any, removed map[string]any, edges []map[string]any) {
	from, to := p.stringValue(removed["fromNodeId"]), p.stringValue(removed["toNodeId"])
	for _, edge := range edges {
		if p.stringValue(edge["fromNodeId"]) == from && p.stringValue(edge["toNodeId"]) == to {
			return
		}
	}
	index := p.canvasNodeIndex(nodes, to)
	if index < 0 {
		return
	}
	meta := p.canvasNodeMetadata(nodes[index])
	refs := p.canvasStrings(meta["referenceNodeIds"])
	next := []any{}
	for _, id := range refs {
		if id != from {
			next = append(next, id)
		}
	}
	if _, exists := meta["referenceNodeIds"]; exists {
		meta["referenceNodeIds"] = next
	}
}

func (p *planner) canvasReorderItems(items []map[string]any, ids []string) ([]map[string]any, error) {
	if len(ids) != len(items) {
		return nil, BadAuthRequest("排序必须包含当前列表的全部 ID")
	}
	byID := map[string]map[string]any{}
	for _, item := range items {
		byID[p.stringValue(item["id"])] = item
	}
	result := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		item := byID[id]
		if item == nil {
			return nil, BadAuthRequest("排序含有不存在或重复的 ID")
		}
		result = append(result, item)
		delete(byID, id)
	}
	return result, nil
}

// canvasResetCopiedMetadata clears generation state a node copy must not inherit.
// nodeID is the copied node's id, or "" when the caller only swaps a media asset.
// It returns the source-to-copy identity mapping of the copied table rows so that
// row scoped connections can be rebound onto the copy instead of the source table.
func (p *planner) canvasResetCopiedMetadata(meta map[string]any, nodeID string, idMap map[string]string) map[string]string {
	delete(meta, "mediaVersion")
	for _, key := range []string{"taskId", "taskClientOperationId", "retryOf", "attemptGroupId", "taskStatus", "taskProgress", "taskStage", "taskMediaStage", "taskCanRecoverMedia", "taskProvider", "taskStartedAt", "taskCompletedAt", "taskDurationMs", "taskErrorCode", "taskOfficialStatus", "taskReceiptRecorded", "taskCreatedAt", "taskUpdatedAt", "generationBatches", "batchRootId", "batchChildIds", "batchFailedCount", "isBatchRoot", "primaryImageId", "imageBatchExpanded", "batchUsesReferenceImages", "versionOfNodeId", "versionLabel", "versionPrimary", "errorDetails", "generationErrorCode", "resourceReloadAvailable", "failedPromptFingerprint"} {
		delete(meta, key)
	}
	for key := range meta {
		if strings.HasPrefix(key, "generationApplied") || strings.HasPrefix(key, "generationPersistence") {
			delete(meta, key)
		}
	}
	meta["status"] = "idle"
	if p.stringValue(meta["content"]) != "" {
		meta["status"] = "success"
	}
	for _, key := range []string{"referenceSetId", "previsPreviewNodeId", "previsDepthNodeId", "previsNormalNodeId", "previsClayVideoNodeId"} {
		if source := p.stringValue(meta[key]); source != "" {
			if idMap[source] == "" {
				delete(meta, key)
			} else {
				meta[key] = idMap[source]
			}
		}
	}
	if refs, ok := meta["referenceAssetNodeIds"]; ok {
		next := []any{}
		for _, id := range p.canvasStrings(refs) {
			if idMap[id] != "" {
				next = append(next, idMap[id])
			}
		}
		meta["referenceAssetNodeIds"] = next
	}
	if views, ok := meta["characterViewNodeIds"].(map[string]any); ok {
		for key, value := range views {
			if idMap[p.stringValue(value)] == "" {
				delete(views, key)
			} else {
				views[key] = idMap[p.stringValue(value)]
			}
		}
		if len(views) == 0 {
			delete(meta, "characterViewNodeIds")
		}
	}
	var rowIDs map[string]string
	if board, ok := meta["storyboard"].(map[string]any); ok {
		rowIDs = map[string]string{}
		for index, row := range p.creationMaps(board["rows"]) {
			owned := false
			for _, key := range []string{"imageNodeId", "videoNodeId"} {
				source := p.stringValue(row[key])
				if idMap[source] != "" {
					row[key] = idMap[source]
					owned = true
				} else {
					delete(row, key)
				}
			}
			p.canvasIsolateCopiedRow(row, nodeID, "row", index, rowIDs, owned)
		}
	}
	if table, ok := meta["batchTable"].(map[string]any); ok {
		if rowIDs == nil {
			rowIDs = map[string]string{}
		}
		for index, row := range p.creationMaps(table["rows"]) {
			owned := false
			if source := p.stringValue(row["outputNodeId"]); idMap[source] == "" {
				delete(row, "outputNodeId")
			} else {
				row["outputNodeId"] = idMap[source]
				owned = true
			}
			p.canvasIsolateCopiedRow(row, nodeID, "batch-row", index, rowIDs, owned)
		}
	}
	return rowIDs
}

// canvasIsolateCopiedRow makes a copied table row a new row. Task identity always goes,
// because a copy is never the task that produced the source result; an in-flight source
// status also resets so the copy cannot show a running task it does not own.
func (p *planner) canvasIsolateCopiedRow(row map[string]any, nodeID, suffix string, index int, rowIDs map[string]string, owned bool) {
	for _, key := range []string{"taskId", "taskClientOperationId", "taskStatus", "taskProgress", "taskStage", "errorDetails"} {
		delete(row, key)
	}
	if !owned || canvasRowTaskActive(p.stringValue(row["status"])) {
		row["status"] = "idle"
	}
	if nodeID == "" {
		return
	}
	source := p.stringValue(row["id"])
	next := fmt.Sprintf("%s-%s-%d", nodeID, suffix, index+1)
	if source != "" {
		rowIDs[source] = next
	}
	row["id"] = next
}

func canvasRowTaskActive(status string) bool {
	switch status {
	case "loading", "running", "pending", "queued":
		return true
	}
	return false
}

func (p *planner) canvasSafeNumber(value any) (any, bool) {
	switch number := value.(type) {
	case float64, float32, int, int64:
		return number, true
	default:
		return nil, false
	}
}

func (p *planner) canvasStoryboardRowDefaults() map[string]any {
	row := map[string]any{
		"characters":      []any{},
		"mustHave":        []any{},
		"optionalDetails": []any{},
		"assetBindings":   []any{},
		"status":          "idle",
	}
	for _, field := range canvasStoryboardTextFields {
		row[field] = ""
	}
	return row
}

var canvasStoryboardTextFields = []string{
	"plotDescription", "dialogue", "videoMotionPrompt", "imageGenerationPrompt", "camera", "motion", "shotSize",
	"emotion", "lightingAndAtmosphere", "audioEffects", "narrativeIntent", "viewerPOV", "performanceBlocking",
	"timeBeats", "continuityOut", "negativePrompt",
}

func (p *planner) canvasStrings(value any) []string {
	result := []string{}
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		for _, v := range values {
			if s, ok := v.(string); ok {
				result = append(result, s)
			}
		}
	}
	return result
}

var canvasTLEnums = map[string]string{
	"color": "black grey light-violet violet blue light-blue yellow orange green light-green light-red red white",
	"dash":  "draw solid dashed dotted none", "fill": "none semi solid pattern fill lined-fill",
	"size": "s m l xl", "font": "draw sans serif mono",
	"align": "start middle end start-legacy end-legacy middle-legacy", "vertical": "start middle end", "textalign": "start middle end",
	"geo":    "cloud rectangle ellipse triangle diamond pentagon hexagon octagon star rhombus rhombus-2 oval trapezoid arrow-right arrow-left arrow-up arrow-down x-box check-box heart",
	"spline": "line cubic", "arrowkind": "arc elbow", "arrowhead": "arrow triangle square dot pipe diamond inverted bar none",
	"terminal": "start end", "snap": "center edge-point edge none", "segmenttype": "free straight",
}

func (p *planner) canvasTLObject(object map[string]any, specs string) bool {
	allowed := map[string]bool{}
	for _, field := range strings.Fields(specs) {
		pair := strings.SplitN(field, ":", 2)
		key := strings.TrimSuffix(pair[0], "?")
		allowed[key] = true
		value, exists := object[key]
		if !exists && strings.HasSuffix(pair[0], "?") {
			continue
		}
		if !exists || !p.canvasTLValue(value, pair[1]) {
			return false
		}
	}
	for key := range object {
		if !allowed[key] {
			return false
		}
	}
	return true
}

func (p *planner) canvasTLPath(segment map[string]any) bool {
	path := p.stringValue(segment["path"])
	if path == "" {
		return true
	}
	dim := 3
	if segment["dim"] == float64(2) {
		dim = 2
	}
	data, err := base64.StdEncoding.Strict().DecodeString(path)
	if err != nil || len(data) < dim*4 || (len(data)-dim*4)%(dim*2) != 0 {
		return false
	}
	for offset := 0; offset < dim*4; offset += 4 {
		n := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4])))
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
	}
	for offset := dim * 4; offset < len(data); offset += 2 {
		if binary.LittleEndian.Uint16(data[offset:offset+2])&0x7c00 == 0x7c00 {
			return false
		}
	}
	return true
}

var canvasTLShapeProps = map[string]string{
	"group":     "",
	"geo":       "geo:geo dash:dash url:link w:positive h:positive growY:nonnegative scale:positive labelColor:color color:color fill:fill size:size font:font align:align verticalAlign:vertical richText:richtext",
	"text":      "color:color size:size font:font textAlign:textalign w:positive richText:richtext scale:positive autoSize:bool",
	"note":      "color:color labelColor:color size:size font:font fontSizeAdjustment:nonnegative-null align:align verticalAlign:vertical growY:nonnegative url:link richText:richtext scale:positive textLastEditedBy:string-null",
	"frame":     "w:positive h:positive name:string color:color",
	"embed":     "w:positive h:positive url:string",
	"bookmark":  "w:positive h:positive assetId:asset-null url:link",
	"image":     "w:positive h:positive playing:bool url:link assetId:asset-null crop:crop-null flipX:bool flipY:bool altText:string",
	"video":     "w:positive h:positive time:number playing:bool autoplay:bool url:link assetId:asset-null altText:string",
	"draw":      "color:color fill:fill dash:dash size:size segments:segments isComplete:bool isClosed:bool isPen:bool scale:positive scaleX:nonzero scaleY:nonzero",
	"highlight": "color:color size:size segments:segments isComplete:bool isPen:bool scale:positive scaleX:nonzero scaleY:nonzero",
	"line":      "color:color dash:dash size:size spline:spline points:points scale:positive",
	"arrow":     "kind:arrowkind labelColor:color color:color fill:fill dash:dash size:size arrowheadStart:arrowhead arrowheadEnd:arrowhead font:font start:vec end:vec bend:number richText:richtext labelPosition:number scale:positive elbowMidPoint:number",
}

func (p *planner) canvasTLValue(value any, rule string) bool {
	if strings.HasSuffix(rule, "-null") {
		if value == nil {
			return true
		}
		rule = strings.TrimSuffix(rule, "-null")
	}
	if values, ok := canvasTLEnums[rule]; ok {
		text, valid := value.(string)
		if !valid {
			return false
		}
		for _, candidate := range strings.Fields(values) {
			if candidate == text {
				return true
			}
		}
		return false
	}
	switch rule {
	case "string":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "object", "jsonobject":
		_, ok := value.(map[string]any)
		return ok
	case "number", "positive", "nonnegative", "nonzero", "opacity":
		n, ok := value.(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
		switch rule {
		case "positive":
			return n > 0
		case "nonnegative":
			return n >= 0
		case "nonzero":
			return n != 0
		case "opacity":
			return n >= 0 && n <= 1
		}
		return true
	case "index":
		text, ok := value.(string)
		if !ok || len(text) < 2 {
			return false
		}
		integerLength := 0
		if text[0] >= 'a' && text[0] <= 'z' {
			integerLength = int(text[0]-'a') + 2
		}
		if text[0] >= 'A' && text[0] <= 'Z' {
			integerLength = int('Z'-text[0]) + 2
		}
		return integerLength > 0 && len(text) >= integerLength && text != "A"+strings.Repeat("0", 26) && !(len(text) > integerLength && strings.HasSuffix(text, "0"))
	case "asset":
		text, ok := value.(string)
		return ok && strings.HasPrefix(text, "asset:") && len(text) > 6
	case "resource":
		text, ok := value.(string)
		return ok && strings.HasPrefix(text, "resource:") && len(text) > 9
	case "link":
		text, ok := value.(string)
		if !ok {
			return false
		}
		if text == "" {
			return true
		}
		parsed, err := url.Parse(text)
		return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http" || parsed.Scheme == "mailto")
	case "vec":
		object, ok := value.(map[string]any)
		return ok && p.canvasTLObject(object, "x:number y:number z?:number")
	case "crop":
		object, ok := value.(map[string]any)
		return ok && p.canvasTLObject(object, "topLeft:vec bottomRight:vec isCircle?:bool")
	case "richtext":
		object, ok := value.(map[string]any)
		return ok && p.canvasTLObject(object, "type:string content:array attrs?:any")
	case "array":
		_, ok := value.([]any)
		return ok
	case "any":
		return true
	case "segments":
		segments, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range segments {
			segment, ok := item.(map[string]any)
			if !ok || !p.canvasTLObject(segment, "type:segmenttype path:string dim?:dimension") {
				return false
			}
			if !p.canvasTLPath(segment) {
				return false
			}
		}
		return true
	case "dimension":
		n, ok := value.(float64)
		return ok && (n == 2 || n == 3)
	case "points":
		points, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for _, item := range points {
			point, ok := item.(map[string]any)
			if !ok || !p.canvasTLObject(point, "id:string index:index x:number y:number") {
				return false
			}
		}
		return true
	}
	return false
}

func (p *planner) canvasValidateEdgeHandles(nodes []map[string]any, edge map[string]any) error {
	for _, end := range []string{"from", "to"} {
		handle := p.stringValue(edge[end+"HandleId"])
		if handle == "" {
			continue
		}
		index := p.canvasNodeIndex(nodes, p.stringValue(edge[end+"NodeId"]))
		if index < 0 {
			return BadAuthRequest("连线节点不存在")
		}
		if strings.HasPrefix(handle, "row:") {
			board, _ := p.canvasNodeMetadata(nodes[index])["storyboard"].(map[string]any)
			if board == nil || p.canvasNodeIndex(p.creationMaps(board["rows"]), strings.TrimPrefix(handle, "row:")) < 0 {
				return BadAuthRequest("连线分镜行端口不存在")
			}
		} else if strings.HasPrefix(handle, "batch-reference:") || strings.HasPrefix(handle, "batch-text:") {
			if end != "to" || p.stringValue(nodes[index]["type"]) != "batch-table" {
				return BadAuthRequest("批量表端口只用于目标输入")
			}
			table, _ := p.canvasNodeMetadata(nodes[index])["batchTable"].(map[string]any)
			prefix, key := "batch-reference:", "referenceColumns"
			if strings.HasPrefix(handle, "batch-text:") {
				prefix, key = "batch-text:", "textColumns"
			}
			if p.canvasNodeIndex(p.creationMaps(table[key]), strings.TrimPrefix(handle, prefix)) < 0 {
				return BadAuthRequest("批量表输入列端口不存在")
			}
		} else if handle == "storyboard:context" {
			if p.stringValue(nodes[index]["type"]) != "script" {
				return BadAuthRequest("上下文端口只用于分镜节点")
			}
		} else if handle != "source" && handle != "target" {
			return BadAuthRequest("连线端口不受支持")
		}
	}
	return nil
}

func (p *planner) creationAddedNode(op creationNodeSpec) map[string]any {
	capability, known := p.canvasNodeCapabilityForType(op.NodeType)
	width, height, title := 340.0, 240.0, "Note"
	if known {
		width, height, title = capability.DefaultWidth, capability.DefaultHeight, capability.Label
	}
	metadata := map[string]any{"content": "", "status": "idle"}
	if known && op.Metadata == nil {
		metadata = capability.Metadata("")
	}
	if op.Width != nil {
		width = *op.Width
	}
	if op.Height != nil {
		height = *op.Height
	}
	if op.Title != "" {
		title = op.Title
	}
	position := op.Position
	if position == nil {
		position = map[string]any{"x": float64(0), "y": float64(0)}
		if op.X != nil {
			position["x"] = *op.X
		}
		if op.Y != nil {
			position["y"] = *op.Y
		}
	}
	return map[string]any{"id": op.ID, "type": op.NodeType, "title": title, "position": position, "width": width, "height": height, "metadata": p.mergeCreationMaps(metadata, op.Metadata)}
}

func (p *planner) creationHash(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (p *planner) creationMaps(value any) []map[string]any {
	result := []map[string]any{}
	switch items := value.(type) {
	case []any:
		for _, v := range items {
			if m, ok := v.(map[string]any); ok {
				result = append(result, m)
			}
		}
	case []map[string]any:
		result = items
	}
	return result
}

func (p *planner) editCanvasDrawingDocument(doc map[string]any, args canvasDrawingArgs) error {
	if len(args.Operations) > 10000 {
		return BadAuthRequest("绘图记录修改最多包含 10000 项")
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	var next map[string]any
	if err = json.Unmarshal(raw, &next); err != nil {
		return err
	}
	nodes := p.creationMaps(next["nodes"])
	index := p.canvasNodeIndex(nodes, args.NodeID)
	if index < 0 || p.stringValue(nodes[index]["type"]) != "drawing" {
		return BadAuthRequest("目标不是当前画布绘图节点")
	}
	node := nodes[index]
	meta := p.canvasNodeMetadata(node)
	engine := p.stringValue(meta["drawingEngine"])
	if engine == "" {
		engine = "excalidraw"
	}
	wrapper, _ := meta["drawingDocument"].(map[string]any)
	if wrapper == nil {
		if p.stringValue(meta["drawingId"]) != "" {
			return BadAuthRequest("本地绘图尚未同步，请先打开并保存绘图")
		}
		if engine != "excalidraw" {
			return BadAuthRequest("请先在 tldraw 编辑器同步原生空白文档")
		}
		wrapper = map[string]any{"version": 2, "engine": engine, "snapshot": map[string]any{"type": "excalidraw", "version": 2, "source": "canvas", "elements": []any{}, "appState": map[string]any{}, "files": map[string]any{}}, "revision": 0}
	}
	if args.Engine != engine || p.stringValue(wrapper["engine"]) != engine {
		return BadAuthRequest("绘图引擎与当前文档不一致")
	}
	records, err := p.canvasDrawingRecords(wrapper)
	if err != nil {
		return err
	}
	for _, op := range args.Operations {
		if err := validateCanvasID(op.ID, "绘图记录 ID", 160); err != nil {
			return err
		}
		switch op.Type {
		case "remove":
			if op.Record != nil {
				return BadAuthRequest("删除绘图记录不接受 record")
			}
			if records[op.ID] == nil {
				return BadAuthRequest("绘图记录不存在")
			}
			delete(records, op.ID)
		case "upsert":
			if op.Record == nil || p.stringValue(op.Record["id"]) != op.ID {
				return BadAuthRequest("绘图 record.id 必须与操作 ID 一致")
			}
			if err := p.validateCanvasDrawingValue(op.Record, 0); err != nil {
				return err
			}
			records[op.ID] = op.Record
		default:
			return BadAuthRequest("绘图仅支持 upsert/remove")
		}
	}
	if len(records) > 10000 {
		return BadAuthRequest("绘图记录数量超限")
	}
	snapshot := wrapper["snapshot"].(map[string]any)
	shapeCount, pageCount := 0, 1
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if engine == "excalidraw" {
		elements := []any{}
		files := map[string]any{}

		ordered := []string{}
		seen := map[string]bool{}
		for _, item := range p.creationMaps(snapshot["elements"]) {
			id := p.stringValue(item["id"])
			if records[id] != nil {
				ordered = append(ordered, id)
				seen[id] = true
			}
		}
		for _, id := range ids {
			if !seen[id] {
				ordered = append(ordered, id)
			}
		}
		for _, id := range ordered {
			item := records[id]
			kind := p.stringValue(item["type"])
			if kind == "" {
				if assets.ResourceID(p.stringValue(item["dataURL"])) == "" || !strings.HasPrefix(p.stringValue(item["mimeType"]), "image/") {
					return BadAuthRequest("绘图文件必须是已上传图片资源")
				}
				files[id] = item
				continue
			}
			switch kind {
			case "rectangle", "diamond", "ellipse", "arrow", "line", "freedraw", "text", "image", "frame", "magicframe", "embeddable", "iframe":
			default:
				return BadAuthRequest("不支持的 Excalidraw 元素类型")
			}
			if kind == "image" {
				fileID := p.stringValue(item["fileId"])
				file := records[fileID]
				if file == nil || assets.ResourceID(p.stringValue(file["dataURL"])) == "" {
					return BadAuthRequest("图片元素缺少平台文件")
				}
			}
			for _, field := range []string{"x", "y", "width", "height"} {
				if value, ok := item[field]; ok {
					n, valid := value.(float64)
					if !valid || math.Abs(n) > 1e6 || ((field == "width" || field == "height") && n < 0) {
						return BadAuthRequest("绘图位置或尺寸无效")
					}
				}
			}
			for _, field := range []string{"containerId", "frameId"} {
				if ref := p.stringValue(item[field]); ref != "" && records[ref] == nil {
					item[field] = nil
				}
			}
			for _, field := range []string{"startBinding", "endBinding"} {
				if binding, ok := item[field].(map[string]any); ok && records[p.stringValue(binding["elementId"])] == nil {
					item[field] = nil
				}
			}
			if bound, ok := item["boundElements"].([]any); ok {
				kept := []any{}
				for _, value := range bound {
					if b, ok := value.(map[string]any); ok && records[p.stringValue(b["id"])] != nil {
						kept = append(kept, b)
					}
				}
				item["boundElements"] = kept
			}
			elements = append(elements, item)
			if item["isDeleted"] != true {
				shapeCount++
			}
		}
		snapshot["elements"], snapshot["files"] = elements, files
	} else {
		if err := p.validateCanvasTldrawRecords(records); err != nil {
			return err
		}
		content := snapshot
		if nested, ok := snapshot["document"].(map[string]any); ok {
			content = nested
		}
		store := map[string]any{}
		pageCount = 0
		for _, id := range ids {
			item := records[id]
			kind := p.stringValue(item["typeName"])
			switch kind {
			case "shape":
				shapeCount++
				if !strings.HasPrefix(id, "shape:") {
					return BadAuthRequest("tldraw shape ID 无效")
				}
				parent := p.stringValue(item["parentId"])
				if parent == "" || records[parent] == nil {
					return BadAuthRequest("tldraw 形状缺少有效父页面或形状")
				}
			case "page":
				pageCount++
				if !strings.HasPrefix(id, "page:") {
					return BadAuthRequest("tldraw page ID 无效")
				}
			case "asset":
				if p.stringValue(item["type"]) != "image" {
					return BadAuthRequest("绘图同步当前仅支持图片素材，请移除视频或书签素材")
				}
				props, _ := item["props"].(map[string]any)
				if assets.ResourceID(p.stringValue(props["src"])) == "" {
					return BadAuthRequest("tldraw 媒体必须是平台资源")
				}
			case "binding":
				if records[p.stringValue(item["fromId"])] == nil || records[p.stringValue(item["toId"])] == nil {
					return BadAuthRequest("tldraw binding 缺少目标形状")
				}
			case "document":
			default:
				return BadAuthRequest("tldraw 仅接受文档、页面、形状、素材和绑定记录")
			}
			store[id] = item
		}
		if pageCount < 1 {
			return BadAuthRequest("tldraw 至少保留一个页面")
		}
		content["store"] = store
		delete(snapshot, "session")
	}
	if err := p.validateCanvasDrawingValue(snapshot, 0); err != nil {
		return err
	}
	revision := 1
	switch v := wrapper["revision"].(type) {
	case float64:
		revision = int(v) + 1
	case int:
		revision = v + 1
	}
	wrapper["revision"], wrapper["updatedAt"], wrapper["shapeCount"], wrapper["pageCount"] = revision, p.now().UTC().Format(time.RFC3339Nano), shapeCount, pageCount
	bytes, err := json.Marshal(wrapper)
	if err != nil {
		return err
	}
	if len(bytes) > 4<<20 {
		return BadAuthRequest("绘图原生文档超过 4 MiB")
	}
	meta["drawingDocument"], meta["drawingEngine"], meta["drawingRevision"], meta["drawingShapeCount"], meta["drawingPageCount"] = wrapper, engine, revision, shapeCount, pageCount
	meta["drawingUpdatedAt"] = wrapper["updatedAt"]
	if p.stringValue(meta["drawingId"]) == "" {
		meta["drawingId"] = args.NodeID + "-document"
	}
	delete(meta, "drawingPreviewUrl")
	delete(meta, "drawingPreviewStorageKey")
	delete(meta, "drawingPreviewRevision")
	next["nodes"] = p.mapsAsAny(nodes)
	for key := range doc {
		delete(doc, key)
	}
	for key, value := range next {
		doc[key] = value
	}
	return nil
}

func (p *planner) findLayoutNode(nodes []layout.Node, id string) (layout.Node, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node, true
		}
	}
	return layout.Node{}, false
}

func (p *planner) findPrevisSceneCamera(scene map[string]any, id string) (map[string]any, bool) {
	for _, camera := range p.creationMaps(scene["cameras"]) {
		if p.stringValue(camera["id"]) == id {
			return camera, true
		}
	}
	return nil, false
}

func (p *planner) findPrevisShot(scene map[string]any, id string) (map[string]any, bool) {
	for _, shot := range p.creationMaps(scene["shots"]) {
		if p.stringValue(shot["id"]) == id {
			return shot, true
		}
	}
	return nil, false
}

func (p *planner) keyframeIDOrExisting(requested string, existing map[string]any) string {
	if requested != "" {
		return requested
	}
	return p.stringValue(existing["id"])
}

func (p *planner) mapsAsAny(values []map[string]any) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

const maxCanvasBatchPromptRunes = 20000

const maxCanvasBatchReferences = 10

const maxCanvasBatchRows = 500

const maxCanvasStoryboardRows = 100

func (p *planner) mergeCreationMaps(left, right map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range left {
		out[k] = v
	}
	for k, v := range right {
		out[k] = v
	}
	return out
}

func (p *planner) numberValue(value any, fallback float64) float64 {
	if number, ok := p.canvasSafeNumber(value); ok {
		switch typed := number.(type) {
		case float64:
			return typed
		case int:
			return float64(typed)
		case int64:
			return float64(typed)
		}
	}
	return fallback
}

const previsDefaultActorURL = "https://cdn.jsdelivr.net/gh/mrdoob/three.js@r185/examples/models/gltf/Xbot.glb"

type previsRenderInput struct {
	CanvasID           string            `json:"canvasId"`
	SceneID            string            `json:"sceneId"`
	ShotID             string            `json:"shotId"`
	CallID             string            `json:"callId"`
	SourceHash         string            `json:"sourceHash"`
	Scene              map[string]any    `json:"scene"`
	Duration           float64           `json:"duration"`
	FPS                int               `json:"fps"`
	Width              int               `json:"width"`
	Height             int               `json:"height"`
	Assets             map[string]string `json:"assets"`
	RepairSourceTaskID string            `json:"repairSourceTaskId,omitempty"`
	RepairMode         string            `json:"repairMode,omitempty"`
}

func (p *planner) previsRenderSourceHash(scene map[string]any, shotID string) string {
	shot, ok := p.findPrevisShot(scene, shotID)
	if !ok {
		return ""
	}
	camera, ok := p.findPrevisSceneCamera(scene, p.stringValue(shot["cameraId"]))
	if !ok {
		return ""
	}
	selectFields := func(source map[string]any, fields ...string) map[string]any {
		out := map[string]any{}
		for _, field := range fields {
			if value, exists := source[field]; exists {
				out[field] = value
			}
		}
		return out
	}
	objects := []any{}
	for _, object := range p.creationMaps(scene["objects"]) {
		if !p.boolValue(object["visible"], true) {
			continue
		}
		projection := selectFields(object, "id", "kind", "primitive", "transform", "color", "visible", "castShadow", "receiveShadow", "pose", "archetype", "actorProfile", "rig", "motionClips", "activeMotionClipId", "boneOverrides", "boneTracks", "assetId", "storageKey", "url", "keyframes", "motionPath")
		projection["color"] = p.canvasPrevisNormalizeSceneColor(p.stringValue(object["color"]), "#8795a5")
		objects = append(objects, projection)
	}
	sceneProjection := selectFields(scene, "id", "version", "environmentIntensity", "environment")
	sceneProjection["background"] = p.canvasPrevisNormalizeSceneColor(p.stringValue(scene["background"]), "#111827")
	lights := []any{}
	for _, light := range p.creationMaps(scene["lights"]) {
		projection := selectFields(light, "id", "type", "transform", "color", "intensity", "angle", "penumbra", "castShadow")
		projection["color"] = p.canvasPrevisNormalizeSceneColor(p.stringValue(light["color"]), "#ffffff")
		lights = append(lights, projection)
	}
	sceneProjection["lights"] = lights
	return p.creationHash(map[string]any{
		"scene":   sceneProjection,
		"objects": objects,
		"camera":  selectFields(camera, "id", "transform", "target", "focalLength", "fov", "near", "far", "keyframes", "motionPath"),
		"shot":    selectFields(shot, "id", "cameraId", "duration", "fps", "aspectRatio", "cameraMove"),
	})
}

func (p *planner) storyboardNodeFromDocument(doc map[string]any, nodeID string) (map[string]any, map[string]any, []map[string]any, error) {
	for _, node := range p.creationMaps(doc["nodes"]) {
		if p.stringValue(node["id"]) != nodeID {
			continue
		}
		if p.stringValue(node["type"]) != "script" {
			return nil, nil, nil, BadAuthRequest("目标节点不是分镜脚本节点")
		}
		metadata, ok := node["metadata"].(map[string]any)
		if !ok {
			return nil, nil, nil, BadAuthRequest("分镜节点数据格式无效")
		}
		storyboard, ok := metadata["storyboard"].(map[string]any)
		if !ok {
			return nil, nil, nil, BadAuthRequest("分镜节点缺少结构化表格")
		}
		rows := p.creationMaps(storyboard["rows"])
		if len(rows) > maxCanvasStoryboardRows {
			return nil, nil, nil, BadAuthRequest("分镜脚本超过100个镜头限制")
		}
		ids := map[string]bool{}
		for _, row := range rows {
			rowID := p.stringValue(row["id"])
			if err := validateCanvasID(rowID, "分镜行ID", 120); err != nil || ids[rowID] {
				return nil, nil, nil, BadAuthRequest("分镜节点包含无效或重复的镜头行ID")
			}
			ids[rowID] = true
		}
		return node, storyboard, rows, nil
	}
	return nil, nil, nil, BadAuthRequest("未找到当前画布的分镜脚本节点")
}

func (p *planner) stringValue(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func (p *planner) truncateRunes(value string, limit int) string {
	return kernel.TruncateRunes(value, limit)
}

func (p *planner) validateCanvasBatchCells(table, cells map[string]any) error {
	allowed := map[string]bool{}
	for _, column := range p.creationMaps(table["textColumns"]) {
		allowed[p.stringValue(column["id"])] = true
	}
	for _, column := range p.creationMaps(table["referenceColumns"]) {
		if p.stringValue(column["type"]) == "text" {
			allowed[p.stringValue(column["id"])] = true
		}
	}
	for key := range cells {
		if !allowed[key] {
			return BadAuthRequest("文本单元格必须对应当前批量创作表的文本列")
		}
	}
	return nil
}

func (p *planner) validateCanvasBatchInputNodes(doc map[string]any, inputNodeIDs []string) error {
	nodes := map[string]map[string]any{}
	for _, node := range p.creationMaps(doc["nodes"]) {
		nodes[p.stringValue(node["id"])] = node
	}
	for _, id := range inputNodeIDs {
		if id == "" {
			continue
		}
		node := nodes[id]
		if node == nil || p.stringValue(node["type"]) != "image" {
			return BadAuthRequest("批量创作表只能引用当前画布内真实存在的图片节点")
		}
	}
	return nil
}

func (p *planner) validateCanvasBatchRowPatch(doc map[string]any, input map[string]any, maxInputs int, tables ...map[string]any) (map[string]any, []string, error) {
	patch := map[string]any{}
	labels := []string{}
	if len(input) > 0 {
		if err := capability.ValidateEditableFields(capability.BatchRowFields(), input); err != nil {
			return nil, nil, BadAuthRequest(err.Error())
		}
	}
	for key, value := range input {
		switch key {
		case "enabled":
			if _, ok := value.(bool); !ok {
				return nil, nil, BadAuthRequest("enabled 必须是布尔值")
			}
			patch[key], labels = value, append(labels, "启用状态")
		case "prompt":
			text, ok := value.(string)
			if !ok || utf8.RuneCountInString(text) > maxCanvasBatchPromptRunes {
				return nil, nil, BadAuthRequest("提示词必须是不超过20000字的文本")
			}
			patch[key], labels = text, append(labels, "提示词")
		case "inputNodeIds":
			ids, err := p.canvasBatchInputNodeIDs(value, maxInputs)
			if err != nil {
				return nil, nil, err
			}
			if err := p.validateCanvasBatchInputNodes(doc, ids); err != nil {
				return nil, nil, err
			}
			values := make([]any, len(ids))
			for index, id := range ids {
				values[index] = id
			}
			patch[key], labels = values, append(labels, "参考图片")
		case "textNodeIds":
			ids, err := p.canvasBatchTextNodeIDs(doc, value)
			if err != nil {
				return nil, nil, err
			}
			patch[key], labels = ids, append(labels, "参考文本")
		case "referenceOverrides":
			known := map[string]bool{}
			if len(tables) > 0 {
				for _, column := range p.creationMaps(tables[0]["referenceColumns"]) {
					known[p.stringValue(column["id"])] = true
				}
			}
			for _, id := range p.canvasStrings(value) {
				if !known[id] {
					return nil, nil, BadAuthRequest("单元格覆盖引用了不存在的参考图列")
				}
			}
			patch[key], labels = value, append(labels, "单元格覆盖")
		case "cells":
			if len(tables) > 0 {
				if err := p.validateCanvasBatchCells(tables[0], value.(map[string]any)); err != nil {
					return nil, nil, err
				}
			}
			patch[key], labels = value, append(labels, "文本单元格")
		default:
			return nil, nil, BadAuthRequest(fmt.Sprintf("不能通过批量创作表工具修改字段 %s", key))
		}
	}
	sort.Strings(labels)
	return patch, labels, nil
}

func (p *planner) validateCanvasCanvasConnection(nodes, edges []map[string]any, op Operation) error {
	candidate := connection.Edge{From: op.FromNodeID, To: op.ToNodeID, FromHandle: op.FromHandleID, ToHandle: op.ToHandleID}
	existing := make([]connection.Edge, 0, len(edges))
	for _, e := range edges {
		existing = append(existing, p.canvasConnectionEdge(e))
	}
	if err := connection.Validate(p.canvasConnectionNodes(nodes), existing, candidate); err != nil {
		return BadAuthRequest(err.Error())
	}
	for _, h := range []struct{ nodeID, handle string }{{op.FromNodeID, op.FromHandleID}, {op.ToNodeID, op.ToHandleID}} {
		if h.handle == "" {
			continue
		}
		if strings.HasPrefix(h.handle, "batch-reference:") {
			node := nodes[p.canvasNodeIndex(nodes, h.nodeID)]
			m, _ := node["metadata"].(map[string]any)
			table, _ := m["batchTable"].(map[string]any)
			columns := p.creationMaps(table["referenceColumns"])
			if len(columns) == 0 {
				columns = []map[string]any{{"id": "reference-1"}, {"id": "reference-2"}, {"id": "reference-3"}}
			}
			found := false
			for _, c := range columns {
				if h.handle == "batch-reference:"+p.stringValue(c["id"]) {
					found = true
				}
			}
			if !found {
				return BadAuthRequest("批量表参考列不存在")
			}
			continue
		}
		if len(h.handle) > 100 {
			return BadAuthRequest("分镜 handle 过长")
		}
		node := nodes[p.canvasNodeIndex(nodes, h.nodeID)]
		metadata, _ := node["metadata"].(map[string]any)
		if metadata["locked"] == true {
			return BadAuthRequest("不能修改锁定分镜节点")
		}
		if strings.HasPrefix(h.handle, "row:") && p.canvasConnectionRow(node, strings.TrimPrefix(h.handle, "row:")) == nil {
			return BadAuthRequest("分镜行不存在")
		}
	}
	if op.FromHandleID != "" && strings.HasPrefix(op.FromHandleID, "row:") {
		target := nodes[p.canvasNodeIndex(nodes, op.ToNodeID)]
		metadata, _ := target["metadata"].(map[string]any)
		if p.stringValue(target["type"]) == "video" && metadata["locked"] == true {
			return BadAuthRequest("不能修改锁定视频节点")
		}
	}
	return nil
}

func (p *planner) validateCanvasConnection(nodes []map[string]any, fromID, toID string, existingConnections ...[]map[string]any) error {
	if err := validateCanvasID(fromID, "来源节点 ID", 80); err != nil {
		return err
	}
	if err := validateCanvasID(toID, "目标节点 ID", 80); err != nil {
		return err
	}
	if fromID == toID {
		return BadAuthRequest("连线不能指向自身")
	}
	var from, to map[string]any
	for _, node := range nodes {
		if p.stringValue(node["id"]) == fromID {
			from = node
		}
		if p.stringValue(node["id"]) == toID {
			to = node
		}
	}
	if from == nil || to == nil {
		return BadAuthRequest("连线端点不存在")
	}
	fromCapability, fromKnown := p.canvasNodeCapabilityForNode(from)
	toCapability, toKnown := p.canvasNodeCapabilityForNode(to)
	if !fromKnown || !toKnown {
		return BadAuthRequest("连线包含当前 画布命令 不支持的节点类型")
	}
	fromKind := fromCapability.InputKind
	if !fromCapability.Connection.CanGraphSource {
		return BadAuthRequest(fmt.Sprintf("来源节点类型 %s 没有输出连接端口", fromCapability.Type))
	}
	if !toCapability.Connection.CanGraphTarget {
		return BadAuthRequest(fmt.Sprintf("目标节点类型 %s 没有输入连接端口", toCapability.Type))
	}
	connections := []map[string]any{}
	if len(existingConnections) > 0 {
		connections = existingConnections[0]
	}
	if err := p.canvasManualGraphInputError(nodes, from, to, fromKind, connections); err != nil {
		return err
	}
	if maxInputs := p.canvasGraphMaxInputs(toCapability.Type); maxInputs > 0 {
		inputIDs := map[string]bool{}
		for _, edge := range connections {
			if p.stringValue(edge["toNodeId"]) == toID {
				inputIDs[p.stringValue(edge["fromNodeId"])] = true
			}
		}
		inputIDs[fromID] = true
		if len(inputIDs) > maxInputs {
			return BadAuthRequest(fmt.Sprintf("%s最多连接 %d 个输入", toCapability.Label, maxInputs))
		}
	}
	return nil
}

func (p *planner) validateCanvasDrawingValue(value any, depth int) error {
	if depth > 32 {
		return BadAuthRequest("绘图记录嵌套过深")
	}
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "__proto__" || key == "constructor" || key == "prototype" {
				return BadAuthRequest("绘图记录不接受原型字段")
			}
			clean := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
			for _, part := range []string{"secret", "token", "credential", "apikey", "accesskey", "authorization", "cookie", "password", "authheaders"} {
				if strings.Contains(clean, part) {
					return BadAuthRequest("绘图记录不接受凭证字段")
				}
			}
			switch clean {
			case "__proto__", "constructor", "prototype", "apikey", "authorization", "cookie", "password", "secret", "token", "accesstoken", "refreshtoken", "headers":
				return BadAuthRequest("绘图记录不接受凭证或原型字段")
			}
			if err := p.validateCanvasDrawingValue(item, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := p.validateCanvasDrawingValue(item, depth+1); err != nil {
				return err
			}
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return BadAuthRequest("绘图数值无效")
		}
	case string:
		v = strings.TrimSpace(v)
		lower := strings.ToLower(v)
		if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") || strings.HasPrefix(lower, "javascript:") {
			return BadAuthRequest("绘图媒体必须引用已上传的平台资源")
		}
		if strings.HasPrefix(lower, "http:") || strings.HasPrefix(lower, "https:") {
			u, err := url.Parse(v)
			if err != nil || u.User != nil {
				return BadAuthRequest("绘图链接无效")
			}
			for key := range u.Query() {
				key = strings.ToLower(key)
				for _, part := range []string{"token", "signature", "credential", "secret", "password", "api_key", "apikey", "authorization"} {
					if strings.Contains(key, part) {
						return BadAuthRequest("绘图记录不接受授权链接")
					}
				}
				if key == "key" {
					return BadAuthRequest("绘图记录不接受授权链接")
				}
			}
		}
	}
	return nil
}

func (p *planner) validateCanvasGenerationReferences(doc map[string]any, nodeID string) error {
	err := references.ValidateGenerationReferences(doc, nodeID, p.registry)
	if typed, ok := err.(*references.Error); ok {
		return canvasFieldError(typed.Field, typed.Reason, typed.Message)
	}
	return err
}

func (p *planner) validateCanvasStoryboardReferences(doc, row map[string]any) error {
	nodes := map[string]map[string]any{}
	for _, node := range p.creationMaps(doc["nodes"]) {
		nodes[p.stringValue(node["id"])] = node
	}
	for _, character := range p.creationMaps(row["characters"]) {
		if strings.TrimSpace(p.stringValue(character["characterName"])) == "" {
			return BadAuthRequest("分镜角色名称不能为空")
		}
		if id := p.stringValue(character["characterImageNodeId"]); id != "" {
			node := nodes[id]
			if node == nil || (p.stringValue(node["type"]) != "image" && p.stringValue(node["type"]) != "drawing") {
				return BadAuthRequest("分镜角色图片必须引用当前画布内图片或绘图节点")
			}
		}
	}
	seen := map[string]bool{}
	for _, binding := range p.creationMaps(row["assetBindings"]) {
		id := p.stringValue(binding["nodeId"])
		node := nodes[id]
		if validateCanvasID(id, "资产节点ID", 80) != nil || node == nil || seen[id] {
			return BadAuthRequest("分镜资产引用必须是当前画布内唯一节点")
		}
		seen[id] = true
		kind := p.stringValue(node["type"])
		if kind == "drawing" {
			kind = "image"
		}
		if p.canvasCharacterNode(node) {
			kind = "character"
		}
		role := p.stringValue(binding["role"])
		allowed := false
		switch role {
		case "character":
			allowed = kind == "image" || kind == "character"
		case "environment", "wardrobe", "prop", "weapon", "style":
			allowed = kind == "image"
		case "motion":
			allowed = kind == "image" || kind == "video"
		case "audio":
			allowed = kind == "audio"
		}
		if !allowed {
			return BadAuthRequest("分镜资产类型不符合引用角色")
		}
	}
	return p.validateCanvasStoryboardSourceRange(row)
}

func (p *planner) validateCanvasStoryboardRow(row map[string]any, requireDescription bool) error {
	if row == nil {
		return BadAuthRequest("分镜行不能为空")
	}
	if duration, ok := row["durationSeconds"]; !ok {
		return BadAuthRequest("每个分镜行必须有 durationSeconds")
	} else if value, ok := duration.(float64); !ok || value <= 0 {
		return BadAuthRequest("分镜时长必须是大于零的数字")
	}
	if err := capability.ValidateEditableFields(capability.StoryboardRowFields(), row); err != nil {
		return BadAuthRequest(err.Error())
	}
	if requireDescription {
		plot := strings.TrimSpace(p.stringValue(row["plotDescription"]))
		motion := strings.TrimSpace(p.stringValue(row["videoMotionPrompt"]))
		if plot == "" && motion == "" {
			return BadAuthRequest("新增镜头需要画面描述或视频提示词")
		}
	}
	return nil
}

func (p *planner) validateCanvasStoryboardSourceRange(row map[string]any) error {
	start, hasStart := row["sourceStartMs"].(float64)
	end, hasEnd := row["sourceEndMs"].(float64)
	if hasStart && hasEnd && end < start {
		return BadAuthRequest("分镜来源结束时间不能早于开始时间")
	}
	if frame, exists := row["keyframeTimeMs"].(float64); exists && ((hasStart && frame < start) || (hasEnd && frame > end)) {
		return BadAuthRequest("分镜关键帧时间必须位于来源时间范围内")
	}
	return nil
}

func (p *planner) validateCanvasTldrawRecords(records map[string]map[string]any) error {
	pageCount, documentCount := 0, 0
	for id, record := range records {
		kind := p.stringValue(record["typeName"])
		if p.stringValue(record["id"]) != id || !strings.HasPrefix(id, kind+":") || len(id) <= len(kind)+1 {
			return BadAuthRequest("tldraw 记录 ID 与类型不一致")
		}
		if err := p.validateCanvasDrawingValue(record, 0); err != nil {
			return err
		}
		var fields string
		switch kind {
		case "document":
			documentCount++
			if id != "document:document" {
				return BadAuthRequest("tldraw 文档 ID 无效")
			}
			fields = "id:string typeName:string gridSize:number name:string meta:jsonobject"
		case "page":
			pageCount++
			fields = "id:string typeName:string name:string index:index meta:jsonobject"
		case "shape":
			fields = "id:string typeName:string type:string x:number y:number rotation:number index:index parentId:string isLocked:bool opacity:opacity props:object meta:jsonobject"
			specs, supported := canvasTLShapeProps[p.stringValue(record["type"])]
			if !supported {
				return BadAuthRequest("不支持的 tldraw 原生形状类型")
			}
			props, ok := record["props"].(map[string]any)
			if !ok || !p.canvasTLObject(props, specs) {
				return BadAuthRequest("tldraw 形状 props 不完整或无效")
			}
			parent := records[p.stringValue(record["parentId"])]
			parentType := p.stringValue(parent["typeName"])
			if parentType != "page" && parentType != "shape" {
				return BadAuthRequest("tldraw 形状缺少有效父页面或形状")
			}
			if assetID := p.stringValue(props["assetId"]); assetID != "" {
				asset := records[assetID]
				if p.stringValue(asset["typeName"]) != "asset" || p.stringValue(asset["type"]) != p.stringValue(record["type"]) {
					return BadAuthRequest("tldraw 形状素材不存在或类型不匹配")
				}
			}
		case "asset":
			fields = "id:string typeName:string type:string props:object meta:jsonobject"
			props, ok := record["props"].(map[string]any)
			specs := "w:number h:number name:string isAnimated:bool mimeType:string-null src:resource-null fileSize?:positive pixelRatio?:nonnegative"
			switch p.stringValue(record["type"]) {
			case "image":
			case "video":
				specs = "w:number h:number name:string isAnimated:bool mimeType:string-null src:resource-null fileSize?:number"
			case "bookmark":
				specs = "title:string description:string image:string favicon:string src:resource-null"
			default:
				return BadAuthRequest("不支持的 tldraw 原生素材类型")
			}
			if !ok || !p.canvasTLObject(props, specs) {
				return BadAuthRequest("tldraw 素材 props 不完整或无效")
			}
		case "binding":
			fields = "id:string typeName:string type:string fromId:string toId:string props:object meta:jsonobject"
			props, ok := record["props"].(map[string]any)
			if p.stringValue(record["type"]) != "arrow" || !ok || !p.canvasTLObject(props, "terminal:terminal normalizedAnchor:vec isExact:bool isPrecise:bool snap:snap") {
				return BadAuthRequest("tldraw 绑定类型或 props 无效")
			}
			source, target := records[p.stringValue(record["fromId"])], records[p.stringValue(record["toId"])]
			if p.stringValue(source["typeName"]) != "shape" || p.stringValue(source["type"]) != "arrow" || p.stringValue(target["typeName"]) != "shape" || source == nil || target == nil || source["id"] == target["id"] {
				return BadAuthRequest("tldraw 箭头绑定缺少有效来源或目标形状")
			}
		default:
			return BadAuthRequest("不支持的 tldraw 原生记录类型")
		}
		if !p.canvasTLObject(record, fields) {
			return BadAuthRequest("tldraw 原生记录字段不完整或无效")
		}
	}
	if pageCount < 1 || documentCount != 1 {
		return BadAuthRequest("tldraw 需要完整文档及至少一个页面")
	}

	visited := map[string]bool{}
	for id, record := range records {
		if p.stringValue(record["typeName"]) != "shape" || visited[id] {
			continue
		}
		path := map[string]bool{}
		current := id
		for p.stringValue(records[current]["typeName"]) == "shape" && !visited[current] {
			if path[current] {
				return BadAuthRequest("tldraw 形状父级形成循环")
			}
			path[current] = true
			current = p.stringValue(records[current]["parentId"])
		}
		for item := range path {
			visited[item] = true
		}
	}
	return nil
}
