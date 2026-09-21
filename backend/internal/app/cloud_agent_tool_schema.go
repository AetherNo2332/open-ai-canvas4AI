package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// 本地 JSON Schema 预检：在业务工具执行之前，按**本轮实际下发给模型的 schema** 校验参数。
//
// 为什么需要它：上游 provider 的 strict function calling 并不总是可用（取决于渠道协议与
// 模型），缺字段/类型错的调用会一路进到业务代码，再以各工具自己的文案失败——一次错误方案
// 会在同一批里被放大成 8 条业务失败（handoff 工作项 B 取证：run agd11f2a72 同一步 8 个缺
// size 的视频调用）。schema 是我们自己下发的，本地校验的口径与模型看到的完全一致，
// 因此能在"零副作用"的位置挡住绝大多数参数错误。
//
// 这里只实现我们 schema 真正用到的关键字；未识别的关键字一律忽略（宁可漏判，不制造假失败）。
// 业务校验（节点是否存在、快照是否过期、额度与权限）仍由各工具自己负责——预检不读库、
// 不碰画布、不提交任务，只做纯函数判断。

const cloudAgentSchemaMaxDepth = 8

// validateCloudAgentToolArguments 校验一次工具调用的参数；返回的错误是字段级参数错误，
// 会被工具回执路径归类为 schema_error 并把 schema 回给模型。
func validateCloudAgentToolArguments(parameters map[string]any, raw string) error {
	if parameters == nil {
		return nil
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}
	var args any
	if err := json.Unmarshal([]byte(trimmed), &args); err != nil {
		return cloudAgentFieldError("arguments", "invalid_json", "工具参数必须是合法的 JSON 对象；本次调用未执行，请按 parameters 重新提交")
	}
	object, ok := args.(map[string]any)
	if !ok {
		return cloudAgentFieldError("arguments", "type_mismatch", "工具参数必须是 JSON 对象，不能是数组、字符串或 null")
	}
	return validateCloudAgentSchemaValue("", parameters, object, 0)
}

// validateCloudAgentSchemaValue 按 schema 校验一个值；field 是给人看的路径（如 ops[0].patch）。
func validateCloudAgentSchemaValue(field string, schema map[string]any, value any, depth int) error {
	if schema == nil || depth > cloudAgentSchemaMaxDepth {
		return nil
	}
	path := field
	if path == "" {
		path = "arguments"
	}
	if expected, ok := schema["type"].(string); ok && expected != "" {
		switch expected {
		case "object":
			object, ok := value.(map[string]any)
			if !ok {
				return cloudAgentSchemaTypeError(path, "对象")
			}
			if err := validateCloudAgentSchemaObject(path, schema, object, depth); err != nil {
				return err
			}
		case "array":
			items, ok := value.([]any)
			if !ok {
				return cloudAgentSchemaTypeError(path, "数组")
			}
			if err := validateCloudAgentSchemaArray(path, schema, items, depth); err != nil {
				return err
			}
		case "string":
			text, ok := value.(string)
			if !ok {
				return cloudAgentSchemaTypeError(path, "字符串")
			}
			if err := validateCloudAgentSchemaString(path, schema, text); err != nil {
				return err
			}
		case "integer":
			number, ok := cloudAgentSchemaNumber(value)
			if !ok || number != float64(int64(number)) {
				return cloudAgentSchemaTypeError(path, "整数")
			}
			if err := validateCloudAgentSchemaNumberBounds(path, schema, number); err != nil {
				return err
			}
		case "number":
			number, ok := cloudAgentSchemaNumber(value)
			if !ok {
				return cloudAgentSchemaTypeError(path, "数字")
			}
			if err := validateCloudAgentSchemaNumberBounds(path, schema, number); err != nil {
				return err
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return cloudAgentSchemaTypeError(path, "布尔值")
			}
		}
	} else {
		// 没有声明 type 的分支（oneOf 的子 schema）也要能递归检查嵌套结构。
		if object, ok := value.(map[string]any); ok {
			if err := validateCloudAgentSchemaObject(path, schema, object, depth); err != nil {
				return err
			}
		}
	}
	if err := validateCloudAgentSchemaEnum(path, schema, value); err != nil {
		return err
	}
	return validateCloudAgentSchemaOneOf(path, schema, value, depth)
}

func validateCloudAgentSchemaObject(path string, schema map[string]any, object map[string]any, depth int) error {
	for _, name := range cloudAgentSchemaStrings(schema["required"]) {
		value, exists := object[name]
		if !exists || value == nil {
			return cloudAgentFieldError(cloudAgentSchemaPath(path, name), "required", fmt.Sprintf("工具参数缺少必填字段 %s；本次调用未执行，请按 parameters 补齐后重试", cloudAgentSchemaPath(path, name)))
		}
	}
	if minimum, ok := cloudAgentSchemaNumber(schema["minProperties"]); ok && float64(len(object)) < minimum {
		return cloudAgentFieldError(path, "item_count", fmt.Sprintf("工具参数 %s 至少要有一个字段", path))
	}
	properties, _ := schema["properties"].(map[string]any)
	for name, value := range object {
		child, declared := properties[name]
		if !declared {
			if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
				return cloudAgentFieldError(cloudAgentSchemaPath(path, name), "unknown_field", fmt.Sprintf("工具参数出现未定义字段 %s；本次调用未执行，请只使用 parameters 里列出的字段", cloudAgentSchemaPath(path, name)))
			}
			continue
		}
		childSchema, _ := child.(map[string]any)
		if err := validateCloudAgentSchemaValue(cloudAgentSchemaPath(path, name), childSchema, value, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateCloudAgentSchemaArray(path string, schema map[string]any, items []any, depth int) error {
	if minimum, ok := cloudAgentSchemaNumber(schema["minItems"]); ok && float64(len(items)) < minimum {
		return cloudAgentFieldError(path, "item_count", fmt.Sprintf("工具参数 %s 至少需要 %d 项", path, int(minimum)))
	}
	if maximum, ok := cloudAgentSchemaNumber(schema["maxItems"]); ok && float64(len(items)) > maximum {
		return cloudAgentFieldError(path, "item_count", fmt.Sprintf("工具参数 %s 最多 %d 项，请分批提交", path, int(maximum)))
	}
	itemSchema, _ := schema["items"].(map[string]any)
	if itemSchema == nil {
		return nil
	}
	for index, item := range items {
		if err := validateCloudAgentSchemaValue(fmt.Sprintf("%s[%d]", path, index), itemSchema, item, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateCloudAgentSchemaString(path string, schema map[string]any, text string) error {
	count := float64(utf8.RuneCountInString(text))
	if minimum, ok := cloudAgentSchemaNumber(schema["minLength"]); ok && count < minimum {
		return cloudAgentFieldError(path, "invalid_value", fmt.Sprintf("工具参数 %s 至少 %d 个字符", path, int(minimum)))
	}
	if maximum, ok := cloudAgentSchemaNumber(schema["maxLength"]); ok && count > maximum {
		return cloudAgentFieldError(path, "invalid_value", fmt.Sprintf("工具参数 %s 最多 %d 个字符，请缩短后重试", path, int(maximum)))
	}
	return nil
}

func validateCloudAgentSchemaNumberBounds(path string, schema map[string]any, value float64) error {
	if minimum, ok := cloudAgentSchemaNumber(schema["minimum"]); ok && value < minimum {
		return cloudAgentFieldError(path, "invalid_value", fmt.Sprintf("工具参数 %s 不能小于 %s", path, cloudAgentFormatSchemaNumber(minimum)))
	}
	if maximum, ok := cloudAgentSchemaNumber(schema["maximum"]); ok && value > maximum {
		return cloudAgentFieldError(path, "invalid_value", fmt.Sprintf("工具参数 %s 不能大于 %s", path, cloudAgentFormatSchemaNumber(maximum)))
	}
	return nil
}

func validateCloudAgentSchemaEnum(path string, schema map[string]any, value any) error {
	values, ok := schema["enum"].([]any)
	if !ok || len(values) == 0 {
		if constant, declared := schema["const"]; declared {
			if !cloudAgentSchemaEqual(constant, value) {
				return cloudAgentFieldError(path, "invalid_value", fmt.Sprintf("工具参数 %s 必须是 %v", path, constant))
			}
		}
		return nil
	}
	for _, candidate := range values {
		if cloudAgentSchemaEqual(candidate, value) {
			return nil
		}
	}
	return cloudAgentFieldError(path, "invalid_value", fmt.Sprintf("工具参数 %s 只能是 %s 之一；本次调用未执行", path, cloudAgentSchemaChoices(values)))
}

// validateCloudAgentSchemaOneOf 支持我们 schema 里用到的条件必填形式：
// oneOf: [{properties:{type:{const:"add_node"}}, required:["nodeType"]}, …]
// 分支之间只判断"必填字段与常量是否成立"，不做分支级的未知字段检查（外层已经查过）。
func validateCloudAgentSchemaOneOf(path string, schema map[string]any, value any, depth int) error {
	branches, ok := schema["oneOf"].([]map[string]any)
	if !ok {
		if generic, ok := schema["oneOf"].([]any); ok {
			for _, item := range generic {
				if branch, ok := item.(map[string]any); ok {
					branches = append(branches, branch)
				}
			}
		}
	}
	if len(branches) == 0 {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, branch := range branches {
		if cloudAgentSchemaBranchMatches(branch, object, depth) {
			return nil
		}
	}
	return cloudAgentFieldError(path, "invalid_value", fmt.Sprintf("工具参数 %s 不满足任何一种允许的组合；本次调用未执行，请按 parameters 检查字段组合", path))
}

func cloudAgentSchemaBranchMatches(branch map[string]any, object map[string]any, depth int) bool {
	if depth > cloudAgentSchemaMaxDepth {
		return true
	}
	for name, value := range object {
		properties, _ := branch["properties"].(map[string]any)
		child, declared := properties[name].(map[string]any)
		if !declared {
			continue
		}
		if constant, ok := child["const"]; ok && !cloudAgentSchemaEqual(constant, value) {
			return false
		}
	}
	for _, name := range cloudAgentSchemaStrings(branch["required"]) {
		value, exists := object[name]
		if !exists || value == nil {
			return false
		}
	}
	return true
}

func cloudAgentSchemaTypeError(path, expected string) error {
	return cloudAgentFieldError(path, "type_mismatch", fmt.Sprintf("工具参数 %s 必须是%s；本次调用未执行，请按 parameters 修正后重试", path, expected))
}

func cloudAgentSchemaPath(parent, name string) string {
	if parent == "" || parent == "arguments" {
		return name
	}
	return parent + "." + name
}

func cloudAgentSchemaStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		names := make([]string, 0, len(typed))
		for _, item := range typed {
			if name, ok := item.(string); ok {
				names = append(names, name)
			}
		}
		return names
	default:
		return nil
	}
}

func cloudAgentSchemaNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func cloudAgentFormatSchemaNumber(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%g", value)
}

func cloudAgentSchemaChoices(values []any) string {
	choices := make([]string, 0, len(values))
	for _, value := range values {
		choices = append(choices, fmt.Sprintf("%v", value))
	}
	return strings.Join(choices, " / ")
}

// cloudAgentSchemaEqual 比较 schema 常量与实参；数字按数值比较，避免 1 与 1.0 被判成不同。
func cloudAgentSchemaEqual(expected, actual any) bool {
	if left, ok := cloudAgentSchemaNumber(expected); ok {
		if right, ok := cloudAgentSchemaNumber(actual); ok {
			return left == right
		}
		return false
	}
	return fmt.Sprintf("%v", expected) == fmt.Sprintf("%v", actual)
}
