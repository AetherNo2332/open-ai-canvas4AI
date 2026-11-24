package commands

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"yingce/backend/internal/assets"
)

func previsValueError(rule string) error {
	return BadAuthRequest("预演字段类型或范围无效：" + rule)
}

func (p *planner) validatePrevisValue(value any, rule string) error {
	if fields, ok := previsFields[rule]; ok {
		return p.validatePrevisRecord(value, rule, fields)
	}
	if strings.HasPrefix(rule, "array:") {
		return p.validatePrevisList(value, strings.TrimPrefix(rule, "array:"), 0, 10000)
	}
	if strings.HasPrefix(rule, "list:") {
		parts := strings.SplitN(rule, ":", 4)
		low, _ := strconv.Atoi(parts[1])
		high, _ := strconv.Atoi(parts[2])
		return p.validatePrevisList(value, parts[3], low, high)
	}
	if strings.HasPrefix(rule, "enum:") {
		textValue, ok := value.(string)
		if ok && slices.Contains(strings.Split(strings.TrimPrefix(rule, "enum:"), ","), textValue) {
			return nil
		}
		return previsValueError(rule)
	}
	if strings.HasPrefix(rule, "range:") || strings.HasPrefix(rule, "int:") {
		return validatePrevisNumber(value, rule)
	}
	return p.validatePrevisScalar(value, rule)
}

func (p *planner) validatePrevisRecord(value any, rule string, fields map[string]string) error {
	object, ok := value.(map[string]any)
	if !ok {
		return previsValueError(rule)
	}
	if err := validatePrevisRequired(object, rule); err != nil {
		return err
	}
	for key, value := range object {
		child, ok := fields[key]
		if !ok {
			return BadAuthRequest("预演字段不可编辑：" + key)
		}
		if err := p.validatePrevisValue(value, child); err != nil {
			return err
		}
	}
	return nil
}

func (p *planner) validatePrevisList(value any, rule string, low, high int) error {
	values, ok := value.([]any)
	if !ok || len(values) < low || len(values) > high {
		return previsValueError("array:" + rule)
	}
	seen := map[string]bool{}
	for _, value := range values {
		if err := p.validatePrevisValue(value, rule); err != nil {
			return err
		}
		if item, ok := value.(map[string]any); ok {
			id := text(item["id"])
			if id == "" {
				id = text(item["bone"])
			}
			if id != "" && seen[id] {
				return BadAuthRequest("预演数组 ID 重复")
			}
			seen[id] = true
		}
	}
	return nil
}

func validatePrevisNumber(value any, rule string) error {
	parts := strings.Split(rule, ":")
	low, _ := strconv.ParseFloat(parts[1], 64)
	high, _ := strconv.ParseFloat(parts[2], 64)
	number, ok := value.(float64)
	if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < low || number > high {
		return previsValueError(rule)
	}
	if parts[0] == "int" && number != math.Trunc(number) {
		return previsValueError(rule)
	}
	return nil
}

func (p *planner) validatePrevisScalar(value any, rule string) error {
	switch rule {
	case "enumNumber:24,25,30":
		if value != float64(24) && value != float64(25) && value != float64(30) {
			return previsValueError(rule)
		}
	case "text", "prompt", "id", "locator", "modelLocator":
		return validatePrevisString(value, rule)
	case "bool":
		if _, ok := value.(bool); !ok {
			return previsValueError(rule)
		}
	case "color":
		return p.canvasPrevisValidateColor(text(value), "预演颜色")
	case "pose":
		return p.canvasPrevisValidatePose(text(value))
	case "bone":
		if !previsBone(text(value)) {
			return previsValueError(rule)
		}
	case "vec3", "quat":
		return validatePrevisVector(value, rule)
	case "boneQuats", "boneNames":
		return p.validatePrevisBones(value, rule)
	case "groundSize":
		if value != float64(1000) && value != float64(2000) && value != float64(4000) {
			return previsValueError(rule)
		}
	case "params":
		if _, ok := value.(map[string]any); !ok {
			return previsValueError(rule)
		}
	default:
		return validatePrevisBoundedText(value, rule)
	}
	return nil
}

func validatePrevisString(value any, rule string) error {
	str, ok := value.(string)
	if !ok || len(str) > 20000 {
		return previsValueError(rule)
	}
	if rule == "id" && str != "" {
		return validateCanvasID(str, "预演引用", 128)
	}
	if rule == "modelLocator" && str == previsDefaultActorURL {
		return nil
	}
	if (rule == "locator" || rule == "modelLocator") && str != "" && assets.ResourceID(str) == "" {
		return failure("resource_not_owned", "预演只接受已上传资源")
	}
	return nil
}

func validatePrevisBoundedText(value any, rule string) error {
	if !strings.HasPrefix(rule, "textRange:") {
		return previsValueError(rule)
	}
	parts := strings.Split(rule, ":")
	low, _ := strconv.Atoi(parts[1])
	high, _ := strconv.Atoi(parts[2])
	str, ok := value.(string)
	if !ok || !utf8.ValidString(str) || utf8.RuneCountInString(strings.TrimSpace(str)) < low || utf8.RuneCountInString(str) > high {
		return previsValueError(rule)
	}
	return nil
}

func validatePrevisVector(value any, rule string) error {
	values, ok := value.([]any)
	size := 3
	if rule == "quat" {
		size = 4
	}
	if !ok || len(values) != size {
		return previsValueError(rule)
	}
	norm := 0.0
	for _, value := range values {
		if err := validatePrevisNumber(value, "range:-1000000:1000000"); err != nil {
			return err
		}
		number := value.(float64)
		norm += number * number
	}
	if rule == "quat" && math.Abs(norm-1) > 0.01 {
		return previsValueError(rule)
	}
	return nil
}

func (p *planner) validatePrevisBones(value any, rule string) error {
	bones, ok := value.(map[string]any)
	if !ok {
		return previsValueError(rule)
	}
	child := "quat"
	if rule == "boneNames" {
		child = "text"
	}
	for bone, value := range bones {
		if !previsBone(bone) {
			return previsValueError(rule)
		}
		if err := p.validatePrevisValue(value, child); err != nil {
			return err
		}
	}
	return nil
}
