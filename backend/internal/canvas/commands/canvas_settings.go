package commands

import (
	"math"
	"regexp"
)

var canvasColor = regexp.MustCompile(`^#?[a-fA-F0-9]{3}([a-fA-F0-9]{3})?$`)

func choice(value any, choices ...string) bool {
	for _, candidate := range choices {
		if value == candidate {
			return true
		}
	}
	return false
}
func editCanvasSettings(doc Document, patch map[string]any) error {
	if len(patch) == 0 {
		return BadAuthRequest("画布设置不能为空")
	}
	for key, value := range patch {
		switch key {
		case "backgroundMode":
			if !choice(value, "blank", "dots", "lines") {
				return BadAuthRequest("画布背景模式无效")
			}
		case "showImageInfo":
			if _, ok := value.(bool); !ok {
				return BadAuthRequest("图片信息开关无效")
			}
		case "starterMode":
			if !choice(value, "freeform", "guided") {
				return BadAuthRequest("画布起始模式无效")
			}
		case "appearance":
			if err := validateCanvasAppearance(value); err != nil {
				return err
			}
		default:
			return BadAuthRequest("该字段不属于共享画布设置")
		}
	}
	for key, value := range patch {
		if update, ok := value.(map[string]any); ok {
			old, _ := doc[key].(map[string]any)
			doc[key] = mergeCanvasSettings(old, update)
		} else {
			doc[key] = value
		}
	}
	return nil
}
func mergeCanvasSettings(before, patch map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range before {
		out[key] = value
	}
	for key, value := range patch {
		if value == nil {
			// Validation allows null only for optional appearance fields.
			delete(out, key)
			continue
		}
		if child, ok := value.(map[string]any); ok {
			old, _ := before[key].(map[string]any)
			out[key] = mergeCanvasSettings(old, child)
		} else {
			out[key] = value
		}
	}
	return out
}
func validateCanvasAppearance(value any) error {
	appearance, ok := value.(map[string]any)
	if !ok || len(appearance) == 0 {
		return BadAuthRequest("画布外观无效")
	}
	return validateCanvasAppearanceSelection(appearance, true)
}
func validateCanvasAppearanceSelection(selection map[string]any, root bool) error {
	for key, value := range selection {
		switch key {
		case "mode":
			if !choice(value, "preset", "custom") && !(root && choice(value, "light", "dark")) {
				return BadAuthRequest("画布颜色模式无效")
			}
		case "custom":
			if value != nil {
				if err := validateCanvasAppearanceValues(value); err != nil {
					return err
				}
			}
		case "preset":
			if value != nil {
				if err := validateCanvasAppearancePreset(value); err != nil {
					return err
				}
			}
		case "themeMemory":
			if !root {
				return BadAuthRequest("画布外观字段无效")
			}
			if value != nil {
				if err := validateCanvasAppearanceThemeMemory(value); err != nil {
					return err
				}
			}
		default:
			return BadAuthRequest("画布外观字段无效")
		}
	}
	return nil
}
func validateCanvasAppearancePreset(value any) error {
	preset, ok := value.(map[string]any)
	if !ok {
		return BadAuthRequest("画布颜色预设无效")
	}
	for field, value := range preset {
		switch field {
		case "id":
			if !choice(value, "light", "pink", "sand", "mint", "dark", "slate") {
				return BadAuthRequest("画布颜色预设无效")
			}
		case "values":
			if err := validateCanvasAppearanceValues(value); err != nil {
				return err
			}
		default:
			return BadAuthRequest("画布颜色预设字段无效")
		}
	}
	return nil
}
func validateCanvasAppearanceThemeMemory(value any) error {
	memory, ok := value.(map[string]any)
	if !ok {
		return BadAuthRequest("画布主题记忆无效")
	}
	for theme, value := range memory {
		if !choice(theme, "light", "dark") {
			return BadAuthRequest("画布主题记忆字段无效")
		}
		if value == nil {
			continue
		}
		selection, ok := value.(map[string]any)
		if !ok {
			return BadAuthRequest("画布主题记忆无效")
		}
		if err := validateCanvasAppearanceSelection(selection, false); err != nil {
			return err
		}
	}
	return nil
}
func validateCanvasAppearanceValues(value any) error {
	values, ok := value.(map[string]any)
	if !ok {
		return BadAuthRequest("自定义画布外观无效")
	}
	for field, value := range values {
		switch field {
		case "baseTheme":
			if !choice(value, "light", "dark") {
				return BadAuthRequest("画布基础主题无效")
			}
		case "backgroundColor", "gridColor":
			color, ok := value.(string)
			if !ok || !canvasColor.MatchString(color) {
				return BadAuthRequest("画布颜色无效")
			}
		case "backgroundBrightness", "gridOpacity":
			number, ok := value.(float64)
			if !ok || math.IsNaN(number) || math.IsInf(number, 0) || field == "backgroundBrightness" && (number < -30 || number > 30) || field == "gridOpacity" && (number < 0 || number > 100) {
				return BadAuthRequest("画布颜色数值无效")
			}
		default:
			return BadAuthRequest("自定义画布外观字段无效")
		}
	}
	return nil
}
