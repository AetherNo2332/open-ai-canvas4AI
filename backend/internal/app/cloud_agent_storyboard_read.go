package app

import (
	"encoding/json"
	"strings"

	"infinite-canvas/backend/internal/canvas/references"
	"infinite-canvas/backend/internal/repository"
)

type cloudAgentStoryboardReadArgs struct {
	NodeID string   `json:"nodeId"`
	View   string   `json:"view"`
	Mode   string   `json:"mode"`
	Offset int      `json:"offset"`
	Rows   int      `json:"rows"`
	RowIDs []string `json:"rowIds"`
}

func cloudAgentReadStoryboard(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (any, error) {
	var args cloudAgentStoryboardReadArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if err := validateCloudAgentID(args.NodeID, "分镜节点ID", 80); err != nil {
		return nil, err
	}
	if args.View == "" {
		args.View = "bindings"
	}
	if args.View != "summary" && args.View != "bindings" && args.View != "full" {
		return nil, cloudAgentFieldError("view", "invalid_value", "view 必须是 summary、bindings 或 full")
	}
	if args.Mode != "" && args.Mode != "image" && args.Mode != "video" {
		return nil, cloudAgentFieldError("mode", "invalid_value", "mode 必须是 image 或 video；省略只解析声明引用")
	}
	if args.Offset < 0 || args.Rows < 0 || args.Rows > 100 || len(args.RowIDs) > 100 || (args.View == "full" && args.Rows > cloudAgentMaxReadRows) {
		return nil, BadAuthRequest("分镜分页范围无效；full 最多20行，轻量视图最多100行")
	}
	if len(args.RowIDs) > 0 && args.Offset != 0 {
		return nil, BadAuthRequest("rowIds 与 offset 不能同时使用")
	}
	canvas, err := repo.CanvasProjectForUser(userID, canvasID)
	if err != nil {
		return nil, err
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		return nil, err
	}
	node, board, all, err := storyboardNodeFromDocument(doc, args.NodeID)
	if err != nil {
		return nil, err
	}
	selected := all
	if len(args.RowIDs) > 0 {
		selected = []map[string]any{}
		seen := map[string]bool{}
		for _, id := range args.RowIDs {
			index := cloudAgentNodeIndex(all, id)
			if index < 0 || seen[id] {
				return nil, cloudAgentFieldError("rowIds", "invalid_value", "rowIds 含不存在或重复的分镜行")
			}
			seen[id] = true
			selected = append(selected, all[index])
		}
	}
	if args.Offset > len(selected) {
		return nil, BadAuthRequest("分镜偏移超出范围")
	}
	limit := args.Rows
	if limit == 0 {
		limit = 100
		if args.View == "full" {
			limit = 1
		}
	}
	reader := newCloudAgentReferenceReader(repo, userID, canvasID, doc)
	rows := []any{}
	missingMedia, invalid, unavailable, unknown, migration, missingImage, missingVideo := []string{}, []string{}, []string{}, []string{}, []string{}, []string{}, []string{}
	processed, next, bytes := 0, 0, 4096
	header, _ := json.Marshal(map[string]any{"referenceNodeIds": cloudAgentStrings(board["referenceNodeIds"]), "visibleColumns": cloudAgentStoryboardVisibleColumns(board), "rowIds": args.RowIDs})
	bytes += len(header)
	for index := args.Offset; index < len(selected); index++ {
		if processed >= limit {
			next = index
			break
		}
		row := selected[index]
		id := stringValue(row["id"])
		resolved := reader.resolve(references.Scope{Kind: "storyboard-row", NodeID: args.NodeID, RowID: id, Mode: args.Mode})
		item := map[string]any{"rowId": id, "shotNumber": row["shotNumber"], "assetBindings": cloudAgentStoryboardBindingFields(row, "assetBindings"), "characters": cloudAgentStoryboardBindingFields(row, "characters"), "references": resolved,
			"imageNodeId": stringValue(row["imageNodeId"]), "videoNodeId": stringValue(row["videoNodeId"]),
			"hasImagePrompt": strings.TrimSpace(stringValue(row["imageGenerationPrompt"])) != "", "hasVideoPrompt": strings.TrimSpace(stringValue(row["videoMotionPrompt"])) != ""}
		if args.View == "full" {
			page := cloudAgentStoryboardState(map[string]any{"rows": []map[string]any{row}}, 0, cloudAgentProjectionDetail, limit)
			for _, full := range creationMaps(page["rows"]) {
				for key, value := range full {
					item[key] = value
				}
			}
		}
		rowBytes := 0
		if args.View != "summary" {
			body, marshalErr := json.Marshal(item)
			if marshalErr != nil {
				return nil, marshalErr
			}
			rowBytes = len(body)
		}
		// Reserve space for every summary list as well as the projected row.
		if bytes+rowBytes+8*len(id)+128 > cloudAgentReadPageBytes-1024 {
			if processed == 0 {
				return nil, BadAuthRequest("单行超过读取预算；先用 summary 检查引用状态，再精读所需内容")
			}
			next = index
			break
		}
		bytes += rowBytes + 8*len(id) + 128
		if args.View != "summary" {
			rows = append(rows, item)
		}
		processed++
		if resolved["state"] == "invalid" {
			invalid = append(invalid, id)
		} else if resolved["state"] == "migration_required" {
			migration = append(migration, id)
		} else if resolved["state"] == "resolved" && resolved["mediaInputCount"] == 0 {
			missingMedia = append(missingMedia, id)
		}
		if count, ok := resolved["unknownInputs"].(int); ok && count > 0 {
			unknown = append(unknown, id)
		}
		if count, ok := resolved["unavailableInputs"].(int); ok && count > 0 {
			unavailable = append(unavailable, id)
		}
		if stringValue(row["imageNodeId"]) == "" {
			missingImage = append(missingImage, id)
		}
		if stringValue(row["videoNodeId"]) == "" {
			missingVideo = append(missingVideo, id)
		}
	}
	coverage := map[string]any{"totalRows": len(all), "selectedRows": len(selected), "inspectedRows": processed, "complete": args.Offset == 0 && processed == len(all) && len(selected) == len(all)}
	resultBoard := map[string]any{"referenceNodeIds": cloudAgentStrings(board["referenceNodeIds"]), "rows": rows, "visibleColumns": cloudAgentStoryboardVisibleColumns(board), "totalRows": len(all), "nextOffset": next, "hasMore": next > 0,
		"videoInputMode": firstNonEmpty(stringValue(cloudAgentNodeMetadata(node)["storyboardVideoInputMode"]), "direct")}
	if next > 0 {
		nextArgs := map[string]any{"nodeId": args.NodeID, "view": args.View, "offset": next, "rows": limit}
		if args.Mode != "" {
			nextArgs["mode"] = args.Mode
		}
		if len(args.RowIDs) > 0 {
			delete(nextArgs, "offset")
			nextArgs["rowIds"] = args.RowIDs[next:]
		}
		resultBoard["nextRead"] = map[string]any{"tool": "canvas_read_storyboard", "arguments": nextArgs}
	}
	result := map[string]any{"nodeId": args.NodeID, "title": truncateRunes(stringValue(node["title"]), 300), "snapshotHash": cloudAgentCanvasHash(doc), "view": args.View, "mode": firstNonEmpty(args.Mode, "declared"), "storyboard": resultBoard, "coverage": coverage, "pageByteBudget": cloudAgentReadPageBytes,
		"referenceMigration": map[string]any{"required": !references.Enabled(doc)}, "summary": map[string]any{"scope": "inspected_rows", "rowsWithoutMediaInputs": missingMedia, "rowsWithInvalidReferences": invalid, "rowsWithUnavailableInputs": unavailable, "rowsWithUnknownInputs": unknown, "rowsRequiringMigration": migration, "rowsWithoutImageOutput": missingImage, "rowsWithoutVideoOutput": missingVideo}}
	if body, err := json.Marshal(result); err != nil {
		return nil, err
	} else if len(body) > cloudAgentReadPageBytes {
		return nil, BadAuthRequest("分镜读取结果超过预算，请缩小 rowIds 或 rows")
	}
	return result, nil
}

func cloudAgentStoryboardBindingFields(row map[string]any, collection string) []any {
	keys := []string{"nodeId", "role", "priority"}
	if collection == "characters" {
		keys = []string{"characterName", "characterAssetId", "characterVersionId", "characterImageNodeId"}
	}
	values := []any{}
	for _, entry := range creationMaps(row[collection]) {
		value := map[string]any{}
		for _, key := range keys {
			if text, ok := entry[key].(string); ok {
				value[key] = truncateRunes(text, 240)
			} else if number, ok := cloudAgentSafeNumber(entry[key]); ok {
				value[key] = number
			}
		}
		values = append(values, value)
	}
	return values
}
