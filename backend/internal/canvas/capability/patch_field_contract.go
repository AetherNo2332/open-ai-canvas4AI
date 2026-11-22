package capability

import (
	"fmt"
	"math"
	"strings"
)

func (field PatchField) validateValue(value any, key string, depth int) error {
	if value == nil && (field.Nullable || field.Resettable) {
		return nil
	}
	if len(field.AnyOf) > 0 {
		for _, choice := range field.AnyOf {
			if choice.validateValue(value, key, depth+1) == nil {
				return nil
			}
		}
		return fmt.Errorf("字段 %s 的值不符合允许的类型", key)
	}
	if depth > 64 {
		return fmt.Errorf("字段 %s 嵌套过深", key)
	}
	var err error
	switch field.Kind {
	case "string":
		err = field.validateStringValue(value, key)
	case "number":
		err = field.validateNumberValue(value, key)
	case "boolean":
		if _, ok := value.(bool); !ok {
			err = fmt.Errorf("字段 %s 必须是布尔值", key)
		}
	case patchKindObject:
		err = field.validateObjectValue(value, key, depth)
	case patchKindArray:
		err = field.validateArrayValue(value, key, depth)
	default:
		err = fmt.Errorf("字段 %s 的类型契约无效", key)
	}
	if err != nil {
		return err
	}
	return validateFieldFormat(field.Format, value)
}

func (field PatchField) validateStringValue(value any, key string) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("字段 %s 必须是字符串", key)
	}
	if field.MaxRunes > 0 && len([]rune(text)) > field.MaxRunes {
		return fmt.Errorf("字段 %s 超出长度限制", key)
	}
	if len(field.Enum) > 0 && !containsString(field.Enum, text) {
		return fmt.Errorf("字段 %s 不是允许的选项", key)
	}
	return nil
}

func (field PatchField) validateNumberValue(value any, key string) error {
	number, ok := value.(float64)
	if !ok {
		return fmt.Errorf("字段 %s 必须是数字", key)
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return fmt.Errorf("字段 %s 不是有效数字", key)
	}
	if field.Limit > 0 && math.Abs(number) > field.Limit {
		return fmt.Errorf("字段 %s 超出允许范围（±%g）", key, field.Limit)
	}
	if (field.Min != nil && number < *field.Min) || (field.Max != nil && number > *field.Max) {
		return fmt.Errorf("字段 %s 超出允许范围", key)
	}
	if field.Integer && number != math.Trunc(number) {
		return fmt.Errorf("字段 %s 必须是整数", key)
	}
	return nil
}

func (field PatchField) validateObjectValue(value any, key string, depth int) error {
	object, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("字段 %s 必须是对象", key)
	}
	for _, required := range field.Required {
		if _, exists := object[required]; !exists {
			return fmt.Errorf("字段 %s.%s 不能为空", key, required)
		}
	}
	for name, item := range object {
		property, exists := field.Properties[name]
		if !exists && field.MapValues != nil {
			if !validDictionaryKey(name) {
				return fmt.Errorf("字段 %s 的字典键无效", key)
			}
			property = *field.MapValues
			exists = true
		}
		if !exists {
			return fmt.Errorf("字段 %s 不支持子字段 %s", key, name)
		}
		if err := property.validateValue(item, key+"."+name, depth+1); err != nil {
			return err
		}
	}
	if field.MaxProperties > 0 && len(object) > field.MaxProperties {
		return fmt.Errorf("字段 %s 超出属性数量限制", key)
	}
	return nil
}

func (field PatchField) validateArrayValue(value any, key string, depth int) error {
	array, ok := value.([]any)
	if !ok {
		return fmt.Errorf("字段 %s 必须是数组", key)
	}
	if field.MaxItems > 0 && len(array) > field.MaxItems {
		return fmt.Errorf("字段 %s 超出元素数量限制", key)
	}
	if field.Items == nil {
		return fmt.Errorf("字段 %s 缺少元素契约", key)
	}
	for index, item := range array {
		if err := field.Items.validateValue(item, fmt.Sprintf("%s[%d]", key, index), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// JSONSchema publishes the same closed, typed contract enforced by ValidatePatch.
func (field PatchField) JSONSchema() map[string]any {
	if len(field.AnyOf) > 0 {
		choices := make([]any, 0, len(field.AnyOf))
		for _, choice := range field.AnyOf {
			choices = append(choices, choice.JSONSchema())
		}
		return map[string]any{"anyOf": choices}
	}
	schema := map[string]any{"type": field.Kind}
	field.addScalarSchema(schema)
	if field.Kind == patchKindObject {
		field.addObjectSchema(schema)
	}
	if field.Kind == patchKindArray {
		field.addArraySchema(schema)
	}
	return schema
}

func (field PatchField) addScalarSchema(schema map[string]any) {
	if field.Nullable || field.Resettable {
		schema["type"] = []string{field.Kind, "null"}
	}
	if field.MaxRunes > 0 {
		schema["maxLength"] = field.MaxRunes
	}
	if len(field.Enum) > 0 {
		if field.Nullable || field.Resettable {
			options := make([]any, 0, len(field.Enum)+1)
			for _, item := range field.Enum {
				options = append(options, item)
			}
			schema["enum"] = append(options, nil)
		} else {
			schema["enum"] = append([]string(nil), field.Enum...)
		}
	}
	if field.Description != "" {
		schema["description"] = field.Description
	}
	if field.Resettable {
		schema["x-canvas-resettable"] = true
		schema["description"] = strings.TrimSpace(field.Description + " null 清除该可选字段；正文与提示词恢复为空字符串。省略字段保持原值。")
	}
	if field.Format != "" {
		schema["x-canvas-format"] = field.Format
	}
	if field.Min != nil {
		schema["minimum"] = *field.Min
	} else if field.Limit > 0 {
		schema["minimum"] = -field.Limit
	}
	if field.Max != nil {
		schema["maximum"] = *field.Max
	} else if field.Limit > 0 {
		schema["maximum"] = field.Limit
	}
	if field.Integer {
		schema["multipleOf"] = 1
	}
}

func (field PatchField) addObjectSchema(schema map[string]any) {
	properties := map[string]any{}
	for key, value := range field.Properties {
		properties[key] = value.JSONSchema()
	}
	schema["properties"], schema["additionalProperties"] = properties, false
	if field.MapValues != nil {
		schema["additionalProperties"] = field.MapValues.JSONSchema()
		schema["propertyNames"] = map[string]any{"minLength": 1, "maxLength": 240, "not": map[string]any{"enum": []string{"__proto__", "constructor", "prototype"}}, "pattern": "^[^\\u0000-\\u001F\\u007F]+$"}
	}
	if field.MaxProperties > 0 {
		schema["maxProperties"] = field.MaxProperties
	}
	if len(field.Required) > 0 {
		schema["required"] = append([]string(nil), field.Required...)
	}
}

func (field PatchField) addArraySchema(schema map[string]any) {
	if field.Items == nil {
		return
	}
	schema["items"] = field.Items.JSONSchema()
	if field.MaxItems > 0 {
		schema["maxItems"] = field.MaxItems
	}
}

func validDictionaryKey(key string) bool {
	if len([]rune(key)) < 1 || len([]rune(key)) > 240 || key == "__proto__" || key == "constructor" || key == "prototype" {
		return false
	}
	for _, r := range key {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}

func (field PatchField) ValidateValue(value any) error {
	return field.validateValue(value, "value", 0)
}
