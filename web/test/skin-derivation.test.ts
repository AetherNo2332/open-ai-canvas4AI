import { expect, test } from "bun:test";

import { DEFAULT_CLASSIC_SKIN, SKIN_MODE_COLOR_KEYS, deriveSkinModeTokens, skinColorChroma, skinColorContrast, skinColorHue } from "../src/lib/skin-themes";

const HEX = /^#[0-9a-f]{6}$/i;
const SIGNAL_KEYS = new Set(["success", "warning", "danger", "dangerHover", "dangerActive", "dangerForeground", "info"]);

function hueDistance(a: number, b: number) {
    const delta = Math.abs(a - b) % 360;
    return delta > 180 ? 360 - delta : delta;
}

test("deriveSkinModeTokens fills every semantic slot from one accent", () => {
    const tokens = deriveSkinModeTokens({ primary: "#c8102e", foreground: "#111111", canvas: "#f7f6f3", mode: "light" });

    expect(Object.keys(tokens).sort()).toEqual([...SKIN_MODE_COLOR_KEYS].sort());
    for (const key of SKIN_MODE_COLOR_KEYS) expect(tokens[key], key).toMatch(HEX);
    expect(tokens.primary).toBe("#c8102e");
    expect(tokens.text).toBe("#111111");
    expect(tokens.canvas).toBe("#f7f6f3");
});

test("derived accent states keep a single hue and a readable foreground", () => {
    for (const [mode, primary, foreground, canvas] of [
        ["light", "#c8102e", "#111111", "#f7f6f3"],
        ["dark", "#e05163", "#f4f2ef", "#121212"],
        ["light", "#1f5fa8", "#111111", "#f7f6f3"],
    ] as const) {
        const tokens = deriveSkinModeTokens({ primary, foreground, canvas, mode });
        const accentHue = skinColorHue(primary)!;

        expect(skinColorContrast(tokens.primary, tokens.primaryForeground), `${mode} ${primary} contrast`).toBeGreaterThanOrEqual(4.5);
        expect(tokens.primaryHover).not.toBe(tokens.primary);
        expect(tokens.primaryActive).not.toBe(tokens.primaryHover);

        for (const key of SKIN_MODE_COLOR_KEYS) {
            if (SIGNAL_KEYS.has(key) || key === "primary") continue;
            const hue = skinColorHue(tokens[key]);
            const chroma = skinColorChroma(tokens[key]);
            if (hue === null || chroma <= 0.06) continue;
            expect(hueDistance(hue, accentHue), `${mode} ${key} = ${tokens[key]}`).toBeLessThanOrEqual(30);
        }
    }
});

test("dark derivation recomputes on dark anchors instead of reusing light values", () => {
    const light = deriveSkinModeTokens({ primary: "#c8102e", foreground: "#111111", canvas: "#f7f6f3", mode: "light" });
    const dark = deriveSkinModeTokens({ primary: "#c8102e", foreground: "#f4f2ef", canvas: "#121212", mode: "dark" });

    expect(dark.canvas).toBe("#121212");
    expect(dark.surface).not.toBe(light.surface);
    expect(skinColorContrast(dark.primary, dark.primaryForeground)).toBeGreaterThanOrEqual(4.5);
    expect(skinColorContrast(light.text, light.canvas)).toBeGreaterThanOrEqual(7);
});

test("built-in preset also keeps to the single accent rule", () => {
    for (const mode of ["light", "dark"] as const) {
        const tokens = DEFAULT_CLASSIC_SKIN.tokens[mode];
        const accentHue = skinColorHue(tokens.primary)!;
        for (const key of SKIN_MODE_COLOR_KEYS) {
            if (SIGNAL_KEYS.has(key)) continue;
            const hue = skinColorHue(tokens[key]);
            const chroma = skinColorChroma(tokens[key]);
            if (hue === null || chroma <= 0.06) continue;
            expect(hueDistance(hue, accentHue), `${mode} ${key} = ${tokens[key]}`).toBeLessThanOrEqual(30);
        }
        expect(skinColorContrast(tokens.primary, tokens.primaryForeground), `${mode} preset contrast`).toBeGreaterThanOrEqual(4.5);
    }
});
