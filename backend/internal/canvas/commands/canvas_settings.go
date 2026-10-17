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
	for key, value := range appearance {
		switch key {
		case "mode":
			if !choice(value, "light", "dark", "custom") {
				return BadAuthRequest("画布颜色模式无效")
			}
		case "custom":
			custom, ok := value.(map[string]any)
			if !ok {
				return BadAuthRequest("自定义画布外观无效")
			}
			for field, value := range custom {
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
		default:
			return BadAuthRequest("画布外观字段无效")
		}
	}
	return nil
}
