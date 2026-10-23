package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"infinite-canvas/backend/internal/canvas/capability"
)

type cloudAgentStoryboardAsset struct {
	Ref  string `json:"ref"`
	Role string `json:"role"`
}

// Provider call IDs may repeat in another model step. Resolve service-owned
// identities before approval so replay and apply use the same durable identity.
func cloudAgentResolveWorkflowIDs(userID, runID string, state *cloudAgentRuntime, call cloudAgentCall) (cloudAgentCall, error) {
	if call.Function.Name != "canvas_create_storyboard" && call.Function.Name != "canvas_create_character" && call.Function.Name != "canvas_edit_storyboard" {
		return call, nil
	}
	var fields map[string]json.RawMessage
	if err := decodeCloudAgentJSONObject(call.Function.Arguments, &fields); err != nil {
		return call, cloudAgentJSONArgumentError(err)
	}
	if call.Function.Name == "canvas_edit_storyboard" {
		var action string
		if err := json.Unmarshal(fields["action"], &action); err != nil {
			return call, cloudAgentJSONArgumentError(err)
		}
		if action == "append" || action == "append_rows" {
			// These inputs are checkpointed while approval is pending. Rebuild the
			// seed on restart without rewriting the original, schema-validated call.
			call.storyboardMutationID = cloudAgentID(userID, fmt.Sprintf("storyboard-append:%s:%s:%d:%s:%s", state.Request.CanvasID, runID, state.Step, state.LastStepTaskID, call.ID))
		}
		return call, nil
	}
	var id string
	if raw, exists := fields["nodeId"]; exists {
		if err := json.Unmarshal(raw, &id); err != nil {
			return call, cloudAgentJSONArgumentError(err)
		}
	}
	if id != "" {
		return call, nil
	}
	id = cloudAgentID(userID, fmt.Sprintf("%s:%s:%s:%d:%s:%s", call.Function.Name, state.Request.CanvasID, runID, state.Step, state.LastStepTaskID, call.ID))
	fields["nodeId"], _ = json.Marshal(id)
	raw, err := json.Marshal(fields)
	if err != nil {
		return call, err
	}
	call.Function.Arguments = string(raw)
	return call, nil
}

func cloudAgentStoryboardAssetsSchema() map[string]any {
	return map[string]any{"type": "array", "maxItems": 100, "items": map[string]any{
		"type": "object", "properties": map[string]any{
			"ref":  map[string]any{"type": "string", "minLength": 1, "description": "当前画布节点 ID 或唯一的完整名称；同名多版本必须使用读取结果中的 ID"},
			"role": map[string]any{"type": "string", "enum": []string{"character", "environment", "wardrobe", "prop", "weapon", "style", "motion", "audio"}},
		}, "required": []string{"ref", "role"}, "additionalProperties": false,
	}}
}

func cloudAgentResolveBusinessReferences(doc map[string]any, refs []string) ([]string, error) {
	if len(refs) > 100 {
		return nil, BadAuthRequest("资产引用最多100项")
	}
	result := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" {
			return nil, cloudAgentFieldError("ref", "invalid_reference", "资产引用不能为空")
		}
		matches := []string{}
		for _, node := range creationMaps(doc["nodes"]) {
			if cloudAgentNodeUsageStatus(node) == "archived" {
				continue
			}
			id, title := stringValue(node["id"]), stringValue(node["title"])
			if id == ref {
				matches = []string{id}
				break
			}
			meta := cloudAgentNodeMetadata(node)
			if title == ref || stringValue(meta["characterName"]) == ref {
				matches = append(matches, id)
			}
		}
		if len(matches) == 0 {
			return nil, cloudAgentFieldError("ref", "reference_missing", "找不到可用资产「"+truncateRunes(ref, 120)+"」，请先搜索画布并选择实际节点")
		}
		if len(matches) != 1 {
			sort.Strings(matches)
			return nil, cloudAgentFieldError("ref", "reference_ambiguous", "资产「"+truncateRunes(ref, 120)+"」存在多个版本或同名节点，请明确选择节点 ID："+strings.Join(matches, "、"))
		}
		if !seen[matches[0]] {
			result = append(result, matches[0])
			seen[matches[0]] = true
		}
	}
	return result, nil
}

func cloudAgentNormalizeStoryboardPatch(doc, patch map[string]any) (map[string]any, error) {
	result := map[string]any{}
	for key, value := range patch {
		result[key] = value
	}
	if raw, exists := result["assets"]; exists {
		if _, conflict := result["assetBindings"]; conflict {
			return nil, BadAuthRequest("assets 与底层 assetBindings 不能同时提交")
		}
		if raw == nil {
			return nil, BadAuthRequest("assets 必须是数组；清空资产使用 []")
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		var assets []cloudAgentStoryboardAsset
		if err := json.Unmarshal(encoded, &assets); err != nil || len(assets) > 100 {
			return nil, BadAuthRequest("assets 必须是最多100项的资产引用数组")
		}
		bindings := []any{}
		seen := map[string]bool{}
		for _, asset := range assets {
			ids, err := cloudAgentResolveBusinessReferences(doc, []string{asset.Ref})
			if err != nil {
				return nil, err
			}
			if seen[ids[0]] {
				return nil, BadAuthRequest("同一镜头资产不能重复，请为每项资产选择一个用途")
			}
			seen[ids[0]] = true
			priority := map[string]int{"character": 100, "environment": 90, "wardrobe": 80, "prop": 80, "weapon": 80, "style": 60, "motion": 70, "audio": 70}[asset.Role]
			if priority == 0 {
				return nil, BadAuthRequest("资产用途无效")
			}
			bindings = append(bindings, map[string]any{"nodeId": ids[0], "role": asset.Role, "priority": float64(priority)})
		}
		delete(result, "assets")
		result["assetBindings"] = bindings
		// Both legacy characters and assetBindings feed generation. Replacing the
		// declared cast must not silently retain a second, stale character list.
		result["characters"] = []any{}
	}
	if err := capability.ValidateEditableFields(capability.StoryboardRowFields(), result); err != nil {
		return nil, BadAuthRequest(err.Error())
	}
	if err := validateCloudAgentStoryboardReferences(doc, result); err != nil {
		return nil, err
	}
	return result, nil
}

// Plan on a copy so one invalid target or reference cannot partially mutate a batch.
func cloudAgentPlanStoryboardRows(doc map[string]any, rows []map[string]any, args cloudAgentStoryboardEditArgs, userID, callID string) ([]map[string]any, []string, error) {
	if args.Action != "append" && args.Action != "append_rows" && args.Action != "batch_update" && args.Action != "update" && args.Action != "remove" && args.Action != "set_assets" {
		return nil, nil, BadAuthRequest("不支持的镜头编辑操作")
	}
	if args.References != nil || args.ReferenceNodeIDs != nil || args.OutputMode != "" || args.OutputNodeID != "" || (args.Action != "set_assets" && args.Assets != nil) {
		return nil, nil, BadAuthRequest("镜头编辑包含不适用于当前操作的参数")
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return nil, nil, err
	}
	next := []map[string]any{}
	if err := json.Unmarshal(raw, &next); err != nil {
		return nil, nil, err
	}
	if args.Action == "batch_update" || args.Action == "append_rows" {
		return cloudAgentPlanStoryboardBatch(doc, next, args, userID, callID)
	}
	if args.Updates != nil || args.Rows != nil {
		return nil, nil, BadAuthRequest("当前操作不接受 updates 或 rows")
	}
	ids := append([]string(nil), args.RowIDs...)
	if args.RowID != "" {
		if len(ids) > 0 {
			return nil, nil, BadAuthRequest("rowId 与 rowIds 只能选择一种")
		}
		ids = []string{args.RowID}
	}
	if args.Action == "append" {
		if len(ids) > 0 || len(next) >= maxCloudAgentStoryboardRows {
			return nil, nil, BadAuthRequest("追加镜头不能指定已有行，分镜最多100行")
		}
	} else if len(ids) == 0 || len(ids) > 100 {
		return nil, nil, BadAuthRequest("请选择1到100个真实分镜行 ID")
	}
	selectionField := "rowIds"
	if args.RowID != "" {
		selectionField = "rowId"
	}
	if err := validateCloudAgentStoryboardRowIDs(next, ids, selectionField); err != nil {
		return nil, nil, err
	}
	selected := map[string]bool{}
	for _, id := range ids {
		selected[id] = true
	}
	patch := args.Patch
	if args.Action == "set_assets" {
		if len(patch) > 0 || args.Assets == nil {
			return nil, nil, BadAuthRequest("set_assets 需要 assets，不接受 patch；[] 表示清空")
		}
		patch = map[string]any{"assets": args.Assets}
	}
	fields := []string{}
	if args.Action == "remove" {
		if len(patch) > 0 {
			return nil, nil, BadAuthRequest("删除镜头不接受 patch")
		}
		retained := []map[string]any{}
		for _, row := range next {
			if selected[stringValue(row["id"])] && stringValue(row["status"]) == "loading" {
				return nil, nil, BadAuthRequest("镜头正在生成，不能删除；请先等待任务结束")
			}
			if !selected[stringValue(row["id"])] {
				retained = append(retained, row)
			}
		}
		next = retained
		fields = []string{"镜头行"}
	} else {
		if len(patch) == 0 {
			return nil, nil, BadAuthRequest("镜头编辑需要 patch")
		}
		normalized, err := cloudAgentNormalizeStoryboardPatch(doc, patch)
		if err != nil {
			return nil, nil, err
		}
		for key := range normalized {
			fields = append(fields, key)
		}
		sort.Strings(fields)
		if args.Action == "append" {
			if err := validateCloudAgentStoryboardRow(normalized, true); err != nil {
				return nil, nil, err
			}
			row := cloudAgentStoryboardRowDefaults()
			for key, value := range normalized {
				row[key] = value
			}
			row["id"] = cloudAgentStoryboardAppendRowID(userID, args.NodeID, callID, next)
			next = append(next, row)
		} else {
			for _, row := range next {
				if !selected[stringValue(row["id"])] {
					continue
				}
				if err := cloudAgentApplyStoryboardRowPatch(row, normalized); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	for index, row := range next {
		row["shotNumber"] = float64(index + 1)
	}
	return next, fields, nil
}

func cloudAgentApplyStoryboardRowPatch(row, patch map[string]any) error {
	for key, value := range patch {
		row[key] = value
	}
	// Persisted IDs, output ownership and legacy metadata are not editable fields.
	creative := map[string]any{"durationSeconds": row["durationSeconds"], "plotDescription": stringValue(row["plotDescription"]), "videoMotionPrompt": stringValue(row["videoMotionPrompt"])}
	if err := validateCloudAgentStoryboardRow(creative, true); err != nil {
		return err
	}
	return validateCloudAgentStoryboardSourceRange(row)
}

func cloudAgentPlanStoryboardBatch(doc map[string]any, rows []map[string]any, args cloudAgentStoryboardEditArgs, userID, callID string) ([]map[string]any, []string, error) {
	if args.RowID != "" || args.RowIDs != nil || args.Patch != nil {
		return nil, nil, BadAuthRequest("批量操作不接受 rowId、rowIds 或 patch；请使用 updates 或 rows")
	}
	count, field := len(args.Updates), "updates"
	if args.Action == "append_rows" {
		count, field = len(args.Rows), "rows"
		if args.Updates != nil {
			return nil, nil, BadAuthRequest("append_rows 不接受 updates")
		}
		if len(rows)+count > maxCloudAgentStoryboardRows {
			return nil, nil, cloudAgentFieldError("rows", "item_count", "rows 追加后分镜最多100行；整批未执行")
		}
	} else if args.Rows != nil {
		return nil, nil, BadAuthRequest("batch_update 不接受 rows")
	}
	if count < 1 || count > maxCloudAgentStoryboardRows {
		return nil, nil, cloudAgentFieldError(field, "item_count", field+" 必须包含1到100项；整批未执行")
	}
	if args.Action == "batch_update" {
		ids := make([]string, count)
		known, seen := map[string]bool{}, map[string]bool{}
		for _, row := range rows {
			known[stringValue(row["id"])] = true
		}
		badIndex := -1
		for i, update := range args.Updates {
			ids[i] = update.RowID
			if badIndex < 0 && (!known[update.RowID] || seen[update.RowID]) {
				badIndex = i
			}
			seen[update.RowID] = true
		}
		if err := validateCloudAgentStoryboardRowIDs(rows, ids, "updates"); err != nil {
			path := fmt.Sprintf("updates[%d].rowId", badIndex)
			if typed, ok := err.(*cloudAgentStoryboardRowIDError); ok {
				typed.error = cloudAgentFieldError(path, "invalid_row_ids", path+" 不存在或重复；使用最新读取的 rowId；整批未执行")
			}
			return nil, nil, err
		}
	}
	byID := map[string]map[string]any{}
	for _, row := range rows {
		byID[stringValue(row["id"])] = row
	}
	fields := map[string]bool{}
	for i := 0; i < count; i++ {
		path := fmt.Sprintf("rows[%d]", i)
		var input map[string]any
		if args.Action == "batch_update" {
			input = args.Updates[i].Patch
			path = fmt.Sprintf("updates[%d].patch", i)
		} else {
			input = args.Rows[i]
		}
		normalized, err := cloudAgentNormalizeStoryboardPatch(doc, input)
		if err != nil {
			return nil, nil, cloudAgentFieldError(path, "invalid_row", path+" 无效："+err.Error()+"；整批未执行")
		}
		if args.Action == "append_rows" {
			err = validateCloudAgentStoryboardRow(normalized, true)
			if err == nil {
				row := cloudAgentStoryboardRowDefaults()
				for key, value := range normalized {
					row[key] = value
				}
				row["id"] = cloudAgentStoryboardAppendRowID(userID, args.NodeID, callID, rows)
				row["shotNumber"] = float64(len(rows) + 1)
				rows = append(rows, row)
			}
		} else {
			err = cloudAgentApplyStoryboardRowPatch(byID[args.Updates[i].RowID], normalized)
		}
		if err != nil {
			return nil, nil, cloudAgentFieldError(path, "invalid_row", path+" 无效："+err.Error()+"；整批未执行")
		}
		for name := range normalized {
			fields[name] = true
		}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return rows, names, nil
}

func cloudAgentStoryboardAppendRowID(userID, nodeID, mutationID string, rows []map[string]any) string {
	seed := fmt.Sprintf("storyboard:%s:%s:%d", nodeID, mutationID, len(rows)+1)
	used := make(map[string]bool, len(rows))
	for _, row := range rows {
		used[stringValue(row["id"])] = true
	}
	for attempt := 0; ; attempt++ {
		candidateSeed := seed
		if attempt > 0 {
			candidateSeed = fmt.Sprintf("%s:collision:%d", seed, attempt)
		}
		id := cloudAgentID(userID, candidateSeed)
		if !used[id] {
			return id
		}
	}
}
