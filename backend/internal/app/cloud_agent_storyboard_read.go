package app

import (
	"context"
	"encoding/json"
	"strings"

	"yingce/backend/internal/canvas/capability"
	"yingce/backend/internal/canvas/references"
	"yingce/backend/internal/repository"
)

type cloudAgentStoryboardReadArgs struct {
	OperationIDs     []string `json:"operationIds,omitempty"`
	NodeID           string   `json:"nodeId"`
	View             string   `json:"view"`
	Mode             string   `json:"mode"`
	Offset           int      `json:"offset"`
	Rows             int      `json:"rows"`
	RowIDs           []string `json:"rowIds"`
	Fields           []string `json:"fields"`
	MissingAnyFields []string `json:"missingAnyFields"`
}

func cloudAgentReadStoryboard(repo *repository.Repository, userID, canvasID string, call cloudAgentCall) (any, error) {
	var args cloudAgentStoryboardReadArgs
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &args); err != nil {
		return nil, cloudAgentJSONArgumentError(err)
	}
	if err := validateCloudAgentID(args.NodeID, "分镜节点ID", 80); err != nil {
		return nil, err
	}
	var provided map[string]json.RawMessage
	_ = json.Unmarshal([]byte(call.Function.Arguments), &provided)
	if _, exists := provided["operationIds"]; exists && len(args.OperationIDs) == 0 {
		return nil, cloudAgentFieldError("operationIds", "invalid_value", "operationIds 不能为空")
	}
	if _, exists := provided["operationIds"]; exists {
		if err := validateCloudAgentStoryboardOperationIDs(args.OperationIDs); err != nil {
			return nil, err
		}
	}
	for key, fields := range map[string][]string{"fields": args.Fields, "missingAnyFields": args.MissingAnyFields} {
		if _, exists := provided[key]; !exists {
			continue
		}
		if len(fields) == 0 {
			return nil, cloudAgentFieldError(key, "invalid_value", key+" 不能为空")
		}
		seen := map[string]bool{}
		for _, field := range fields {
			if (field != "durationSeconds" && !cloudAgentStoryboardTextField(field)) || seen[field] {
				return nil, cloudAgentFieldError(key, "invalid_value", key+" 含有未知或重复的创作字段: "+field)
			}
			seen[field] = true
		}
	}
	if args.View == "" {
		args.View = "creative"
	}
	if args.View != "summary" && args.View != "bindings" && args.View != "full" && args.View != "creative" {
		return nil, cloudAgentFieldError("view", "invalid_value", "view 必须是 creative、summary、bindings 或 full")
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
	var result any
	err := repo.CanvasReadTransaction(context.Background(), func(tx *repository.Repository) error {
		var err error
		result, err = cloudAgentReadStoryboardSnapshot(tx, userID, canvasID, args)
		return err
	})
	return result, err
}

func cloudAgentReadStoryboardSnapshot(repo *repository.Repository, userID, canvasID string, args cloudAgentStoryboardReadArgs) (any, error) {
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
		if err := validateCloudAgentStoryboardRowIDs(all, args.RowIDs, "rowIds"); err != nil {
			return nil, err
		}
		selected = []map[string]any{}
		for _, id := range args.RowIDs {
			index := cloudAgentNodeIndex(all, id)
			selected = append(selected, all[index])
		}
	}
	selectionRows := len(selected)
	if len(args.MissingAnyFields) > 0 {
		matched := []map[string]any{}
		for _, row := range selected {
			if cloudAgentStoryboardMissingAny(row, args.MissingAnyFields) {
				matched = append(matched, row)
			}
		}
		selected = matched
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
	contentTruncated := false
	includeRows := args.View != "summary" || len(args.Fields) > 0
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
		if args.View == "full" && len(args.Fields) == 0 {
			page := cloudAgentStoryboardState(map[string]any{"rows": []map[string]any{row}}, 0, cloudAgentProjectionDetail, limit)
			for _, full := range creationMaps(page["rows"]) {
				for key, value := range full {
					item[key] = value
				}
			}
		}
		if args.View == "creative" {
			item = cloudAgentStoryboardCreativeRow(args.NodeID, row, resolved)
		}
		cloudAgentStoryboardPromptMetadata(item, row)
		if len(args.Fields) > 0 {
			for field := range capability.StoryboardRowFields() {
				if field == "assetBindings" || field == "characters" {
					continue
				}
				delete(item, field)
				delete(item, field+"Truncated")
			}
			delete(item, "nextReads")
			cloudAgentStoryboardProjectFields(item, args.NodeID, row, args.Fields, 2000)
		} else if args.View == "full" {
			reads := map[string]any{}
			for _, field := range cloudAgentStoryboardTextFields {
				if item[field+"Truncated"] == true {
					text, _ := item[field].(string)
					// The state projection appends a display ellipsis. Expose the
					// exact source prefix so continuation offsets never count it.
					offset := len([]rune(strings.TrimSuffix(text, "...")))
					original, _ := row[field].(string)
					item[field] = string([]rune(original)[:offset])
					reads[field] = cloudAgentStoryboardContentContinuation(args.NodeID, row["id"], field, offset)
				}
			}
			if len(reads) > 0 {
				item["nextReads"] = reads
			}
		}
		rowBytes := 0
		if includeRows {
			body, marshalErr := json.Marshal(item)
			if marshalErr != nil {
				return nil, marshalErr
			}
			rowBytes = len(body)
			// A field projection may request every text column on a single row.
			// Reduce previews together, keeping exact continuation offsets.
			for preview := 1000; processed == 0 && len(args.Fields) > 0 && bytes+rowBytes+8*len(id)+128 > cloudAgentReadPageBytes-1024 && preview >= 1; preview /= 2 {
				cloudAgentStoryboardProjectFields(item, args.NodeID, row, args.Fields, preview)
				body, _ = json.Marshal(item)
				rowBytes = len(body)
			}
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
		if includeRows {
			rows = append(rows, item)
			if _, exists := item["nextReads"]; exists {
				contentTruncated = true
			}
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
	tableComplete := args.Offset == 0 && processed == len(all) && len(selected) == len(all)
	coverage := map[string]any{"totalRows": len(all), "selectionRows": selectionRows, "selectedRows": len(selected), "matchedRows": len(selected), "inspectedRows": processed, "offset": args.Offset, "complete": tableComplete, "selectionComplete": next == 0, "tableComplete": tableComplete, "contentTruncated": contentTruncated}
	resultBoard := map[string]any{"referenceNodeIds": cloudAgentStrings(board["referenceNodeIds"]), "rows": rows, "visibleColumns": cloudAgentStoryboardVisibleColumns(board), "totalRows": len(all), "nextOffset": next, "hasMore": next > 0,
		"videoInputMode": firstNonEmpty(stringValue(cloudAgentNodeMetadata(node)["storyboardVideoInputMode"]), "direct")}
	if next > 0 {
		nextArgs := map[string]any{"nodeId": args.NodeID, "view": args.View, "offset": next, "rows": limit}
		if args.Mode != "" {
			nextArgs["mode"] = args.Mode
		}
		if len(args.RowIDs) > 0 {
			delete(nextArgs, "offset")
			ids := []string{}
			for _, row := range selected[next:] {
				ids = append(ids, stringValue(row["id"]))
			}
			nextArgs["rowIds"] = ids
		}
		if len(args.Fields) > 0 {
			nextArgs["fields"] = args.Fields
		}
		if len(args.MissingAnyFields) > 0 {
			nextArgs["missingAnyFields"] = args.MissingAnyFields
		}
		resultBoard["nextRead"] = map[string]any{"tool": "canvas_read_storyboard", "arguments": nextArgs}
	}
	result := map[string]any{"nodeId": args.NodeID, "title": truncateRunes(stringValue(node["title"]), 300), "snapshotHash": cloudAgentCanvasHash(doc), "view": args.View, "mode": firstNonEmpty(args.Mode, "declared"), "storyboard": resultBoard, "coverage": coverage, "pageByteBudget": cloudAgentReadPageBytes,
		"referenceMigration": map[string]any{"required": !references.Enabled(doc)}, "summary": map[string]any{"scope": "inspected_rows", "rowsWithoutMediaInputs": missingMedia, "rowsWithInvalidReferences": invalid, "rowsWithUnavailableInputs": unavailable, "rowsWithUnknownInputs": unknown, "rowsRequiringMigration": migration, "rowsWithoutImageOutput": missingImage, "rowsWithoutVideoOutput": missingVideo}}
	revision, err := cloudAgentStoryboardRevision(repo, userID, canvasID, doc, args.NodeID)
	if err != nil {
		return nil, err
	}
	result["storyboardRevision"] = revision
	if len(args.OperationIDs) > 0 {
		operations, err := cloudAgentStoryboardOperationStatuses(repo, userID, canvasID, args.NodeID, args.OperationIDs)
		if err != nil {
			return nil, err
		}
		result["operations"] = operations
	}
	if body, err := json.Marshal(result); err != nil {
		return nil, err
	} else if len(body) > cloudAgentReadPageBytes {
		return nil, BadAuthRequest("分镜读取结果超过预算，请缩小 rowIds 或 rows")
	}
	return result, nil
}

func cloudAgentStoryboardCreativeRow(nodeID string, row, resolved map[string]any) map[string]any {
	item := map[string]any{"rowId": row["id"], "shotNumber": row["shotNumber"], "durationSeconds": row["durationSeconds"], "inputs": resolved["effectiveInputs"], "inputState": resolved["state"], "imageNodeId": stringValue(row["imageNodeId"]), "videoNodeId": stringValue(row["videoNodeId"]), "hasImagePrompt": strings.TrimSpace(stringValue(row["imageGenerationPrompt"])) != "", "hasVideoPrompt": strings.TrimSpace(stringValue(row["videoMotionPrompt"])) != ""}
	if issue := resolved["issue"]; issue != nil {
		item["issue"] = issue
	}
	cloudAgentStoryboardPromptMetadata(item, row)
	cloudAgentStoryboardProjectFields(item, nodeID, row, []string{"plotDescription", "dialogue"}, 2000)
	return item
}

func cloudAgentStoryboardPromptMetadata(item, row map[string]any) {
	_, imageSource := cloudAgentStoryboardEffectivePrompt(row, "image")
	_, videoSource := cloudAgentStoryboardEffectivePrompt(row, "video")
	item["hasImagePromptOverride"] = imageSource == "imageGenerationPrompt"
	item["hasVideoPromptOverride"] = videoSource == "videoMotionPrompt"
	item["effectivePromptSource"] = map[string]any{"image": imageSource, "video": videoSource}
}

func cloudAgentStoryboardMissingAny(row map[string]any, fields []string) bool {
	for _, field := range fields {
		if field == "durationSeconds" {
			if err := capability.ValidateEditableFields(capability.StoryboardRowFields(), map[string]any{field: row[field]}); err != nil {
				return true
			}
		} else if strings.TrimSpace(stringValue(row[field])) == "" {
			return true
		}
	}
	return false
}

func cloudAgentStoryboardProjectFields(item map[string]any, nodeID string, row map[string]any, fields []string, preview int) {
	reads := map[string]any{}
	for _, key := range fields {
		if key == "durationSeconds" {
			item[key] = row[key]
			continue
		}
		value, _ := row[key].(string)
		text := []rune(value)
		end := min(len(text), preview)
		item[key] = string(text[:end])
		delete(item, key+"Truncated")
		if end < len(text) {
			item[key+"Truncated"] = true
			reads[key] = cloudAgentStoryboardContentContinuation(nodeID, row["id"], key, end)
		}
	}
	if len(reads) > 0 {
		item["nextReads"] = reads
	} else {
		delete(item, "nextReads")
	}
}

func cloudAgentStoryboardContentContinuation(nodeID string, rowID any, field string, offset int) map[string]any {
	return map[string]any{"tool": "canvas_read_content", "arguments": map[string]any{"nodeId": nodeID, "rowId": rowID, "field": field, "offset": offset, "limit": 8000}}
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
