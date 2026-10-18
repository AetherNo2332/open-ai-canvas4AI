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
					// Every declared binding array is dropped as a whole entry: leaving the
					// entry behind with a dangling nodeId keeps a deleted node referenced.
					if (key == "assetBindings" || key == "referenceBindings" || key == "inputBindings") && removed[stringValue(entryMap["nodeId"])] {
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
