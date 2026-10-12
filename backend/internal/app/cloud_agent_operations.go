package app

import (
	"fmt"
	"strings"
)

func cloudAgentPatchMovesNode(patch map[string]any) bool {
	for _, key := range []string{"x", "y", "width", "height"} {
		if _, ok := patch[key]; ok {
			return true
		}
	}
	return false
}

func applyCloudAgentExtendedOp(nodes, edges []map[string]any, op agentCanvasOp) ([]map[string]any, []map[string]any, cloudAgentApprovalPreviewItem, error) {
	item := cloudAgentApprovalPreviewItem{Operation: op.Type, NodeID: op.ID, NodeTitle: op.ID}
	fail := func(message string) ([]map[string]any, []map[string]any, cloudAgentApprovalPreviewItem, error) {
		return nodes, edges, item, BadAuthRequest(message)
	}
	index := cloudAgentNodeIndex(nodes, op.ID)
	if index >= 0 {
		item.NodeTitle = stringValue(nodes[index]["title"])
		item.NodeType = stringValue(nodes[index]["type"])
	}
	switch op.Type {
	case "delete_node":
		if index < 0 {
			return fail("删除节点不存在")
		}
		removed := map[string]bool{op.ID: true}
		for _, id := range cloudAgentStrings(cloudAgentNodeMetadata(nodes[index])["batchChildIds"]) {
			removed[id] = true
		}
		next := make([]map[string]any, 0, len(nodes))
		for _, node := range nodes {
			if !removed[stringValue(node["id"])] {
				cloudAgentCleanNodeReferences(node, removed)
				next = append(next, node)
			}
		}
		kept := make([]map[string]any, 0, len(edges))
		for _, edge := range edges {
			if !removed[stringValue(edge["fromNodeId"])] && !removed[stringValue(edge["toNodeId"])] {
				kept = append(kept, edge)
			}
		}
		item.Summary = fmt.Sprintf("删除《%s》及其关联引用，保留素材库原文件", item.NodeTitle)
		return next, kept, item, nil
	case "delete_connection":
		edgeIndex := cloudAgentNodeIndex(edges, op.ID)
		if edgeIndex < 0 {
			return fail("删除连线不存在")
		}
		item.NodeID = stringValue(edges[edgeIndex]["toNodeId"])
		item.TargetNodeID = stringValue(edges[edgeIndex]["fromNodeId"])
		removed := edges[edgeIndex]
		next := append(edges[:edgeIndex:edgeIndex], edges[edgeIndex+1:]...)
		cloudAgentRemoveEdgeReference(nodes, removed, next)
		item.Summary = "移除引用连线"
		return nodes, next, item, nil
	case "update_connection":
		edgeIndex := cloudAgentNodeIndex(edges, op.ID)
		if edgeIndex < 0 {
			return fail("修改连线不存在")
		}
		other := append([]map[string]any{}, edges[:edgeIndex]...)
		other = append(other, edges[edgeIndex+1:]...)
		if err := validateCloudAgentConnection(nodes, op.FromNodeID, op.ToNodeID, other); err != nil {
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
		if err := cloudAgentValidateEdgeHandles(nodes, edge); err != nil {
			return nodes, edges, item, err
		}
		for _, existing := range other {
			if stringValue(existing["fromNodeId"]) == op.FromNodeID && stringValue(existing["toNodeId"]) == op.ToNodeID && stringValue(existing["fromHandleId"]) == op.FromHandleID && stringValue(existing["toHandleId"]) == op.ToHandleID {
				return fail("连线重复")
			}
		}
		edges[edgeIndex] = edge
		cloudAgentRemoveEdgeReference(nodes, old, edges)
		if err := cloudAgentAttachStoryboardEdge(nodes, edge); err != nil {
			return nodes, edges, item, err
		}
		item.NodeID = op.FromNodeID
		item.TargetNodeID = op.ToNodeID
		item.Summary = "修改引用连线端点"
		return nodes, edges, item, nil
	case "duplicate_node":
		return cloudAgentCopyGraph(nodes, edges, op)
	case "set_parent":
		if index < 0 {
			return fail("分组节点不存在")
		}
		if op.ParentID == "" {
			delete(nodes[index], "parentId")
		} else {
			parent := cloudAgentNodeIndex(nodes, op.ParentID)
			if parent < 0 || stringValue(nodes[parent]["type"]) != "frame" {
				return fail("父节点必须是当前画布的背板或文件夹")
			}
			if stringValue(nodes[index]["type"]) == "frame" {
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
		descriptor, ok := cloudAgentNodeCapabilityForNode(nodes[index])
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
		metadata := cloudAgentNodeMetadata(nodes[index])
		text := stringValue(metadata[parts[1]])
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
		next, err := cloudAgentReorderItems(nodes, op.NodeIDs)
		if err != nil {
			return nodes, edges, item, err
		}
		item.Summary = "调整节点堆叠顺序"
		return next, edges, item, nil
	case "reorder_rows":
		if index < 0 {
			return fail("排序表格不存在")
		}
		meta := cloudAgentNodeMetadata(nodes[index])
		var table map[string]any
		switch stringValue(nodes[index]["type"]) {
		case "script":
			table, _ = meta["storyboard"].(map[string]any)
		case "batch-table":
			table, _ = meta["batchTable"].(map[string]any)
		}
		if table == nil {
			return fail("该节点没有可排序的结构化行")
		}
		rows := creationMaps(table["rows"])
		next, err := cloudAgentReorderItems(rows, op.RowIDs)
		if err != nil {
			return nodes, edges, item, err
		}
		table["rows"] = next
		item.Summary = "调整结构化行顺序，保留行 ID 和内容"
		return nodes, edges, item, nil
	}
	return fail("不支持的画布操作")
}

func cloudAgentNodeMetadata(node map[string]any) map[string]any {
	meta, _ := node["metadata"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
		node["metadata"] = meta
	}
	return meta
}
func cloudAgentStrings(value any) []string {
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
func cloudAgentCleanNodeReferences(node map[string]any, removed map[string]bool) {
	if removed[stringValue(node["parentId"])] {
		delete(node, "parentId")
	}
	meta := cloudAgentNodeMetadata(node)
	cloudAgentCleanReferenceObject(meta, removed)
}
func cloudAgentCleanReferenceObject(object map[string]any, removed map[string]bool) {
	for key, value := range object {
		switch typed := value.(type) {
		case map[string]any:
			cloudAgentCleanReferenceObject(typed, removed)
		case []any:
			values := make([]any, 0, len(typed))
			for _, entry := range typed {
				if s, ok := entry.(string); ok && (strings.HasSuffix(key, "NodeIds") || key == "batchChildIds") && removed[s] {
					continue
				}
				if entryMap, ok := entry.(map[string]any); ok {
					if (key == "assetBindings" || key == "referenceBindings") && removed[stringValue(entryMap["nodeId"])] {
						continue
					}
					cloudAgentCleanReferenceObject(entryMap, removed)
				}
				values = append(values, entry)
			}
			object[key] = values
		case string:
			if (strings.HasSuffix(key, "NodeId") || key == "primaryImageId" || key == "batchRootId") && removed[typed] {
				delete(object, key)
			}
		}
	}
}
func cloudAgentReorderItems(items []map[string]any, ids []string) ([]map[string]any, error) {
	if len(ids) != len(items) {
		return nil, BadAuthRequest("排序必须包含当前列表的全部 ID")
	}
	byID := map[string]map[string]any{}
	for _, item := range items {
		byID[stringValue(item["id"])] = item
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
func cloudAgentRemoveEdgeReference(nodes []map[string]any, removed map[string]any, edges []map[string]any) {
	from, to := stringValue(removed["fromNodeId"]), stringValue(removed["toNodeId"])
	for _, edge := range edges {
		if stringValue(edge["fromNodeId"]) == from && stringValue(edge["toNodeId"]) == to {
			return
		}
	}
	index := cloudAgentNodeIndex(nodes, to)
	if index < 0 {
		return
	}
	meta := cloudAgentNodeMetadata(nodes[index])
	refs := cloudAgentStrings(meta["referenceNodeIds"])
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
func cloudAgentValidateEdgeHandles(nodes []map[string]any, edge map[string]any) error {
	for _, end := range []string{"from", "to"} {
		handle := stringValue(edge[end+"HandleId"])
		if handle == "" {
			continue
		}
		index := cloudAgentNodeIndex(nodes, stringValue(edge[end+"NodeId"]))
		if index < 0 {
			return BadAuthRequest("连线节点不存在")
		}
		if strings.HasPrefix(handle, "row:") {
			board, _ := cloudAgentNodeMetadata(nodes[index])["storyboard"].(map[string]any)
			if board == nil || cloudAgentNodeIndex(creationMaps(board["rows"]), strings.TrimPrefix(handle, "row:")) < 0 {
				return BadAuthRequest("连线分镜行端口不存在")
			}
		} else if strings.HasPrefix(handle, "batch-reference:") || strings.HasPrefix(handle, "batch-text:") {
			if end != "to" || stringValue(nodes[index]["type"]) != "batch-table" {
				return BadAuthRequest("批量表端口只用于目标输入")
			}
			table, _ := cloudAgentNodeMetadata(nodes[index])["batchTable"].(map[string]any)
			prefix, key := "batch-reference:", "referenceColumns"
			if strings.HasPrefix(handle, "batch-text:") {
				prefix, key = "batch-text:", "textColumns"
			}
			if cloudAgentNodeIndex(creationMaps(table[key]), strings.TrimPrefix(handle, prefix)) < 0 {
				return BadAuthRequest("批量表输入列端口不存在")
			}
		} else if handle == "storyboard:context" {
			if stringValue(nodes[index]["type"]) != "script" {
				return BadAuthRequest("上下文端口只用于分镜节点")
			}
		} else if handle != "source" && handle != "target" {
			return BadAuthRequest("连线端口不受支持")
		}
	}
	return nil
}
