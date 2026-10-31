package app

import (
	"encoding/json"
	"reflect"
	"sort"
)

// 回执按实际持久内容比较，避免单行编辑重复附送全表行标识。
func cloudAgentStoryboardMutationReceipt(plan *cloudAgentStoryboardMutationPlan, call cloudAgentCall) (map[string]any, error) {
	if len(plan.Preview.Items) == 0 {
		return nil, BadAuthRequest("分镜操作缺少目标节点")
	}
	nodeID := plan.Preview.Items[0].NodeID
	beforeDoc, err := creationDocument(plan.BeforeJSON)
	if err != nil {
		return nil, err
	}
	_, board, rows, err := storyboardNodeFromDocument(plan.Document, nodeID)
	if err != nil {
		return nil, err
	}
	beforeBoard := map[string]any{}
	beforeRows := []map[string]any{}
	for _, node := range creationMaps(beforeDoc["nodes"]) {
		if stringValue(node["id"]) != nodeID {
			continue
		}
		_, beforeBoard, beforeRows, err = storyboardNodeFromDocument(beforeDoc, nodeID)
		if err != nil {
			return nil, err
		}
		break
	}
	oldByID, newByID := map[string]map[string]any{}, map[string]map[string]any{}
	for _, row := range beforeRows {
		oldByID[stringValue(row["id"])] = row
	}
	changes, created := []any{}, []any{}
	for _, row := range rows {
		id := stringValue(row["id"])
		newByID[id] = row
		previous, exists := oldByID[id]
		if !exists {
			created = append(created, map[string]any{"rowId": id, "shotNumber": row["shotNumber"]})
			continue
		}
		if fields := cloudAgentStoryboardChangedFields(previous, row, "id"); len(fields) > 0 {
			changes = append(changes, map[string]any{"rowId": id, "shotNumber": row["shotNumber"], "changedFields": fields})
		}
	}
	removed := []string{}
	for _, row := range beforeRows {
		id := stringValue(row["id"])
		if _, exists := newByID[id]; !exists {
			removed = append(removed, id)
		}
	}
	affected := len(created)
	if call.Function.Name != "canvas_create_storyboard" {
		var args cloudAgentStoryboardEditArgs
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return nil, cloudAgentJSONArgumentError(err)
		}
		if args.Action == "update" && len(args.Updates) > 0 {
			args.Action = "batch_update"
		}
		switch args.Action {
		case "append", "append_rows":
			affected = len(created)
		case "batch_update":
			affected = len(args.Updates)
		default:
			affected = len(args.RowIDs)
			if args.RowID != "" {
				affected = 1
			}
		}
	}
	snapshot := cloudAgentCanvasHash(plan.Document)
	receipt := map[string]any{
		"canvasId": plan.Canvas.ID, "nodeId": nodeID, "snapshotHash": snapshot, "beforeSnapshotHash": plan.BeforeSnapshotHash,
		"applied": true, "changed": snapshot != plan.BeforeSnapshotHash, "affectedRows": affected, "changedRows": changes,
		"summary": plan.Preview.Description, "preview": plan.Preview,
	}
	if len(created) > 0 {
		receipt["createdRows"] = created
	}
	if len(removed) > 0 {
		receipt["removedRowIds"] = removed
	}
	if fields := cloudAgentStoryboardChangedFields(beforeBoard, board, "rows"); len(fields) > 0 {
		receipt["changedFields"] = fields
	}
	assets, err := cloudAgentStoryboardAssetChanges(beforeDoc, plan.Document, nodeID, beforeRows, rows, cloudAgentStrings(board["referenceNodeIds"]))
	if err != nil {
		return nil, err
	}
	if len(assets) > 0 {
		receipt["assetChanges"] = assets
		remaining := []string{}
		for _, change := range creationMaps(assets) {
			if change["detailsOmitted"] == true {
				remaining = append(remaining, stringValue(change["rowId"]))
			}
		}
		if len(remaining) > 0 {
			receipt["assetChangesNextRead"] = map[string]any{"tool": "storyboard", "arguments": map[string]any{"action": "read", "nodeId": nodeID, "rowIds": remaining, "view": "bindings"}}
		}
	}
	if call.Function.Name == "canvas_edit_storyboard" || call.Function.Name == "storyboard" {
		var args cloudAgentStoryboardEditArgs
		_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		if cloudAgentStoryboardSharedAction(args.Action) {
			old, next := cloudAgentStrings(beforeBoard["referenceNodeIds"]), cloudAgentStrings(board["referenceNodeIds"])
			added, removed, retained := []string{}, []string{}, []string{}
			for _, id := range old {
				if cloudAgentContainsString(next, id) {
					retained = append(retained, id)
				} else {
					removed = append(removed, id)
				}
			}
			for _, id := range next {
				if !cloudAgentContainsString(old, id) {
					added = append(added, id)
				}
			}
			receipt["referenceChanges"] = map[string]any{"scope": "shared", "addedNodeIds": added, "removedNodeIds": removed, "retainedNodeIds": retained, "affectedRows": len(rows)}
		}
	}
	return receipt, nil
}

func cloudAgentStoryboardChangedFields(before, after map[string]any, excluded string) []string {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	fields := []string{}
	for key := range keys {
		old, oldExists := before[key]
		value, exists := after[key]
		if key != excluded && (oldExists != exists || !reflect.DeepEqual(old, value)) {
			fields = append(fields, key)
		}
	}
	sort.Strings(fields)
	return fields
}
