package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func cloudAgentNodeStyleField(name string) bool {
	switch name {
	case "width", "height", "folderStyle", "folderTheme", "frame", "locked":
		return true
	}
	return false
}

func cloudAgentNodeStyleError(path, name string) error {
	field := cloudAgentSchemaPath(path, name)
	return cloudAgentFieldError(field, "unexpected_field", "尺寸与样式字段 "+field+" 不接受直接写入操作对象；新建仅传 type/id/nodeType/title/content/x/y，尺寸样式须用后续 update_node.patch 并核对节点能力")
}

func cloudAgentArgumentHints(schema map[string]any, args map[string]any, field string) map[string]any {
	parent, current := schema, schema
	var value any = args
	path := strings.NewReplacer("[", ".", "]", "").Replace(field)
	for _, part := range strings.Split(path, ".") {
		if part == "arguments" || part == "" {
			continue
		}
		if index, err := strconv.Atoi(part); err == nil {
			current, _ = current["items"].(map[string]any)
			items, _ := value.([]any)
			value = nil
			if index >= 0 && index < len(items) {
				value = items[index]
			}
		} else {
			object, _ := value.(map[string]any)
			parent = cloudAgentHintObject(current, object)
			fields, _ := current["properties"].(map[string]any)
			current, _ = fields[part].(map[string]any)
			value = object[part]
		}
		if current == nil {
			break
		}
	}
	properties, _ := parent["properties"].(map[string]any)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	hints := map[string]any{"allowedFields": names, "required": cloudAgentSchemaStrings(parent["required"])}
	constraints := map[string]any{}
	for _, key := range []string{"type", "enum", "const", "minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems"} {
		if value, ok := current[key]; ok {
			constraints[key] = value
		}
	}
	if len(constraints) > 0 {
		hints["fieldConstraints"] = constraints
	}
	return hints
}

func cloudAgentHintObject(schema, object map[string]any) map[string]any {
	for _, branch := range cloudAgentSchemaBranchList(schema["oneOf"]) {
		if !cloudAgentSchemaBranchDiscriminated(branch, object) {
			continue
		}
		properties := schema["properties"]
		if branch["additionalProperties"] == false {
			properties = branch["properties"]
		}
		required := slices.Clone(cloudAgentSchemaStrings(schema["required"]))
		for _, name := range cloudAgentSchemaStrings(branch["required"]) {
			if !slices.Contains(required, name) {
				required = append(required, name)
			}
		}
		return map[string]any{"properties": properties, "required": required}
	}
	return schema
}

func cloudAgentArgumentFeedback(state *cloudAgentRuntime, call cloudAgentCall, detail map[string]any) {
	detail["guidance"] = "本次调用未执行，请按 field、issue、argumentHints 和本轮工具契约修正；示例中的占位符须替换为真实读取值，不要重复提交相同的错误参数"
	schema, advertised := cloudAgentAdvertisedTool(state, call.Function.Name)
	if !advertised || schema == nil {
		return
	}
	args := map[string]any{}
	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
	field := stringValue(detail["field"])
	detail["argumentHints"] = cloudAgentArgumentHints(schema, args, field)
	if call.Function.Name == "canvas_apply_ops" {
		if guidance := cloudAgentCanvasOpGuidance(cloudAgentFailedCanvasOp(args, field), field); guidance != "" {
			detail["guidance"] = guidance + "；本次未执行，保留原操作意图和其他操作，用真实读取值替换示例占位符"
		}
	}
	if call.Function.Name == "storyboard" && field == "newRows" && stringValue(args["action"]) == "write_rows" {
		detail["guidance"] = "本次未写入；newRows 必须是镜头对象数组，不能是 JSON 字符串。保持目标、快照和 operationId，不要改成 split 或分批提交已确认的方案"
	}
	example := cloudAgentArgumentExampleForFields(call.Function.Name, args, field)
	if example != nil {
		body, err := json.Marshal(example)
		if err == nil && validateCloudAgentToolArguments(schema, string(body)) == nil {
			detail["exampleArguments"] = example
		}
	}
}

// Examples preserve the action, but use placeholders instead of echoing invalid input.
func cloudAgentArgumentExampleForFields(tool string, args map[string]any, field string) map[string]any {
	switch tool {
	case "canvas_get_state", "canvas_list_node_types", "canvas_list_assets", "model_list":
		return map[string]any{}
	case "canvas_read_batch_table":
		return map[string]any{"nodeId": "<nodeId>"}
	case "canvas_read_content":
		return map[string]any{"nodeId": "<nodeId>", "field": "content"}
	case "task_get":
		return map[string]any{"taskId": "<taskId>"}
	case "drama_read_episode":
		return map[string]any{"episodeId": "<episodeId>"}
	case "canvas_inspect_image":
		return cloudAgentInspectImageExample(args, field)
	case "storyboard":
		return cloudAgentStoryboardArgumentExample(args)
	case "canvas_apply_ops":
		return cloudAgentApplyOpsArgumentExample(args, field)
	}
	return nil
}

// cloudAgentInspectImageExample 保存摘要是最容易写坏的一种调用：给出完整的对象结构，nodeId/sha256 原样沿用本次调用。
func cloudAgentInspectImageExample(args map[string]any, field string) map[string]any {
	nodeID, _ := args["nodeId"].(string)
	sha, _ := args["sha256"].(string)
	if !strings.HasPrefix(field, "summary") || nodeID == "" || len(sha) != 64 {
		return nil
	}
	return map[string]any{"nodeId": nodeID, "sha256": sha, "summary": map[string]any{
		"short":    "<一句话画面内容>",
		"detailed": map[string]any{"subjects": []string{"<主体与外观>"}, "composition": "<构图>", "uncertainties": []string{}},
	}}
}

func cloudAgentStoryboardArgumentExample(args map[string]any) map[string]any {
	example := map[string]any{"snapshotHash": "<canvas_get_state.snapshotHash>", "nodeId": "<nodeId>", "action": args["action"]}
	switch args["action"] {
	case "read":
		return map[string]any{"action": "read", "nodeId": "<nodeId>"}
	case "lock", "unlock":
		example["rowId"] = "<rowId>"
		return example
	case "append":
		patch := cloudAgentPatchExample(args["patch"], cloudAgentStoryboardRowSchema())
		if patch == nil {
			return nil
		}
		example["patch"] = patch
	case "update":
		patch := cloudAgentPatchExample(args["patch"], cloudAgentStoryboardPatchSchema())
		if patch == nil {
			return nil
		}
		example["rowId"], example["patch"] = "<rowId>", patch
	case "remove":
		example["rowId"] = "<rowId>"
	case "set_assets", "set_references":
		// Guessing a replacement list could silently remove existing inputs.
		return nil
	case "bind_output", "unbind_output":
		if !cloudAgentStoryboardOutputExample(args, example) {
			return nil
		}
	default:
		return nil
	}
	if ids, ok := args["rowIds"].([]any); ok && len(ids) > 0 {
		placeholders := make([]string, len(ids))
		for index := range ids {
			placeholders[index] = fmt.Sprintf("<rowId%d>", index+1)
		}
		delete(example, "rowId")
		example["rowIds"] = placeholders
	}
	return example
}

func cloudAgentStoryboardOutputExample(args, example map[string]any) bool {
	mode, _ := args["outputMode"].(string)
	if mode != "image" && mode != "video" {
		return false
	}
	example["rowId"], example["outputMode"] = "<rowId>", mode
	if args["action"] == "bind_output" {
		example["outputNodeId"] = "<outputNodeId>"
	}
	return true
}

func cloudAgentFailedCanvasOp(args map[string]any, field string) map[string]any {
	ops := creationMaps(args["ops"])
	index := 0
	_, _ = fmt.Sscanf(field, "ops[%d]", &index)
	if index < 0 || index >= len(ops) {
		return nil
	}
	return ops[index]
}

func cloudAgentCanvasOpGuidance(op map[string]any, field string) string {
	name := field[strings.LastIndex(field, ".")+1:]
	switch stringValue(op["type"]) {
	case "add_node":
		if strings.HasPrefix(name, "workflow") {
			return "add_node 仅接受 type/id/nodeType/title/content/x/y；workflow 字段请在创建后用 update_node.patch 写入，并先查 canvas_list_node_types"
		}
	case "update_node":
		if cloudAgentOpUsesTextReplacement(op) {
			return "精确替换应使用独立操作 type=replace_text，传 id、match 和 replacement；match 必须在节点正文中唯一"
		}
		if name == "patch" && len(cloudAgentMisplacedCanvasPatch(op)) > 0 {
			return "update_node 的 title/content/prompt 必须放在 patch 对象内，不能放在操作顶层"
		}
	}
	return ""
}

func cloudAgentOpUsesTextReplacement(op map[string]any) bool {
	_, match := op["match"]
	_, replacement := op["replacement"]
	return match || replacement
}

func cloudAgentMisplacedCanvasPatch(op map[string]any) map[string]any {
	patch := map[string]any{}
	for _, name := range []string{"content", "title", "prompt"} {
		if value, exists := op[name]; exists {
			patch[name] = value
		}
	}
	return patch
}

func cloudAgentApplyOpsArgumentExample(args map[string]any, field string) map[string]any {
	op := cloudAgentCanvasOpExample(cloudAgentFailedCanvasOp(args, field))
	if op == nil {
		return nil
	}
	return map[string]any{"snapshotHash": "<canvas_get_state.snapshotHash>", "ops": []any{op}}
}

// cloudAgentCanvasOpExample builds one placeholder op of the failing op's type;
// nil means no safe example exists.
func cloudAgentCanvasOpExample(source map[string]any) map[string]any {
	op := map[string]any{"type": source["type"], "id": "<nodeId>"}
	switch op["type"] {
	case "add_node":
		nodeType := stringValue(source["nodeType"])
		if !containsString(cloudAgentNodeTypeNames(), nodeType) {
			return nil
		}
		op["id"], op["nodeType"] = "<new-node-id>", nodeType
	case "update_node":
		if cloudAgentOpUsesTextReplacement(source) {
			return map[string]any{"type": "replace_text", "id": "<nodeId>", "match": "<原文唯一片段>", "replacement": "<替换内容>"}
		}
		patch := cloudAgentPatchExample(source["patch"], cloudAgentPatchSchema())
		if patch == nil {
			patch = cloudAgentPatchExample(cloudAgentMisplacedCanvasPatch(source), cloudAgentPatchSchema())
		}
		if patch == nil {
			return nil
		}
		op["patch"] = patch
	case "delete_node":
	case "duplicate_node":
		op["id"], op["sourceNodeId"] = "<new-node-id>", "<sourceNodeId>"
	case "set_parent":
		parent, ok := source["parentId"].(string)
		if !ok {
			return nil
		}
		op["parentId"] = ""
		if parent != "" {
			op["parentId"] = "<parentId>"
		}
	case "replace_text":
		op["match"], op["replacement"] = "<原文唯一片段>", "<替换内容>"
	case "reorder_nodes":
		op["nodeIds"] = []string{"<全部节点ID，按目标顺序>"}
	case "reorder_rows":
		op["rowIds"] = []string{"<全部行ID，按目标顺序>"}
	default:
		return nil
	}
	return op
}

// Keep the requested field set; complex or ambiguous patches receive hints only.
func cloudAgentPatchExample(raw any, schema map[string]any) map[string]any {
	patch, ok := raw.(map[string]any)
	if !ok || len(patch) == 0 {
		return nil
	}
	properties, _ := schema["properties"].(map[string]any)
	example := map[string]any{}
	for key, value := range patch {
		property, ok := properties[key].(map[string]any)
		if !ok {
			return nil
		}
		if property["enum"] != nil {
			if validateCloudAgentSchemaValue(key, property, value, 0) != nil {
				return nil
			}
			example[key] = value
			continue
		}
		if text, ok := value.(string); ok && validateCloudAgentSchemaValue(key, property, text, 0) == nil {
			example[key] = "<" + key + ">"
			continue
		}
		switch property["type"] {
		case "number", "integer":
			if _, ok := cloudAgentSchemaNumber(value); !ok || validateCloudAgentSchemaValue(key, property, value, 0) != nil {
				return nil
			}
			example[key] = value
		case "boolean":
			if _, ok := value.(bool); !ok {
				return nil
			}
			example[key] = value
		default:
			return nil
		}
	}
	return example
}

func cloudAgentSnapshotFeedback(state *cloudAgentRuntime, call cloudAgentCall, detail map[string]any) {
	delete(detail, "parameters")
	delete(detail, "argumentHints")
	delete(detail, "exampleArguments")
	detail["field"], detail["issue"] = "snapshotHash", "stale_snapshot"
	detail["guidance"] = "本次未写入；先读取最新画布，重新核对目标与原操作，再使用新 snapshotHash 提交；不要改成新建节点"
	detail["nextRead"] = map[string]any{"tool": "canvas_get_state", "arguments": map[string]any{}}
	detail["retryArgumentsPatch"] = map[string]any{"snapshotHash": "<canvas_get_state.snapshotHash>"}
	var arguments map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &arguments)
	if call.Function.Name == "storyboard" && stringValue(arguments["storyboardRevision"]) != "" {
		detail["field"] = "storyboardRevision"
		detail["guidance"] = "本次未写入；先读取目标分镜和依赖，核对原补丁及人工修改后刷新 snapshotHash 与 storyboardRevision；保持 operationId 和原操作，不自动覆盖冲突内容"
		detail["nextRead"] = map[string]any{"tool": "storyboard", "arguments": map[string]any{"action": "read", "nodeId": arguments["nodeId"], "view": "creative"}}
		detail["retryArgumentsPatch"] = map[string]any{"snapshotHash": "<storyboard.snapshotHash>", "storyboardRevision": "<storyboard.storyboardRevision>"}
	}
	// Large operations stay in the existing call; only the replacement hash is needed.
	if len(call.Function.Arguments) > 1600 {
		return
	}
	schema, advertised := cloudAgentAdvertisedTool(state, call.Function.Name)
	if !advertised || validateCloudAgentToolArguments(schema, call.Function.Arguments) != nil {
		return
	}
	var example map[string]any
	if json.Unmarshal([]byte(call.Function.Arguments), &example) == nil && example != nil {
		for field, value := range detail["retryArgumentsPatch"].(map[string]any) {
			example[field] = value
		}
		detail["exampleArguments"] = example
	}
}
