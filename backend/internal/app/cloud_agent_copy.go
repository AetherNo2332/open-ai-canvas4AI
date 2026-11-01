package app

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"yingce/backend/internal/canvas/references"
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

// cloudAgentCopyPlan holds one duplicate_node: the source members, their new
// IDs, the position offset and storyboard rows re-derived on the copies.
type cloudAgentCopyPlan struct {
	op         agentCanvasOp
	nodes      []map[string]any
	sources    []map[string]any
	frame      bool
	idMap      map[string]string
	dx, dy     float64
	copiedRows map[string]string
}

func cloudAgentCopyGraph(nodes, edges []map[string]any, op agentCanvasOp) ([]map[string]any, []map[string]any, cloudAgentApprovalPreviewItem, error) {
	item := cloudAgentApprovalPreviewItem{Operation: "duplicate_node", NodeID: op.ID}
	plan, err := cloudAgentCopySources(nodes, op)
	if err != nil {
		return nodes, edges, item, err
	}
	copies := []map[string]any{}
	for _, node := range plan.sources {
		copy, err := plan.copyNode(node, &item)
		if err != nil {
			return nodes, edges, item, err
		}
		copies = append(copies, copy)
	}
	nextEdges, err := plan.copyEdges(edges)
	if err != nil {
		return nodes, edges, item, err
	}
	item.Summary = fmt.Sprintf("复制 %d 个节点及关联连线，并隔离生成任务身份", len(copies))
	return append(nodes, copies...), nextEdges, item, nil
}

// cloudAgentCopySources resolves the source (a frame copies its children too),
// assigns copy IDs and computes the position offset.
func cloudAgentCopySources(nodes []map[string]any, op agentCanvasOp) (*cloudAgentCopyPlan, error) {
	sourceIndex := cloudAgentNodeIndex(nodes, op.SourceNodeID)
	if sourceIndex < 0 || cloudAgentNodeIndex(nodes, op.ID) >= 0 {
		return nil, BadAuthRequest("复制来源不存在或副本 ID 重复")
	}
	source := nodes[sourceIndex]
	plan := &cloudAgentCopyPlan{op: op, nodes: nodes, sources: []map[string]any{source}, frame: stringValue(source["type"]) == "frame", copiedRows: map[string]string{}}
	if plan.frame {
		for _, node := range nodes {
			if stringValue(node["parentId"]) == op.SourceNodeID {
				plan.sources = append(plan.sources, node)
			}
		}
	}
	plan.idMap = map[string]string{op.SourceNodeID: op.ID}
	for i, node := range plan.sources[1:] {
		id := fmt.Sprintf("%s-child-%d", op.ID, i+1)
		if err := validateCloudAgentID(id, "副本节点 ID", 80); err != nil {
			return nil, err
		}
		if cloudAgentNodeIndex(nodes, id) >= 0 {
			return nil, BadAuthRequest("副本成员 ID 已存在")
		}
		plan.idMap[stringValue(node["id"])] = id
	}
	sourcePosition, _ := source["position"].(map[string]any)
	plan.dx, plan.dy = 36.0, 36.0
	if op.X != nil {
		value, _ := sourcePosition["x"].(float64)
		plan.dx = *op.X - value
	}
	if op.Y != nil {
		value, _ := sourcePosition["y"].(float64)
		plan.dy = *op.Y - value
	}
	return plan, nil
}

// copyNode clones one member, remaps its IDs and position, and resets
// generation identity; the copied source also gets its new title.
func (plan *cloudAgentCopyPlan) copyNode(node map[string]any, item *cloudAgentApprovalPreviewItem) (map[string]any, error) {
	if _, ok := cloudAgentNodeCapabilityForNode(node); !ok {
		return nil, BadAuthRequest("复制包含不支持的节点类型")
	}
	if stringValue(node["type"]) == "drawing" && stringValue(cloudAgentNodeMetadata(node)["drawingId"]) != "" && cloudAgentNodeMetadata(node)["drawingDocument"] == nil {
		return nil, BadAuthRequest("绘图文档尚未同步，不能只复制预览节点")
	}
	raw, _ := json.Marshal(node)
	var copy map[string]any
	if err := json.Unmarshal(raw, &copy); err != nil {
		return nil, err
	}
	oldID := stringValue(node["id"])
	copy["id"] = plan.idMap[oldID]
	if parent := stringValue(copy["parentId"]); plan.idMap[parent] != "" {
		copy["parentId"] = plan.idMap[parent]
	}
	if err := plan.offsetPosition(copy); err != nil {
		return nil, err
	}
	metadata := cloudAgentNodeMetadata(copy)
	for source, derived := range cloudAgentResetCopiedMetadata(metadata, stringValue(copy["id"]), plan.idMap) {
		plan.copiedRows[source] = derived
	}
	cloudAgentRemapCopiedReferences(metadata, plan.idMap)
	metadata["copiedFromNodeId"] = oldID
	switch stringValue(node["type"]) {
	case "image", "video", "audio":
		metadata["generationResultPlacement"] = "replace-node"
	}
	if stringValue(node["type"]) == "drawing" {
		cloudAgentResetCopiedDrawing(metadata, plan.idMap[oldID])
	}
	if oldID == plan.op.SourceNodeID {
		copy["title"] = cloudAgentCopyTitle(stringValue(node["title"]), plan.nodes)
		if plan.op.Title != nil {
			copy["title"] = *plan.op.Title
		}
		if len([]rune(stringValue(copy["title"]))) > 240 {
			return nil, BadAuthRequest("副本标题超出限制")
		}
		item.NodeTitle = stringValue(node["title"])
		item.NodeType = stringValue(node["type"])
		item.ResultTitle = stringValue(copy["title"])
	}
	return copy, nil
}

func (plan *cloudAgentCopyPlan) offsetPosition(copy map[string]any) error {
	position, _ := copy["position"].(map[string]any)
	if position == nil {
		position = map[string]any{}
		copy["position"] = position
	}
	for axis, delta := range map[string]float64{"x": plan.dx, "y": plan.dy} {
		value, _ := position[axis].(float64)
		value += delta
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e6 {
			return BadAuthRequest("副本位置无效")
		}
		position[axis] = value
	}
	return nil
}

// cloudAgentResetCopiedDrawing gives a copied drawing its own document identity
// and restarts its revision at 1 when a native document is present.
func cloudAgentResetCopiedDrawing(metadata map[string]any, copyID string) {
	metadata["drawingId"] = copyID + "-document"
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

// copyEdges copies unmanaged edges into the copied members; a frame copy only
// keeps edges whose both ends were copied.
func (plan *cloudAgentCopyPlan) copyEdges(edges []map[string]any) ([]map[string]any, error) {
	nextEdges := append([]map[string]any{}, edges...)
	edgeIndex := 0
	for _, edge := range edges {
		// Managed edges are derived from declared fields. Copying one would carry the
		// source owner's markers onto the copy, so the target scope re-coordinates them
		// from the copied fields instead.
		if stringValue(edge["managedBy"]) == references.ManagedBy {
			continue
		}
		from, to := stringValue(edge["fromNodeId"]), stringValue(edge["toNodeId"])
		if plan.idMap[to] == "" || (plan.idMap[from] == "" && plan.frame) {
			continue
		}
		edgeIndex++
		edgeID := fmt.Sprintf("%s-edge-%d", plan.op.ID, edgeIndex)
		if err := validateCloudAgentID(edgeID, "副本连线 ID", 80); err != nil {
			return nil, err
		}
		if cloudAgentNodeIndex(nextEdges, edgeID) >= 0 {
			return nil, BadAuthRequest("副本连线 ID 已存在")
		}
		nextEdges = append(nextEdges, plan.copyEdge(edge, edgeID, from, to))
	}
	return nextEdges, nil
}

func (plan *cloudAgentCopyPlan) copyEdge(edge map[string]any, edgeID, from, to string) map[string]any {
	raw, _ := json.Marshal(edge)
	var copy map[string]any
	_ = json.Unmarshal(raw, &copy)
	copy["id"] = edgeID
	copy["toNodeId"] = plan.idMap[to]
	if plan.idMap[from] != "" {
		copy["fromNodeId"] = plan.idMap[from]
	}
	if row := stringValue(copy["storyboardRowId"]); row != "" {
		if derived := plan.copiedRows[row]; derived != "" {
			copy["storyboardRowId"] = derived
		} else {
			delete(copy, "storyboardRowId")
		}
	}
	return copy
}

// cloudAgentResetCopiedMetadata clears generation state a node copy must not inherit.
// nodeID is the copied node's id, or "" when the caller only swaps a media asset. It
// returns the source-to-copy identity mapping of the copied table rows so that row
// scoped connections can be rebound onto the copy instead of the source table. The
// command planner applies the same rules; the shared conformance fixture pins them.
func cloudAgentResetCopiedMetadata(meta map[string]any, nodeID string, idMap map[string]string) map[string]string {
	cloudAgentClearCopiedGenerationState(meta)
	cloudAgentRemapCopiedReferenceKeys(meta, idMap)
	return cloudAgentRebindCopiedRowIDs(meta, nodeID, idMap)
}

// cloudAgentClearCopiedGenerationState 清掉副本不得继承的生成与任务状态；有正文时
// 状态落到 success，否则回到 idle。
func cloudAgentClearCopiedGenerationState(meta map[string]any) {
	delete(meta, "mediaVersions")
	delete(meta, "currentMediaVersionId")
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
}

// cloudAgentRemapCopiedReferenceKeys 把引用类键重定向到副本节点；源节点没有副本时丢弃引用。
func cloudAgentRemapCopiedReferenceKeys(meta map[string]any, idMap map[string]string) {
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
}

// cloudAgentRebindCopiedRowIDs 重接分镜与批量表的行输出，返回源行到副本行的
// ID 映射，供行级连线重新挂接到副本。
func cloudAgentRebindCopiedRowIDs(meta map[string]any, nodeID string, idMap map[string]string) map[string]string {
	var rowIDs map[string]string
	if board, ok := meta["storyboard"].(map[string]any); ok {
		rowIDs = map[string]string{}
		for index, row := range creationMaps(board["rows"]) {
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
			cloudAgentIsolateCopiedRow(row, nodeID, "row", index, rowIDs, owned)
		}
	}
	if table, ok := meta["batchTable"].(map[string]any); ok {
		if rowIDs == nil {
			rowIDs = map[string]string{}
		}
		for index, row := range creationMaps(table["rows"]) {
			owned := false
			if source := stringValue(row["outputNodeId"]); idMap[source] == "" {
				delete(row, "outputNodeId")
			} else {
				row["outputNodeId"] = idMap[source]
				owned = true
			}
			cloudAgentIsolateCopiedRow(row, nodeID, "batch-row", index, rowIDs, owned)
		}
	}
	return rowIDs
}

// cloudAgentIsolateCopiedRow makes a copied table row a new row. Task identity always
// goes, because a copy is never the task that produced the source result; an in-flight
// source status also resets so the copy cannot show a running task it does not own.
func cloudAgentIsolateCopiedRow(row map[string]any, nodeID, suffix string, index int, rowIDs map[string]string, owned bool) {
	for _, key := range []string{"taskId", "taskClientOperationId", "taskStatus", "taskProgress", "taskStage", "errorDetails"} {
		delete(row, key)
	}
	if !owned || cloudAgentRowTaskActive(stringValue(row["status"])) {
		row["status"] = "idle"
	}
	if nodeID == "" {
		return
	}
	source := stringValue(row["id"])
	next := fmt.Sprintf("%s-%s-%d", nodeID, suffix, index+1)
	if source != "" {
		rowIDs[source] = next
	}
	row["id"] = next
}

func cloudAgentRowTaskActive(status string) bool {
	switch status {
	case "loading", "running", "pending", "queued":
		return true
	}
	return false
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
