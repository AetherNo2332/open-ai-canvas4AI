package legacymigrate

import (
	"fmt"
	"sort"
	"strings"

	"yingce/backend/internal/canvas/capability"
)

func inspect(doc map[string]any, options Options) (Identity, Explicit, []Resource, []Issue, []Issue) {
	identity := Identity{Nodes: []NodeIdentity{}, Rows: []RowIdentity{}, Columns: []ColumnIdentity{}}
	explicit := emptyExplicit()
	resources := []Resource{}
	blocking := []Issue{}
	warnings := []Issue{}
	byID := map[string]map[string]any{}
	for index, node := range list(doc["nodes"]) {
		inspectNode(node, index, options, &identity, &explicit, &resources, &blocking, &warnings, byID)
	}
	connectionIDs := map[string]bool{}
	for index, edge := range list(doc["connections"]) {
		inspectConnection(edge, index, byID, connectionIDs, &explicit, &blocking)
	}
	checkResources(options, resources, &warnings)
	sortIssues(blocking)
	sortIssues(warnings)
	return identity, explicit, uniqueResources(resources), blocking, warnings
}

func emptyExplicit() Explicit {
	return Explicit{
		RowOutputs: []HandleRef{}, RowInputs: []HandleRef{}, BatchColumns: []HandleRef{},
		BatchOutputs: []BatchOutput{}, EmptyCells: []CellRef{}, Frames: []FrameRef{},
	}
}

func inspectNode(node map[string]any, index int, options Options, identity *Identity, explicit *Explicit, resources *[]Resource, blocking, warnings *[]Issue, byID map[string]map[string]any) {
	id, nodeType := strings.TrimSpace(text(node["id"])), strings.TrimSpace(text(node["type"]))
	path := fmt.Sprintf("nodes[%d]", index)
	if id == "" {
		*blocking = append(*blocking, Issue{Code: "node_id_missing", Path: path, Message: "节点缺少 id"})
		return
	}
	if byID[id] != nil {
		*blocking = append(*blocking, Issue{Code: "duplicate_node_id", Path: path, Message: "节点 id 重复：" + id})
		return
	}
	byID[id] = node
	identity.Nodes = append(identity.Nodes, NodeIdentity{ID: id, Type: nodeType})
	if nodeType == "" {
		*blocking = append(*blocking, Issue{Code: "node_type_missing", Path: path, Message: "节点缺少 type：" + id})
	} else if !knownNodeType(nodeType, options.Registry) {
		*warnings = append(*warnings, Issue{Code: "unknown_node_type", Path: path, Message: "节点类型不在当前注册表：" + nodeType})
	}
	inspectNodeMetadata(node, id, nodeType, identity, explicit, resources, blocking)
}

func inspectNodeMetadata(node map[string]any, id, nodeType string, identity *Identity, explicit *Explicit, resources *[]Resource, blocking *[]Issue) {
	metadata := object(node["metadata"])
	*resources = append(*resources, metadataResources(metadata, id)...)
	if nodeType == "script" {
		identity.Rows = append(identity.Rows, storyboardRows(metadata, id, blocking)...)
	}
	if nodeType == "batch-table" {
		rows, columns, empty := batchIdentity(metadata, id, blocking)
		identity.Rows = append(identity.Rows, rows...)
		identity.Columns = append(identity.Columns, columns...)
		explicit.EmptyCells = append(explicit.EmptyCells, empty...)
	}
	if nodeType == "video" {
		for _, frame := range []struct{ kind, field string }{{"first-frame", "videoStartFrameNodeId"}, {"last-frame", "videoEndFrameNodeId"}} {
			if ref := strings.TrimSpace(text(metadata[frame.field])); ref != "" {
				explicit.Frames = append(explicit.Frames, FrameRef{NodeID: id, Kind: frame.kind, RefID: ref})
			}
		}
	}
}

func inspectConnection(edge map[string]any, index int, byID map[string]map[string]any, connectionIDs map[string]bool, explicit *Explicit, blocking *[]Issue) {
	path := fmt.Sprintf("connections[%d]", index)
	edgeID := strings.TrimSpace(text(edge["id"]))
	if edgeID != "" {
		if connectionIDs[edgeID] {
			*blocking = append(*blocking, Issue{Code: "duplicate_connection_id", Path: path, Message: "连线 id 重复：" + edgeID})
		}
		connectionIDs[edgeID] = true
	}
	fromID, toID := text(edge["fromNodeId"]), text(edge["toNodeId"])
	from, to := byID[fromID], byID[toID]
	if from == nil || to == nil {
		*blocking = append(*blocking, Issue{Code: "dangling_connection", Path: path, Message: "连线端点不在节点表：" + fromID + " -> " + toID})
		return
	}
	fromHandle, toHandle := text(edge["fromHandleId"]), text(edge["toHandleId"])
	if rowID, ok := strings.CutPrefix(fromHandle, "row:"); ok {
		if !hasRow(from, "storyboard", rowID) {
			*blocking = append(*blocking, Issue{Code: "row_handle_missing", Path: path, Message: "fromHandleId 指向不存在的分镜行：" + rowID})
		} else {
			explicit.RowOutputs = append(explicit.RowOutputs, HandleRef{EdgeID: edgeID, NodeID: fromID, HandleID: fromHandle, RefID: rowID})
		}
	}
	if rowID, ok := strings.CutPrefix(toHandle, "row:"); ok {
		if !hasRow(to, "storyboard", rowID) {
			*blocking = append(*blocking, Issue{Code: "row_handle_missing", Path: path, Message: "toHandleId 指向不存在的分镜行：" + rowID})
		} else {
			explicit.RowInputs = append(explicit.RowInputs, HandleRef{EdgeID: edgeID, NodeID: toID, HandleID: toHandle, RefID: rowID})
		}
	}
	if tag, columnID, ok := batchHandle(toHandle); ok {
		if !hasColumn(to, columnID) {
			*blocking = append(*blocking, Issue{Code: "batch_column_missing", Path: path, Message: "toHandleId 指向不存在的批量列：" + columnID})
		} else {
			explicit.BatchColumns = append(explicit.BatchColumns, HandleRef{EdgeID: edgeID, NodeID: toID, HandleID: toHandle, RefID: tag + ":" + columnID})
		}
	}
	if text(from["type"]) == "batch-table" && text(edge["relation"]) == "batch-output" {
		rowID := text(edge["storyboardRowId"])
		if rowID == "" || !hasRow(from, "batchTable", rowID) {
			*blocking = append(*blocking, Issue{Code: "batch_output_row_missing", Path: path, Message: "批量输出连线缺少有效行"})
		} else {
			explicit.BatchOutputs = append(explicit.BatchOutputs, BatchOutput{TableID: fromID, RowID: rowID, NodeID: toID})
		}
	}
}

func checkResources(options Options, resources []Resource, warnings *[]Issue) {
	if options.ResourceExists == nil {
		return
	}
	missing := map[string]bool{}
	for index := range resources {
		exists := options.ResourceExists(resources[index].Kind, resources[index].ID)
		resources[index].Exists = &exists
		key := resources[index].Kind + "\x00" + resources[index].ID
		if !exists && !missing[key] {
			missing[key] = true
			*warnings = append(*warnings, Issue{Code: "resource_missing", Path: pathOf("resources", resources[index].Kind, resources[index].ID), Message: "资源不存在或不属于当前账号：" + resources[index].Kind + "/" + resources[index].ID})
		}
	}
}

func storyboardRows(metadata map[string]any, nodeID string, blocking *[]Issue) []RowIdentity {
	rows := []RowIdentity{}
	ids := map[string]bool{}
	for index, row := range list(object(metadata["storyboard"])["rows"]) {
		rowID := strings.TrimSpace(text(row["id"]))
		if rowID == "" {
			*blocking = append(*blocking, Issue{Code: "row_id_missing", Path: fmt.Sprintf("nodes[%s].metadata.storyboard.rows[%d]", nodeID, index), Message: "分镜行缺少 id"})
			continue
		}
		if ids[rowID] {
			*blocking = append(*blocking, Issue{Code: "duplicate_row_id", Path: fmt.Sprintf("nodes[%s].metadata.storyboard.rows[%d]", nodeID, index), Message: "分镜行 id 重复：" + rowID})
			continue
		}
		ids[rowID] = true
		rows = append(rows, RowIdentity{NodeID: nodeID, Kind: "storyboard", RowID: rowID})
	}
	return rows
}

func batchIdentity(metadata map[string]any, tableID string, blocking *[]Issue) ([]RowIdentity, []ColumnIdentity, []CellRef) {
	table := object(metadata["batchTable"])
	rows := []RowIdentity{}
	rowIDs := map[string]bool{}
	for index, row := range list(table["rows"]) {
		rowID := strings.TrimSpace(text(row["id"]))
		if rowID == "" {
			*blocking = append(*blocking, Issue{Code: "row_id_missing", Path: fmt.Sprintf("nodes[%s].metadata.batchTable.rows[%d]", tableID, index), Message: "批量行缺少 id"})
			continue
		}
		if rowIDs[rowID] {
			*blocking = append(*blocking, Issue{Code: "duplicate_row_id", Path: fmt.Sprintf("nodes[%s].metadata.batchTable.rows[%d]", tableID, index), Message: "批量行 id 重复：" + rowID})
			continue
		}
		rowIDs[rowID] = true
		rows = append(rows, RowIdentity{NodeID: tableID, Kind: "batch", RowID: rowID})
	}
	columns := []ColumnIdentity{}
	columnIDs := map[string]bool{}
	for _, group := range []struct{ field, kind string }{{"referenceColumns", "batch-reference"}, {"textColumns", "batch-text"}} {
		for index, column := range list(table[group.field]) {
			columnID := strings.TrimSpace(text(column["id"]))
			if columnID == "" {
				*blocking = append(*blocking, Issue{Code: "column_id_missing", Path: fmt.Sprintf("nodes[%s].metadata.batchTable.%s[%d]", tableID, group.field, index), Message: "批量列缺少 id"})
				continue
			}
			key := group.kind + "\x00" + columnID
			if columnIDs[key] {
				*blocking = append(*blocking, Issue{Code: "duplicate_column_id", Path: fmt.Sprintf("nodes[%s].metadata.batchTable.%s[%d]", tableID, group.field, index), Message: "批量列 id 重复：" + columnID})
				continue
			}
			columnIDs[key] = true
			columns = append(columns, ColumnIdentity{TableID: tableID, ColumnID: columnID, Kind: group.kind})
		}
	}
	empty := []CellRef{}
	for _, row := range list(table["rows"]) {
		rowID := strings.TrimSpace(text(row["id"]))
		cells := object(row["cells"])
		keys := make([]string, 0, len(cells))
		for key := range cells {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, columnID := range keys {
			if value, ok := cells[columnID].(string); ok && value == "" {
				empty = append(empty, CellRef{TableID: tableID, RowID: rowID, ColumnID: columnID})
			}
		}
	}
	return rows, columns, empty
}

func metadataResources(metadata map[string]any, nodeID string) []Resource {
	out := []Resource{}
	if key := strings.TrimSpace(text(metadata["storageKey"])); key != "" {
		kind, id := "storage", key
		if resourceID, ok := strings.CutPrefix(key, "resource:"); ok {
			kind, id = "resource", resourceID
		}
		out = append(out, Resource{Kind: kind, ID: id, NodeID: nodeID, Role: "output"})
	}
	if id := strings.TrimSpace(text(metadata["assetId"])); id != "" {
		out = append(out, Resource{Kind: "asset", ID: id, NodeID: nodeID, Role: "asset"})
	}
	if id := strings.TrimSpace(text(metadata["drawingId"])); id != "" {
		out = append(out, Resource{Kind: "drawing", ID: id, NodeID: nodeID, Role: "drawing"})
	}
	return out
}

func knownNodeType(name string, registry *capability.Registry) bool {
	if registry == nil {
		registry = capability.BuiltinRegistry()
	}
	_, ok := registry.ResolveNode(name, "")
	return ok
}

func batchHandle(handle string) (string, string, bool) {
	if column, ok := strings.CutPrefix(handle, "batch-text:"); ok && column != "" {
		return "batch-text", column, true
	}
	if column, ok := strings.CutPrefix(handle, "batch-reference:"); ok && column != "" {
		return "batch-reference", column, true
	}
	return "", "", false
}

func hasRow(node map[string]any, field, rowID string) bool {
	for _, row := range list(object(object(node["metadata"])[field])["rows"]) {
		if text(row["id"]) == rowID {
			return true
		}
	}
	return false
}

func batchRow(node map[string]any, rowID string) map[string]any {
	for _, row := range list(object(object(node["metadata"])["batchTable"])["rows"]) {
		if text(row["id"]) == rowID {
			return row
		}
	}
	return nil
}

func hasColumn(node map[string]any, columnID string) bool {
	table := object(object(node["metadata"])["batchTable"])
	for _, field := range []string{"referenceColumns", "textColumns"} {
		for _, column := range list(table[field]) {
			if text(column["id"]) == columnID {
				return true
			}
		}
	}
	return false
}
