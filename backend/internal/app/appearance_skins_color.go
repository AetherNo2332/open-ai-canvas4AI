package app

import (
	"math"
	"strconv"
	"strings"
)

// 与 web/src/lib/skin-themes.ts 保持一致的颜色度量：服务端据此独立判断
// 「单一品牌主色 + 中性阶」是否被破坏，前端只做提前提示。
const (
	appearanceSkinMaxHueDistance = 30
	appearanceSkinNeutralChroma  = 0.06
	appearanceSkinMinContrast    = 4.5
)

type appearanceSkinColor struct {
	red, green, blue float64
}

func parseAppearanceSkinColor(value string) (appearanceSkinColor, bool) {
	hex := strings.TrimPrefix(strings.TrimSpace(value), "#")
	if len(hex) == 8 {
		hex = hex[:6]
	}
	if len(hex) != 6 {
		return appearanceSkinColor{}, false
	}
	var channels [3]float64
	for index := 0; index < 3; index++ {
		parsed, err := strconv.ParseUint(hex[index*2:index*2+2], 16, 8)
		if err != nil {
			return appearanceSkinColor{}, false
		}
		channels[index] = float64(parsed) / 255
	}
	return appearanceSkinColor{red: channels[0], green: channels[1], blue: channels[2]}, true
}

func linearizeAppearanceChannel(channel float64) float64 {
	if channel <= 0.04045 {
		return channel / 12.92
	}
	return math.Pow((channel+0.055)/1.055, 2.4)
}

func appearanceSkinLuminance(color appearanceSkinColor) float64 {
	red := linearizeAppearanceChannel(color.red)
	green := linearizeAppearanceChannel(color.green)
	blue := linearizeAppearanceChannel(color.blue)
	return 0.2126*red + 0.7152*green + 0.0722*blue
}

func appearanceSkinContrast(first, second appearanceSkinColor) float64 {
	lighter := appearanceSkinLuminance(first)
	darker := appearanceSkinLuminance(second)
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

func appearanceSkinChroma(color appearanceSkinColor) float64 {
	maximum := math.Max(color.red, math.Max(color.green, color.blue))
	minimum := math.Min(color.red, math.Min(color.green, color.blue))
	return maximum - minimum
}

func appearanceSkinHue(color appearanceSkinColor) (float64, bool) {
	maximum := math.Max(color.red, math.Max(color.green, color.blue))
	minimum := math.Min(color.red, math.Min(color.green, color.blue))
	delta := maximum - minimum
	if delta < 0.02 {
		return 0, false
	}
	var raw float64
	switch maximum {
	case color.red:
		raw = math.Mod((color.green-color.blue)/delta, 6)
	case color.green:
		raw = (color.blue-color.red)/delta + 2
	default:
		raw = (color.red-color.green)/delta + 4
	}
	hue := raw * 60
	if hue < 0 {
		hue += 360
	}
	return math.Mod(hue, 360), true
}

func appearanceSkinHueDistance(first, second float64) float64 {
	delta := math.Mod(math.Abs(first-second), 360)
	if delta > 180 {
		return 360 - delta
	}
	return delta
}

// appearanceSkinModeFieldLabels 按 JSON 字段名给出可读名称，用于拒绝原因。
var appearanceSkinModeFieldLabels = []struct{ name, label string }{
	{"canvas", "页面背景"}, {"surface", "基础表面"}, {"surfaceSubtle", "次级表面"}, {"surfaceRaised", "强调表面"},
	{"overlay", "浮层表面"}, {"text", "主要文字"}, {"textMuted", "次要文字"}, {"border", "分隔与边界"},
	{"control", "控件背景"}, {"controlHover", "控件悬停"}, {"controlActive", "控件按下"}, {"controlBorder", "控件边框"},
	{"controlFocus", "键盘焦点"}, {"controlDisabledBackground", "禁用背景"}, {"controlDisabledForeground", "禁用前景"},
	{"switchChecked", "开关开启"}, {"switchCheckedHover", "开启悬停"}, {"switchCheckedHandle", "开启滑块"},
	{"switchUnchecked", "开关关闭"}, {"switchUncheckedHover", "关闭悬停"}, {"switchUncheckedHandle", "关闭滑块"},
	{"primary", "主操作"}, {"primaryHover", "主操作悬停"}, {"primaryActive", "主操作按下"}, {"primaryForeground", "主操作前景"},
	{"selected", "选中背景"}, {"selectedHover", "选中悬停"}, {"selectedActive", "选中按下"}, {"selectedForeground", "选中前景"},
	{"icon", "普通图标"}, {"iconMuted", "弱化图标"}, {"iconActive", "激活图标"},
	{"success", "成功"}, {"warning", "警告"}, {"danger", "危险"}, {"dangerHover", "危险悬停"}, {"dangerActive", "危险按下"},
	{"dangerForeground", "危险前景"}, {"info", "信息"},
	{"workspace", "创作工作区"}, {"workspaceGrid", "画布网格"}, {"adminBackground", "后台底层"}, {"adminSurface", "后台卡片"},
	{"adminSubtle", "后台次级层"}, {"adminStrong", "后台强调层"}, {"authBackground", "登录页背景"}, {"authPanel", "登录表单区"},
	{"authCard", "登录卡片"}, {"authAccent", "登录页强调"}, {"authMuted", "登录页次要文字"},
}

// appearanceSkinSignalFields 是状态色：它们可以出现主色之外的色相，但不参与装饰强调。
var appearanceSkinSignalFields = map[string]struct{}{
	"success": {}, "warning": {}, "danger": {}, "dangerHover": {}, "dangerActive": {}, "dangerForeground": {}, "info": {},
}
