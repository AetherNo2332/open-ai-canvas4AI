package app

import (
	"strings"
	"testing"
)

// baseValidationThemes 返回一套可保存的皮肤，第二套改成自定义主题以便承载违规值。
func baseValidationThemes() []AppearanceSkinTheme {
	themes := defaultAppearanceSkinThemes()
	custom := themes[1]
	custom.ID = "custom-validation"
	custom.Name = "校验用主题"
	themes[1] = custom
	return themes
}

func TestValidateAppearanceSkinThemesAcceptsBuiltins(t *testing.T) {
	if err := validateAppearanceSkinThemes(defaultAppearanceSkinThemes(), defaultAppearanceSkinID); err != nil {
		t.Fatalf("内置皮肤必须通过服务端校验：%v", err)
	}
}

func TestValidateAppearanceSkinThemesEnforcesSwissConstraints(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(themes []AppearanceSkinTheme)
		expect string
	}{
		{
			name:   "gradient-button-fill",
			mutate: func(themes []AppearanceSkinTheme) { themes[1].Tokens.Buttons.Light.Mode = "gradient" },
			expect: "纯色",
		},
		{
			name:   "unknown-button-mode",
			mutate: func(themes []AppearanceSkinTheme) { themes[1].Tokens.Buttons.Dark.Mode = "glow" },
			expect: "纯色",
		},
		{
			name:   "rounded-components",
			mutate: func(themes []AppearanceSkinTheme) { themes[1].Tokens.Components.ButtonRadius = 8 },
			expect: "圆角",
		},
		{
			name:   "hover-lift",
			mutate: func(themes []AppearanceSkinTheme) { themes[1].Tokens.Components.HoverLift = 2 },
			expect: "悬停抬升",
		},
		{
			name:   "slow-motion",
			mutate: func(themes []AppearanceSkinTheme) { themes[1].Tokens.Components.MotionFast = 400 },
			expect: "150",
		},
		{
			name:   "shadow",
			mutate: func(themes []AppearanceSkinTheme) { themes[1].Tokens.Components.ShadowStyle = "soft" },
			expect: "阴影",
		},
		{
			name:   "missing-primary",
			mutate: func(themes []AppearanceSkinTheme) { themes[1].Tokens.Light.Primary = "" },
			expect: "主色",
		},
		{
			name: "low-contrast-foreground",
			mutate: func(themes []AppearanceSkinTheme) {
				themes[1].Tokens.Light.Primary = "#7a7a7a"
				themes[1].Tokens.Light.PrimaryForeground = "#ffffff"
			},
			expect: "对比度",
		},
		{
			name: "second-accent-hue",
			mutate: func(themes []AppearanceSkinTheme) {
				themes[1].Tokens.Light.Primary = "#c8102e"
				themes[1].Tokens.Light.PrimaryForeground = "#ffffff"
				themes[1].Tokens.Light.Selected = "#1f5fa8"
			},
			expect: "色相",
		},
		{
			name:   "duplicate-id",
			mutate: func(themes []AppearanceSkinTheme) { themes[1].ID = themes[0].ID },
			expect: "重复",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			themes := baseValidationThemes()
			testCase.mutate(themes)
			err := validateAppearanceSkinThemes(themes, defaultAppearanceSkinID)
			if err == nil {
				t.Fatalf("违规皮肤必须被拒绝")
			}
			if !strings.Contains(err.Error(), testCase.expect) {
				t.Fatalf("拒绝原因必须说明被拒字段，期望包含 %q，实际 %q", testCase.expect, err.Error())
			}
		})
	}
}

func TestValidateAppearanceSkinThemesKeepsSignalColorsAndNeutralAccent(t *testing.T) {
	themes := baseValidationThemes()
	themes[1].Tokens.Light.Success = "#0b7a3b"
	themes[1].Tokens.Light.Info = "#3f4ae0"
	themes[1].Tokens.Dark.Warning = "#ffb020"
	if err := validateAppearanceSkinThemes(themes, defaultAppearanceSkinID); err != nil {
		t.Fatalf("状态色不应触发单色相限制：%v", err)
	}
}

// 旧皮肤（渐变填充、圆角、长动效）在读取路径必须静默降级为瑞士约束。
func TestMigrateAppearanceSkinThemesDowngradesLegacyValues(t *testing.T) {
	themes := baseValidationThemes()
	legacy := &themes[1]
	legacy.Tokens.Buttons.Light.Mode = "gradient"
	legacy.Tokens.Buttons.Dark.Mode = "gradient"
	legacy.Tokens.Components.ButtonRadius = 14
	legacy.Tokens.Components.CardRadius = 18
	legacy.Tokens.Components.HoverLift = 3
	legacy.Tokens.Components.MotionFast = 400
	legacy.Tokens.Components.MotionNormal = 800
	legacy.Tokens.Components.ShadowStyle = "strong"

	migrated := migrateAppearanceSkinThemes(normalizeAppearanceSkinThemes(themes))
	if err := validateAppearanceSkinThemes(migrated, defaultAppearanceSkinID); err != nil {
		t.Fatalf("降级后的旧皮肤必须可以保存：%v", err)
	}
	components := migrated[1].Tokens.Components
	if components.ButtonRadius != 0 || components.CardRadius != 0 || components.HoverLift != 0 {
		t.Fatalf("圆角与悬停抬升必须降级为 0，实际 %+v", components)
	}
	if components.MotionFast != 150 || components.MotionNormal != 150 || components.ShadowStyle != "none" {
		t.Fatalf("动效与阴影必须降级为瑞士约束，实际 %+v", components)
	}
	if migrated[1].Tokens.Buttons.Light.Mode != "solid" || migrated[1].Tokens.Buttons.Dark.Mode != "solid" {
		t.Fatalf("渐变按钮必须降级为纯色，实际 %+v", migrated[1].Tokens.Buttons)
	}
}
