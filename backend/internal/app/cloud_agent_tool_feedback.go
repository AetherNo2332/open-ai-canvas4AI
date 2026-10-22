package app

import (
	"encoding/json"
	"fmt"
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

// Describe only the relevant field and top-level names, never replay nested schemas.
func cloudAgentArgumentHints(schema map[string]any, field string) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	hints := map[string]any{"allowedFields": names, "required": cloudAgentSchemaStrings(schema["required"])}
	current := schema
	path := strings.NewReplacer("[", ".", "]", "").Replace(field)
	for _, part := range strings.Split(path, ".") {
		if part == "arguments" || part == "" {
			continue
		}
		if _, err := strconv.Atoi(part); err == nil {
			current, _ = current["items"].(map[string]any)
		} else {
			fields, _ := current["properties"].(map[string]any)
			current, _ = fields[part].(map[string]any)
		}
		if current == nil {
			break
		}
	}
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

func cloudAgentArgumentFeedback(state *cloudAgentRuntime, call cloudAgentCall, detail map[string]any) {
	detail["guidance"] = "本次调用未执行，请按 field、issue、argumentHints 和本轮工具契约修正；示例中的占位符须替换为真实读取值，不要重复提交相同的错误参数"
	schema, advertised := cloudAgentAdvertisedTool(state, call.Function.Name)
	if !advertised || schema == nil {
		return
	}
	detail["argumentHints"] = cloudAgentArgumentHints(schema, stringValue(detail["field"]))
	example := cloudAgentArgumentExample(call, stringValue(detail["field"]))
	if example != nil {
		body, err := json.Marshal(example)
		if err == nil && validateCloudAgentToolArguments(schema, string(body)) == nil {
			detail["exampleArguments"] = example
		}
	}
}

// Examples preserve the action, but use placeholders instead of echoing invalid input.
func cloudAgentArgumentExample(call cloudAgentCall, field string) map[string]any {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
	snapshot := "<canvas_get_state.snapshotHash>"
	switch call.Function.Name {
	case "canvas_get_state", "canvas_list_node_types", "agent_profile_read", "canvas_list_assets", "model_list":
		return map[string]any{}
	case "canvas_read_storyboard", "canvas_read_batch_table":
		return map[string]any{"nodeId": "<nodeId>"}
	case "canvas_read_content":
		return map[string]any{"nodeId": "<nodeId>", "field": "content"}
	case "task_get":
		return map[string]any{"taskId": "<taskId>"}
	case "canvas_edit_storyboard":
		example := map[string]any{"snapshotHash": snapshot, "nodeId": "<nodeId>", "action": args["action"]}
		switch args["action"] {
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
			mode, _ := args["outputMode"].(string)
			if mode != "image" && mode != "video" {
				return nil
			}
			example["rowId"], example["outputMode"] = "<rowId>", mode
			if args["action"] == "bind_output" {
				example["outputNodeId"] = "<outputNodeId>"
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
	case "canvas_apply_ops":
		ops := creationMaps(args["ops"])
		index := 0
		_, _ = fmt.Sscanf(field, "ops[%d]", &index)
		if index < 0 || index >= len(ops) {
			return nil
		}
		op := map[string]any{"type": ops[index]["type"], "id": "<nodeId>"}
		switch op["type"] {
		case "add_node":
			nodeType := stringValue(ops[index]["nodeType"])
			if !containsString(cloudAgentNodeTypeNames(), nodeType) {
				return nil
			}
			op["id"], op["nodeType"] = "<new-node-id>", nodeType
		case "update_node":
			patch := cloudAgentPatchExample(ops[index]["patch"], cloudAgentPatchSchema())
			if patch == nil {
				return nil
			}
			op["patch"] = patch
		case "delete_node":
		case "duplicate_node":
			op["id"], op["sourceNodeId"] = "<new-node-id>", "<sourceNodeId>"
		case "set_parent":
			parent, ok := ops[index]["parentId"].(string)
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
		return map[string]any{"snapshotHash": snapshot, "ops": []any{op}}
	}
	return nil
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
		switch property["type"] {
		case "string":
			example[key] = "<" + key + ">"
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
		example["snapshotHash"] = "<canvas_get_state.snapshotHash>"
		detail["exampleArguments"] = example
	}
}
