package app

import "testing"

// 内置皮肤必须满足 swiss-design 的结构约束：直角构件、实心按钮、无阴影、
// 无悬停抬升、动效固定 150ms。配置面收敛（只留一个品牌主色）由后续的服务端
// 校验强制，见 .superpowers/plans/2026-10-03-appearance-single-accent.md。
func TestDefaultAppearanceSkinsFollowSwissConstraints(t *testing.T) {
	themes := defaultAppearanceSkinThemes()
	if len(themes) != 2 {
		t.Fatalf("内置皮肤应为纸感与夜间 2 套，实际 %d 套", len(themes))
	}

	ids := map[string]bool{}
	for _, theme := range themes {
		ids[theme.ID] = true
		t.Run(theme.ID, func(t *testing.T) {
			components := theme.Tokens.Components
			radii := map[string]int{
				"buttonRadius":   components.ButtonRadius,
				"inputRadius":    components.InputRadius,
				"cardRadius":     components.CardRadius,
				"overlayRadius":  components.OverlayRadius,
				"menuRadius":     components.MenuRadius,
				"checkboxRadius": components.CheckboxRadius,
			}
			for name, value := range radii {
				if value != 0 {
					t.Errorf("%s 必须为 0，实际 %d", name, value)
				}
			}
			if components.ShadowStyle != "none" {
				t.Errorf("shadowStyle 必须为 none，实际 %q", components.ShadowStyle)
			}
			if components.HoverLift != 0 {
				t.Errorf("hoverLift 必须为 0，实际 %d", components.HoverLift)
			}
			if components.MotionFast != 150 || components.MotionNormal != 150 {
				t.Errorf("动效必须固定 150ms，实际 fast=%d normal=%d", components.MotionFast, components.MotionNormal)
			}
			if mode := theme.Tokens.Buttons.Light.Mode; mode != "solid" {
				t.Errorf("浅色按钮必须为 solid，实际 %q", mode)
			}
			if mode := theme.Tokens.Buttons.Dark.Mode; mode != "solid" {
				t.Errorf("深色按钮必须为 solid，实际 %q", mode)
			}
		})
	}

	// 系统默认主题沿用 classic ID，避免已保存的 skinId 失效；显示名改为瑞士纸感。
	if themes[0].Name != "瑞士纸感" {
		t.Errorf("默认主题显示名应为瑞士纸感，实际 %q", themes[0].Name)
	}
	if !themes[0].Locked {
		t.Error("默认主题必须保持 locked，运营不可改写")
	}
	for _, id := range []string{"classic", "swiss-ink"} {
		if !ids[id] {
			t.Errorf("缺少内置皮肤 %q", id)
		}
	}
}
