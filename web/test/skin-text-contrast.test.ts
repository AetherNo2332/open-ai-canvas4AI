import { expect, test } from "bun:test";
import { DEFAULT_CLASSIC_SKIN, cloneSkinDefinition, getSkinAntOverrides, skinCSSVariables, skinColorContrast } from "../src/lib/skin-themes";
import { getIsolatedAdminAntTheme } from "../src/pages/admin/theme/admin-ant-theme";

test("light ink skin corrects faint saved text for CSS and Ant Design without changing settings", () => {
    const skin = cloneSkinDefinition(DEFAULT_CLASSIC_SKIN);
    skin.id = "swiss-ink";
    Object.assign(skin.tokens.light, { textMuted: "#a8a8a6", iconMuted: "#b8b8b6", authMuted: "#a8a8a6", adminBackground: "#f5f4f1" });
    const before = JSON.stringify(skin);
    const css = skinCSSVariables(skin, "light");
    const ant = getSkinAntOverrides(skin, "light");
    for (const background of [skin.tokens.light.canvas, skin.tokens.light.surface, skin.tokens.light.adminBackground, skin.tokens.light.adminSurface, skin.tokens.light.adminStrong]) {
        expect(skinColorContrast(css["--site-muted"], background)).toBeGreaterThanOrEqual(4.5);
        expect(skinColorContrast(css["--icon-muted"], background)).toBeGreaterThanOrEqual(3);
    }
    expect(skinColorContrast(css["--auth-muted"], "#ffffff")).toBeGreaterThanOrEqual(4.5);
    expect(ant.textMuted).toBe(css["--site-muted"]);
    expect(getIsolatedAdminAntTheme(false, skin).token?.colorTextSecondary).toBe(css["--site-muted"]);
    expect(css["--site-text"]).toBe(skin.tokens.light.text);
    expect(css["--site-canvas"]).toBe(skin.tokens.light.canvas);
    expect(JSON.stringify(skin)).toBe(before);
});

test("readable custom text and dark theme colors are preserved", () => {
    const skin = cloneSkinDefinition(DEFAULT_CLASSIC_SKIN);
    Object.assign(skin.tokens.light, { textMuted: "#505050", iconMuted: "#606060", authMuted: "#505050" });
    for (const mode of ["light", "dark"] as const) {
        const css = skinCSSVariables(skin, mode);
        expect(css["--site-muted"]).toBe(skin.tokens[mode].textMuted);
        expect(css["--icon-muted"]).toBe(skin.tokens[mode].iconMuted);
        expect(css["--auth-muted"]).toBe(skin.tokens[mode].authMuted);
    }
});
