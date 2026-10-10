package app

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var cloudAgentCopySuffix = regexp.MustCompile(`(?i)^(.*)_copy([0-9]+)$`)
var cloudAgentLegacyCopySuffix = regexp.MustCompile(`(?i) Copy$`)

func cloudAgentCopyTitle(title string, nodes []map[string]any) string {
	base := strings.TrimSpace(cloudAgentLegacyCopySuffix.ReplaceAllString(title, ""))
	if match := cloudAgentCopySuffix.FindStringSubmatch(title); match != nil {
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
		match := cloudAgentCopySuffix.FindStringSubmatch(stringValue(node["title"]))
		if len(match) == 3 && match[1] == base {
			index, err := strconv.Atoi(match[2])
			if err == nil && index <= 9007199254740991 {
				maxIndex = max(maxIndex, index)
			}
		}
	}
	return fmt.Sprintf("%s_copy%d", base, maxIndex+1)
}
func cloudAgentCopyGraph(nodes, edges []map[string]any, op agentCanvasOp) ([]map[string]any, []map[string]any, cloudAgentApprovalPreviewItem, error) {
	item := cloudAgentApprovalPreviewItem{Operation: "duplicate_node", NodeID: op.ID}
	sourceIndex := cloudAgentNodeIndex(nodes, op.SourceNodeID)
	if sourceIndex < 0 || cloudAgentNodeIndex(nodes, op.ID) >= 0 {
		return nodes, edges, item, BadAuthRequest("复制来源不存在或副本 ID 重复")
	}
	source := nodes[sourceIndex]
	sources := []map[string]any{source}
	frame := stringValue(source["type"]) == "frame"
	if frame {
		for _, node := range nodes {
			if stringValue(node["parentId"]) == op.SourceNodeID {
				sources = append(sources, node)
			}
		}
	}
	idMap := map[string]string{op.SourceNodeID: op.ID}
	for i, node := range sources[1:] {
		id := fmt.Sprintf("%s-child-%d", op.ID, i+1)
		if err := validateCloudAgentID(id, "副本节点 ID", 80); err != nil {
			return nodes, edges, item, err
		}
		if cloudAgentNodeIndex(nodes, id) >= 0 {
			return nodes, edges, item, BadAuthRequest("副本成员 ID 已存在")
		}
		idMap[stringValue(node["id"])] = id
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
	for _, node := range sources {
		if _, ok := cloudAgentNodeCapabilityForNode(node); !ok {
			return nodes, edges, item, BadAuthRequest("复制包含不支持的节点类型")
		}
		if stringValue(node["type"]) == "drawing" && stringValue(cloudAgentNodeMetadata(node)["drawingId"]) != "" && cloudAgentNodeMetadata(node)["drawingDocument"] == nil {
			return nodes, edges, item, BadAuthRequest("绘图文档尚未同步，不能只复制预览节点")
		}
		raw, _ := json.Marshal(node)
		var copy map[string]any
		if err := json.Unmarshal(raw, &copy); err != nil {
			return nodes, edges, item, err
		}
		oldID := stringValue(node["id"])
		copy["id"] = idMap[oldID]
		if parent := stringValue(copy["parentId"]); idMap[parent] != "" {
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
		metadata := cloudAgentNodeMetadata(copy)
		cloudAgentResetCopiedMetadata(metadata, idMap)
		cloudAgentRemapCopiedReferences(metadata, idMap)
		metadata["copiedFromNodeId"] = oldID
		switch stringValue(node["type"]) {
		case "image", "video", "audio":
			metadata["generationResultPlacement"] = "replace-node"
		}
		if stringValue(node["type"]) == "drawing" {
			metadata["drawingId"] = idMap[oldID] + "-document"
			metadata["drawingRevision"] = float64(0)
			metadata["drawingShapeCount"] = float64(0)
			metadata["drawingPageCount"] = float64(1)
			delete(metadata, "drawingUpdatedAt")
			if document, ok := metadata["drawingDocument"].(map[string]any); ok {
				document["revision"] = float64(1)
				document["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
				metadata["drawingEngine"] = document["engine"]
				metadata["drawingRevision"] = document["revision"]
				metadata["drawingUpdatedAt"] = document["updatedAt"]
				metadata["drawingShapeCount"] = document["shapeCount"]
				metadata["drawingPageCount"] = document["pageCount"]
			}
		}
		if oldID == op.SourceNodeID {
			copy["title"] = cloudAgentCopyTitle(stringValue(node["title"]), nodes)
			if op.Title != nil {
				copy["title"] = *op.Title
			}
			if len([]rune(stringValue(copy["title"]))) > 240 {
				return nodes, edges, item, BadAuthRequest("副本标题超出限制")
			}
			item.NodeTitle = stringValue(node["title"])
			item.NodeType = stringValue(node["type"])
			item.ResultTitle = stringValue(copy["title"])
		}
		copies = append(copies, copy)
	}
	nextEdges := append([]map[string]any{}, edges...)
	edgeIndex := 0
	for _, edge := range edges {
		from, to := stringValue(edge["fromNodeId"]), stringValue(edge["toNodeId"])
		if idMap[to] == "" || (idMap[from] == "" && frame) {
			continue
		}
		edgeIndex++
		edgeID := fmt.Sprintf("%s-edge-%d", op.ID, edgeIndex)
		if err := validateCloudAgentID(edgeID, "副本连线 ID", 80); err != nil {
			return nodes, edges, item, err
		}
		if cloudAgentNodeIndex(nextEdges, edgeID) >= 0 {
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
		nextEdges = append(nextEdges, copy)
	}
	item.Summary = fmt.Sprintf("复制 %d 个节点及关联连线，并隔离生成任务身份", len(copies))
	return append(nodes, copies...), nextEdges, item, nil
}

func cloudAgentResetCopiedMetadata(meta map[string]any, idMap map[string]string) {
	for _, key := range []string{"taskId", "taskClientOperationId", "retryOf", "attemptGroupId", "taskStatus", "taskProgress", "taskStage", "taskMediaStage", "taskCanRecoverMedia", "taskProvider", "taskStartedAt", "taskCompletedAt", "taskDurationMs", "taskErrorCode", "taskOfficialStatus", "taskReceiptRecorded", "taskCreatedAt", "taskUpdatedAt", "generationBatches", "batchRootId", "batchChildIds", "batchFailedCount", "isBatchRoot", "primaryImageId", "imageBatchExpanded", "batchUsesReferenceImages", "versionOfNodeId", "versionLabel", "versionPrimary", "errorDetails", "generationErrorCode", "resourceReloadAvailable", "failedPromptFingerprint"} {
		delete(meta, key)
	}
	for key := range meta {
		if strings.HasPrefix(key, "generationApplied") || strings.HasPrefix(key, "generationPersistence") {
			delete(meta, key)
		}
	}
	meta["status"] = "idle"
	if stringValue(meta["content"]) != "" {
		meta["status"] = "success"
	}
	for _, key := range []string{"referenceSetId", "previsPreviewNodeId", "previsDepthNodeId", "previsNormalNodeId"} {
		if source := stringValue(meta[key]); source != "" {
			if idMap[source] == "" {
				delete(meta, key)
			} else {
				meta[key] = idMap[source]
			}
		}
	}
	if refs, ok := meta["referenceAssetNodeIds"]; ok {
		next := []any{}
		for _, id := range cloudAgentStrings(refs) {
			if idMap[id] != "" {
				next = append(next, idMap[id])
			}
		}
		meta["referenceAssetNodeIds"] = next
	}
	if views, ok := meta["characterViewNodeIds"].(map[string]any); ok {
		for key, value := range views {
			if idMap[stringValue(value)] == "" {
				delete(views, key)
			} else {
				views[key] = idMap[stringValue(value)]
			}
		}
		if len(views) == 0 {
			delete(meta, "characterViewNodeIds")
		}
	}
	if board, ok := meta["storyboard"].(map[string]any); ok {
		for _, row := range creationMaps(board["rows"]) {
			owned := false
			for _, key := range []string{"imageNodeId", "videoNodeId"} {
				source := stringValue(row[key])
				if idMap[source] != "" {
					row[key] = idMap[source]
					owned = true
				} else {
					delete(row, key)
				}
			}
			delete(row, "errorDetails")
			if !owned {
				row["status"] = "idle"
			}
		}
	}
	if table, ok := meta["batchTable"].(map[string]any); ok {
		for _, row := range creationMaps(table["rows"]) {
			if source := stringValue(row["outputNodeId"]); idMap[source] == "" {
				delete(row, "outputNodeId")
			} else {
				row["outputNodeId"] = idMap[source]
			}
		}
	}
}
func cloudAgentRemapCopiedReferences(object map[string]any, idMap map[string]string) {
	for key, value := range object {
		switch typed := value.(type) {
		case map[string]any:
			cloudAgentRemapCopiedReferences(typed, idMap)
		case []any:
			for i, entry := range typed {
				if nested, ok := entry.(map[string]any); ok {
					cloudAgentRemapCopiedReferences(nested, idMap)
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
