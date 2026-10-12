package app

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxAppearanceSkinThemes = 16

var (
	appearanceSkinIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)
	appearanceColorPattern  = regexp.MustCompile(`^#[0-9a-fA-F]{6}(?:[0-9a-fA-F]{2})?$`)
)

// AppearanceSkinModeTokens is deliberately an allowlist instead of arbitrary CSS.
// Every value is validated as a six- or eight-digit hex color before it is exposed
// by the public appearance endpoint.
type AppearanceSkinModeTokens struct {
	Canvas                    string `json:"canvas"`
	Surface                   string `json:"surface"`
	SurfaceSubtle             string `json:"surfaceSubtle"`
	SurfaceRaised             string `json:"surfaceRaised"`
	Overlay                   string `json:"overlay"`
	Text                      string `json:"text"`
	TextMuted                 string `json:"textMuted"`
	Border                    string `json:"border"`
	Control                   string `json:"control"`
	ControlHover              string `json:"controlHover"`
	ControlActive             string `json:"controlActive"`
	ControlBorder             string `json:"controlBorder"`
	ControlFocus              string `json:"controlFocus"`
	ControlDisabledBackground string `json:"controlDisabledBackground"`
	ControlDisabledForeground string `json:"controlDisabledForeground"`
	SwitchChecked             string `json:"switchChecked"`
	SwitchCheckedHover        string `json:"switchCheckedHover"`
	SwitchCheckedHandle       string `json:"switchCheckedHandle"`
	SwitchUnchecked           string `json:"switchUnchecked"`
	SwitchUncheckedHover      string `json:"switchUncheckedHover"`
	SwitchUncheckedHandle     string `json:"switchUncheckedHandle"`
	Primary                   string `json:"primary"`
	PrimaryHover              string `json:"primaryHover"`
	PrimaryActive             string `json:"primaryActive"`
	PrimaryForeground         string `json:"primaryForeground"`
	Selected                  string `json:"selected"`
	SelectedHover             string `json:"selectedHover"`
	SelectedActive            string `json:"selectedActive"`
	SelectedForeground        string `json:"selectedForeground"`
	Icon                      string `json:"icon"`
	IconMuted                 string `json:"iconMuted"`
	IconActive                string `json:"iconActive"`
	Success                   string `json:"success"`
	Warning                   string `json:"warning"`
	Danger                    string `json:"danger"`
	DangerHover               string `json:"dangerHover"`
	DangerActive              string `json:"dangerActive"`
	DangerForeground          string `json:"dangerForeground"`
	Info                      string `json:"info"`
	Workspace                 string `json:"workspace"`
	WorkspaceGrid             string `json:"workspaceGrid"`
	AdminBackground           string `json:"adminBackground"`
	AdminSurface              string `json:"adminSurface"`
	AdminSubtle               string `json:"adminSubtle"`
	AdminStrong               string `json:"adminStrong"`
	AuthBackground            string `json:"authBackground"`
	AuthPanel                 string `json:"authPanel"`
	AuthCard                  string `json:"authCard"`
	AuthAccent                string `json:"authAccent"`
	AuthMuted                 string `json:"authMuted"`
}

type AppearanceSkinComponentTokens struct {
	ButtonRadius       int    `json:"buttonRadius"`
	InputRadius        int    `json:"inputRadius"`
	CardRadius         int    `json:"cardRadius"`
	OverlayRadius      int    `json:"overlayRadius"`
	MenuRadius         int    `json:"menuRadius"`
	CheckboxRadius     int    `json:"checkboxRadius"`
	ControlHeight      int    `json:"controlHeight"`
	ControlHeightSmall int    `json:"controlHeightSmall"`
	ControlHeightLarge int    `json:"controlHeightLarge"`
	BorderWidth        int    `json:"borderWidth"`
	FocusRingWidth     int    `json:"focusRingWidth"`
	IconSize           int    `json:"iconSize"`
	ButtonFontWeight   int    `json:"buttonFontWeight"`
	HoverLift          int    `json:"hoverLift"`
	MotionFast         int    `json:"motionFast"`
	MotionNormal       int    `json:"motionNormal"`
	ShadowStyle        string `json:"shadowStyle"`
}

type AppearanceSkinTokens struct {
	Light      AppearanceSkinModeTokens      `json:"light"`
	Dark       AppearanceSkinModeTokens      `json:"dark"`
	Components AppearanceSkinComponentTokens `json:"components"`
	Buttons    AppearanceSkinButtons         `json:"buttons"`
}

type AppearanceSkinButtons struct {
	Light AppearanceSkinButtonFill `json:"light"`
	Dark  AppearanceSkinButtonFill `json:"dark"`
}

type AppearanceSkinButtonFill struct {
	Mode        string `json:"mode"`
	Angle       int    `json:"angle"`
	Start       string `json:"start"`
	End         string `json:"end"`
	HoverStart  string `json:"hoverStart"`
	HoverEnd    string `json:"hoverEnd"`
	ActiveStart string `json:"activeStart"`
	ActiveEnd   string `json:"activeEnd"`
	Foreground  string `json:"foreground"`
}

func defaultAppearanceSkinButtons(gradient bool) AppearanceSkinButtons {
	fill := AppearanceSkinButtonFill{
		Mode: "solid", Angle: 115, Start: "#6554df", End: "#386fbc",
		HoverStart: "#5744cf", HoverEnd: "#356bbb", ActiveStart: "#4938b8", ActiveEnd: "#2c5da5", Foreground: "#ffffff",
	}
	if gradient {
		fill.Mode = "gradient"
	}
	return AppearanceSkinButtons{Light: fill, Dark: fill}
}

type AppearanceSkinTheme struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Locked      bool                 `json:"locked"`
	Tokens      AppearanceSkinTokens `json:"tokens"`
}

// 瑞士预设：两套主题都只由「中性阶 + 一个品牌主色」构成，直角、实心按钮、
// 无阴影、无悬停抬升、150ms 动效。系统默认主题沿用 ID classic，
// 保证已保存的 skinId 与既有数据继续解析。
func defaultAppearanceSkinThemes() []AppearanceSkinTheme {
	return []AppearanceSkinTheme{defaultClassicAppearanceSkin(), defaultSwissInkSkin()}
}

func swissAppearanceSkinComponents() AppearanceSkinComponentTokens {
	components := appearanceSkinComponentPreset(0, 0, 0, 0, 0, 0)
	components.HoverLift = 0
	components.MotionFast = 150
	components.MotionNormal = 150
	components.ShadowStyle = "none"
	return components
}

// appearanceSkinAccent 是每套皮肤、每个模式唯一可配的强调色族。
type appearanceSkinAccent struct {
	primary, primaryHover, primaryActive, primaryForeground string
	switchChecked, switchCheckedHover, switchCheckedHandle  string
}

func applyAppearanceSkinAccent(mode AppearanceSkinModeTokens, accent appearanceSkinAccent) AppearanceSkinModeTokens {
	mode.Primary, mode.PrimaryHover, mode.PrimaryActive, mode.PrimaryForeground = accent.primary, accent.primaryHover, accent.primaryActive, accent.primaryForeground
	mode.SwitchChecked, mode.SwitchCheckedHover, mode.SwitchCheckedHandle = accent.switchChecked, accent.switchCheckedHover, accent.switchCheckedHandle
	mode.AuthAccent = accent.primary
	return mode
}

func defaultClassicAppearanceSkin() AppearanceSkinTheme {
	return AppearanceSkinTheme{
		ID: "classic", Name: "瑞士纸感", Description: "暖纸墨色 · 单一品牌红 · 直角构件", Locked: true,
		Tokens: AppearanceSkinTokens{
			Light: AppearanceSkinModeTokens{
				Canvas: "#f7f6f3", Surface: "#ffffff", SurfaceSubtle: "#f1efeb", SurfaceRaised: "#e7e4de", Overlay: "#ffffff", Text: "#111111", TextMuted: "#6f6b66", Border: "#e3e0da",
				Control: "#ffffff", ControlHover: "#f4f2ee", ControlActive: "#ebe8e2", ControlBorder: "#d6d2cb", ControlFocus: "#111111", ControlDisabledBackground: "#f2f0ec", ControlDisabledForeground: "#a8a49d",
				SwitchChecked: "#c8102e", SwitchCheckedHover: "#a90d27", SwitchCheckedHandle: "#ffffff", SwitchUnchecked: "#c3bfb8", SwitchUncheckedHover: "#a9a49c", SwitchUncheckedHandle: "#ffffff",
				Primary: "#c8102e", PrimaryHover: "#a90d27", PrimaryActive: "#8f0b21", PrimaryForeground: "#ffffff",
				Selected: "#efece6", SelectedHover: "#e6e2da", SelectedActive: "#ddd8cf", SelectedForeground: "#111111",
				Icon: "#3d3a36", IconMuted: "#8b8780", IconActive: "#111111", Success: "#2f7d4f", Warning: "#b26a00", Danger: "#c8102e", DangerHover: "#a90d27", DangerActive: "#8f0b21", DangerForeground: "#ffffff", Info: "#1f5fa8", Workspace: "#ffffff", WorkspaceGrid: "#efede9",
				AdminBackground: "#f4f2ee", AdminSurface: "#ffffff", AdminSubtle: "#f8f6f3", AdminStrong: "#eae7e1", AuthBackground: "#f7f6f3", AuthPanel: "#f1efeb", AuthCard: "#ffffff", AuthAccent: "#c8102e", AuthMuted: "#6f6b66",
			},
			Dark: AppearanceSkinModeTokens{
				Canvas: "#121212", Surface: "#1a1a1a", SurfaceSubtle: "#212121", SurfaceRaised: "#2b2b2b", Overlay: "#1e1e1e", Text: "#f4f2ef", TextMuted: "#a5a09a", Border: "#2f2e2c",
				Control: "#1f1f1f", ControlHover: "#272727", ControlActive: "#303030", ControlBorder: "#3d3c39", ControlFocus: "#f4f2ef", ControlDisabledBackground: "#242424", ControlDisabledForeground: "#6f6c68",
				SwitchChecked: "#e05163", SwitchCheckedHover: "#ef6b7c", SwitchCheckedHandle: "#240a0f", SwitchUnchecked: "#4a4946", SwitchUncheckedHover: "#5d5b57", SwitchUncheckedHandle: "#f4f2ef",
				Primary: "#e05163", PrimaryHover: "#ef6b7c", PrimaryActive: "#c84456", PrimaryForeground: "#240a0f",
				Selected: "#2a2a2a", SelectedHover: "#333333", SelectedActive: "#3c3c3c", SelectedForeground: "#f4f2ef",
				Icon: "#d7d4cf", IconMuted: "#8f8b86", IconActive: "#ffffff", Success: "#4ade80", Warning: "#f2b35f", Danger: "#ff7a7a", DangerHover: "#ff9a9a", DangerActive: "#e06262", DangerForeground: "#2b0808", Info: "#7fb2f0", Workspace: "#1a1a1a", WorkspaceGrid: "#242322",
				AdminBackground: "#141414", AdminSurface: "#1c1c1c", AdminSubtle: "#232323", AdminStrong: "#2c2c2c", AuthBackground: "#101010", AuthPanel: "#181818", AuthCard: "#1f1f1f", AuthAccent: "#ef6b7c", AuthMuted: "#a5a09a",
			},
			Components: swissAppearanceSkinComponents(),
			Buttons:    defaultAppearanceSkinButtons(false),
		},
	}
}

// defaultSwissInkSkin 是同一套中性阶的无彩色变体，强调色退化为墨色。
func defaultSwissInkSkin() AppearanceSkinTheme {
	theme := defaultClassicAppearanceSkin()
	theme.ID = "swiss-ink"
	theme.Name = "墨色理性"
	theme.Description = "中性墨阶 · 无彩色强调 · 直角构件"
	theme.Locked = false
	theme.Tokens.Light = applyAppearanceSkinAccent(theme.Tokens.Light, appearanceSkinAccent{
		primary: "#171717", primaryHover: "#303030", primaryActive: "#404040", primaryForeground: "#ffffff",
		switchChecked: "#171717", switchCheckedHover: "#303030", switchCheckedHandle: "#ffffff",
	})
	theme.Tokens.Dark = applyAppearanceSkinAccent(theme.Tokens.Dark, appearanceSkinAccent{
		primary: "#f5f5f5", primaryHover: "#ffffff", primaryActive: "#e5e5e5", primaryForeground: "#171717",
		switchChecked: "#f5f5f5", switchCheckedHover: "#ffffff", switchCheckedHandle: "#171717",
	})
	return theme
}

type appearanceSkinPalette struct {
	canvas, surface, subtle, raised, overlay, text, muted, border                                                        string
	primary, primaryHover, primaryActive, primaryForeground, selected, selectedHover, selectedActive, selectedForeground string
	switchChecked, switchCheckedHover, switchCheckedHandle, switchUnchecked, switchUncheckedHover, switchUncheckedHandle string
	success, warning, danger, dangerHover, dangerActive, dangerForeground, info                                          string
	workspace, grid, adminBackground, adminSurface, adminSubtle, adminStrong                                             string
	authBackground, authPanel, authCard, authAccent, authMuted                                                           string
}

func tintAppearanceSkinMode(value AppearanceSkinModeTokens, palette appearanceSkinPalette) AppearanceSkinModeTokens {
	value.Canvas, value.Surface, value.SurfaceSubtle, value.SurfaceRaised, value.Overlay = palette.canvas, palette.surface, palette.subtle, palette.raised, palette.overlay
	value.Text, value.TextMuted, value.Border = palette.text, palette.muted, palette.border
	value.Control, value.ControlHover, value.ControlActive, value.ControlBorder, value.ControlFocus = palette.surface, palette.subtle, palette.raised, palette.border, palette.primary
	value.ControlDisabledBackground, value.ControlDisabledForeground = palette.subtle, palette.muted
	value.SwitchChecked, value.SwitchCheckedHover, value.SwitchCheckedHandle = palette.switchChecked, palette.switchCheckedHover, palette.switchCheckedHandle
	value.SwitchUnchecked, value.SwitchUncheckedHover, value.SwitchUncheckedHandle = palette.switchUnchecked, palette.switchUncheckedHover, palette.switchUncheckedHandle
	value.Primary, value.PrimaryHover, value.PrimaryActive, value.PrimaryForeground = palette.primary, palette.primaryHover, palette.primaryActive, palette.primaryForeground
	value.Selected, value.SelectedHover, value.SelectedActive, value.SelectedForeground = palette.selected, palette.selectedHover, palette.selectedActive, palette.selectedForeground
	value.Icon, value.IconMuted, value.IconActive = palette.text, palette.muted, palette.primary
	value.Success, value.Warning, value.Danger = palette.success, palette.warning, palette.danger
	value.DangerHover, value.DangerActive, value.DangerForeground = palette.dangerHover, palette.dangerActive, palette.dangerForeground
	value.Info, value.Workspace, value.WorkspaceGrid = palette.info, palette.workspace, palette.grid
	value.AdminBackground, value.AdminSurface, value.AdminSubtle, value.AdminStrong = palette.adminBackground, palette.adminSurface, palette.adminSubtle, palette.adminStrong
	value.AuthBackground, value.AuthPanel, value.AuthCard, value.AuthAccent, value.AuthMuted = palette.authBackground, palette.authPanel, palette.authCard, palette.authAccent, palette.authMuted
	return value
}

func appearanceSkinComponentPreset(buttonRadius, inputRadius, cardRadius, overlayRadius, menuRadius, checkboxRadius int) AppearanceSkinComponentTokens {
	return AppearanceSkinComponentTokens{
		ButtonRadius: buttonRadius, InputRadius: inputRadius, CardRadius: cardRadius, OverlayRadius: overlayRadius, MenuRadius: menuRadius, CheckboxRadius: checkboxRadius,
		ControlHeight: 36, ControlHeightSmall: 30, ControlHeightLarge: 42, BorderWidth: 1, FocusRingWidth: 2, IconSize: 16, ButtonFontWeight: 500,
		HoverLift: 1, MotionFast: 120, MotionNormal: 180, ShadowStyle: "soft",
	}
}

func cloneAppearanceSkin(source AppearanceSkinTheme, id, name, description string) AppearanceSkinTheme {
	source.ID, source.Name, source.Description, source.Locked = id, name, description, false
	return source
}

func normalizeAppearanceSkinThemes(themes []AppearanceSkinTheme) []AppearanceSkinTheme {
	result := make([]AppearanceSkinTheme, len(themes))
	copy(result, themes)
	builtins := defaultAppearanceSkinThemes()
	classic := defaultClassicAppearanceSkin()
	replacedClassic := false
	for index := range result {
		result[index].ID = strings.ToLower(strings.TrimSpace(result[index].ID))
		if result[index].ID == defaultAppearanceSkinID {
			// The system default is not editable. Whatever was submitted is
			// discarded so a drifted client copy cannot reject the whole save.
			result[index] = classic
			replacedClassic = true
			continue
		}
		result[index].Name = strings.TrimSpace(result[index].Name)
		result[index].Description = strings.TrimSpace(result[index].Description)
		result[index].Locked = false
		// Only a wholly absent legacy button block is upgraded. Partial or
		// malformed submitted parameters remain invalid on the write path.
		if result[index].Tokens.Buttons == (AppearanceSkinButtons{}) {
			result[index].Tokens.Buttons = defaultAppearanceSkinButtons(result[index].Locked)
		}
		var fallback AppearanceSkinTokens
		for _, builtin := range builtins {
			if builtin.ID == result[index].ID {
				fallback = builtin.Tokens
				break
			}
		}
		backfillAppearanceSkinModeColors(&result[index].Tokens.Light, fallback.Light)
		backfillAppearanceSkinModeColors(&result[index].Tokens.Dark, fallback.Dark)
		normalizeAppearanceSkinModeColors(&result[index].Tokens.Light)
		normalizeAppearanceSkinModeColors(&result[index].Tokens.Dark)
		result[index].Tokens.Components.ShadowStyle = strings.ToLower(strings.TrimSpace(result[index].Tokens.Components.ShadowStyle))
	}
	if !replacedClassic {
		if len(result) >= maxAppearanceSkinThemes {
			result = result[:maxAppearanceSkinThemes-1]
		}
		result = append([]AppearanceSkinTheme{classic}, result...)
	}
	return result
}

func backfillAppearanceSkinModeColors(mode *AppearanceSkinModeTokens, fallback AppearanceSkinModeTokens) {
	for _, field := range []struct {
		value    *string
		fallback string
		derived  string
	}{
		{&mode.SwitchChecked, fallback.SwitchChecked, mode.Primary},
		{&mode.SwitchCheckedHover, fallback.SwitchCheckedHover, mode.PrimaryHover},
		{&mode.SwitchCheckedHandle, fallback.SwitchCheckedHandle, mode.PrimaryForeground},
		{&mode.SwitchUnchecked, fallback.SwitchUnchecked, mode.ControlBorder},
		{&mode.SwitchUncheckedHover, fallback.SwitchUncheckedHover, mode.ControlActive},
		{&mode.SwitchUncheckedHandle, fallback.SwitchUncheckedHandle, mode.SelectedForeground},
		{&mode.DangerHover, fallback.DangerHover, mode.Danger},
		{&mode.DangerActive, fallback.DangerActive, mode.Danger},
		{&mode.DangerForeground, fallback.DangerForeground, mode.PrimaryForeground},
	} {
		if strings.TrimSpace(*field.value) != "" {
			continue
		}
		if field.fallback != "" {
			*field.value = field.fallback
		} else {
			*field.value = field.derived
		}
	}
}

func normalizeAppearanceSkinModeColors(mode *AppearanceSkinModeTokens) {
	value := reflect.ValueOf(mode).Elem()
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		field.SetString(strings.ToLower(strings.TrimSpace(field.String())))
	}
}

func validateAppearanceSkinThemes(themes []AppearanceSkinTheme, selectedID string) error {
	if len(themes) == 0 || len(themes) > maxAppearanceSkinThemes {
		return BadAuthRequest(fmt.Sprintf("皮肤主题数量必须为 1 到 %d 套", maxAppearanceSkinThemes))
	}
	seen := make(map[string]struct{}, len(themes))
	foundSelected := false
	for _, skin := range themes {
		if !appearanceSkinIDPattern.MatchString(skin.ID) {
			return BadAuthRequest("皮肤主题 ID 无效")
		}
		if _, exists := seen[skin.ID]; exists {
			return BadAuthRequest("皮肤主题 ID 不能重复")
		}
		seen[skin.ID] = struct{}{}
		if skin.ID == selectedID {
			foundSelected = true
		}
		if err := validateAppearanceSkinText(skin.Name, "皮肤主题名称", 40, true); err != nil {
			return err
		}
		if err := validateAppearanceSkinText(skin.Description, "皮肤主题说明", 100, false); err != nil {
			return err
		}
		if err := validateAppearanceSkinMode(skin.Tokens.Light, fmt.Sprintf("主题「%s」的浅色模式", skin.Name)); err != nil {
			return err
		}
		if err := validateAppearanceSkinMode(skin.Tokens.Dark, fmt.Sprintf("主题「%s」的深色模式", skin.Name)); err != nil {
			return err
		}
		if err := validateAppearanceSkinComponents(skin.Tokens.Components); err != nil {
			return err
		}
		for _, fill := range []AppearanceSkinButtonFill{skin.Tokens.Buttons.Light, skin.Tokens.Buttons.Dark} {
			if err := validateAppearanceSkinButtonFill(fill); err != nil {
				return err
			}
		}
	}
	if !foundSelected {
		return BadAuthRequest("当前启用的皮肤主题不存在")
	}
	return nil
}

// migrateAppearanceSkinThemes 只在读取路径使用：把历史皮肤静默降级到瑞士约束
// （直角、无阴影、无悬停抬升、150ms 动效、纯色按钮）。写入路径不做降级，
// 违规提交由 validateAppearanceSkinThemes 明确拒绝。
func migrateAppearanceSkinThemes(themes []AppearanceSkinTheme) []AppearanceSkinTheme {
	for index := range themes {
		components := &themes[index].Tokens.Components
		components.ButtonRadius, components.InputRadius, components.CardRadius = 0, 0, 0
		components.OverlayRadius, components.MenuRadius, components.CheckboxRadius = 0, 0, 0
		components.HoverLift = 0
		components.MotionFast, components.MotionNormal = 150, 150
		components.ShadowStyle = "none"
		for _, fill := range []*AppearanceSkinButtonFill{&themes[index].Tokens.Buttons.Light, &themes[index].Tokens.Buttons.Dark} {
			fill.Mode = "solid"
		}
	}
	return themes
}

func appearanceSkinModeFieldValues(mode AppearanceSkinModeTokens) map[string]string {
	result := make(map[string]string, len(appearanceSkinModeFieldLabels))
	value := reflect.ValueOf(mode)
	fieldType := value.Type()
	for index := 0; index < fieldType.NumField(); index++ {
		name := strings.Split(fieldType.Field(index).Tag.Get("json"), ",")[0]
		result[name] = value.Field(index).String()
	}
	return result
}

func validateAppearanceSkinMode(mode AppearanceSkinModeTokens, label string) error {
	values := appearanceSkinModeFieldValues(mode)
	for _, field := range appearanceSkinModeFieldLabels {
		value := strings.TrimSpace(values[field.name])
		if value == "" {
			if field.name == "primary" {
				return BadAuthRequest(fmt.Sprintf("%s的品牌主色不能为空", label))
			}
			return BadAuthRequest(fmt.Sprintf("%s的%s不能为空", label, field.label))
		}
		if !appearanceColorPattern.MatchString(value) {
			return BadAuthRequest(fmt.Sprintf("%s的%s必须使用 6 或 8 位十六进制颜色", label, field.label))
		}
	}
	primary, ok := parseAppearanceSkinColor(values["primary"])
	if !ok {
		return BadAuthRequest(label + "的品牌主色必须是有效的十六进制颜色")
	}
	foreground, ok := parseAppearanceSkinColor(values["primaryForeground"])
	if !ok {
		return BadAuthRequest(label + "的主操作前景必须是有效的十六进制颜色")
	}
	if appearanceSkinContrast(primary, foreground) < appearanceSkinMinContrast {
		return BadAuthRequest(fmt.Sprintf("%s的品牌主色与主按钮文字对比度必须达到 %.1f:1", label, appearanceSkinMinContrast))
	}
	primaryHue, hasAccentHue := appearanceSkinHue(primary)
	if !hasAccentHue {
		return nil
	}
	for _, field := range appearanceSkinModeFieldLabels {
		if field.name == "primary" {
			continue
		}
		if _, signal := appearanceSkinSignalFields[field.name]; signal {
			continue
		}
		color, ok := parseAppearanceSkinColor(values[field.name])
		if !ok || appearanceSkinChroma(color) <= appearanceSkinNeutralChroma {
			continue
		}
		hue, ok := appearanceSkinHue(color)
		if !ok {
			continue
		}
		if appearanceSkinHueDistance(hue, primaryHue) > appearanceSkinMaxHueDistance {
			return BadAuthRequest(fmt.Sprintf("%s的%s偏离品牌主色色相超过 %d°，皮肤只能有一个强调色相", label, field.label, appearanceSkinMaxHueDistance))
		}
	}
	return nil
}

func validateAppearanceSkinButtonFill(fill AppearanceSkinButtonFill) error {
	if fill.Mode != "solid" {
		return BadAuthRequest("主按钮填充必须为纯色（solid），不允许渐变或其他填充方式")
	}
	if fill.Angle < 0 || fill.Angle > 360 {
		return BadAuthRequest("主按钮渐变角度必须在 0 到 360 之间")
	}
	for _, color := range []string{fill.Start, fill.End, fill.HoverStart, fill.HoverEnd, fill.ActiveStart, fill.ActiveEnd, fill.Foreground} {
		if !appearanceColorPattern.MatchString(color) {
			return BadAuthRequest("主按钮颜色必须使用 6 或 8 位十六进制颜色")
		}
	}
	return nil
}

func validateAppearanceSkinComponents(value AppearanceSkinComponentTokens) error {
	if value.ButtonRadius != 0 || value.InputRadius != 0 || value.CardRadius != 0 || value.OverlayRadius != 0 || value.MenuRadius != 0 || value.CheckboxRadius != 0 {
		return BadAuthRequest("构件圆角必须为 0：瑞士设计守则要求直角构件")
	}
	if value.HoverLift != 0 {
		return BadAuthRequest("悬停抬升必须为 0：悬停不得改变控件几何尺寸")
	}
	if value.MotionFast != 150 || value.MotionNormal != 150 {
		return BadAuthRequest("动效时长必须固定为 150ms")
	}
	if value.ShadowStyle != "none" {
		return BadAuthRequest("阴影风格必须为 none：瑞士设计守则不使用投影")
	}
	for _, candidate := range []struct {
		value, min, max int
		label           string
	}{
		{value.ControlHeight, 30, 48, "控件高度"}, {value.ControlHeightSmall, 24, 40, "小控件高度"}, {value.ControlHeightLarge, 36, 56, "大控件高度"}, {value.BorderWidth, 1, 3, "描边宽度"}, {value.FocusRingWidth, 1, 4, "焦点环宽度"},
		{value.IconSize, 12, 24, "图标尺寸"}, {value.ButtonFontWeight, 400, 700, "按钮字重"},
	} {
		if candidate.value < candidate.min || candidate.value > candidate.max {
			return BadAuthRequest(fmt.Sprintf("%s必须在 %d 到 %d 之间", candidate.label, candidate.min, candidate.max))
		}
	}
	if value.ControlHeightSmall > value.ControlHeight || value.ControlHeight > value.ControlHeightLarge {
		return BadAuthRequest("控件高度须满足小号不大于标准、标准不大于大号")
	}
	return nil
}

func validateAppearanceSkinText(value, label string, maxRunes int, required bool) error {
	if required && value == "" {
		return BadAuthRequest(label + "不能为空")
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return BadAuthRequest(fmt.Sprintf("%s不能超过 %d 个字符", label, maxRunes))
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return BadAuthRequest(label + "不能包含控制字符")
		}
	}
	return nil
}

func activeAppearanceSkin(themes []AppearanceSkinTheme, id string) AppearanceSkinTheme {
	for _, skin := range themes {
		if skin.ID == id {
			return skin
		}
	}
	return defaultClassicAppearanceSkin()
}
