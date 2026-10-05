import { describe, expect, test } from "bun:test";
import { applySkinTheme, DEFAULT_CLASSIC_SKIN, duplicateSkinDefinition, getSkinButtonAppearance, isSkinButtonFill, normalizeSkinDefinition } from "../src/lib/skin-themes";

describe("theme primary button fills", () => {
    test("classic uses the single accent solid fill in both modes without changing selection colors", () => {
        expect(getSkinButtonAppearance(DEFAULT_CLASSIC_SKIN, "light")).toEqual({
            background: "#c8102e",
            hover: "#a90d27",
            active: "#8f0b21",
            foreground: "#ffffff",
        });
        expect(getSkinButtonAppearance(DEFAULT_CLASSIC_SKIN, "dark")).toEqual({
            background: "#e05163",
            hover: "#ef6b7c",
            active: "#c84456",
            foreground: "#240a0f",
        });
        expect(DEFAULT_CLASSIC_SKIN.tokens.light.selected).toBe("#efece6");
    });

    test("copies have independent fills and switching to solid clears every gradient state", () => {
        const skin = duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"]);
        skin.tokens.buttons.light.mode = "gradient";
        skin.tokens.buttons.light.angle = 45;
        skin.tokens.buttons.light.start = "#123456";
        skin.tokens.buttons.light.end = "#386fbc";
        expect(DEFAULT_CLASSIC_SKIN.tokens.buttons.light.angle).toBe(115);
        expect(skin.tokens.buttons.dark.angle).toBe(115);
        expect(getSkinButtonAppearance(skin, "light").background).toBe("linear-gradient(45deg, #123456, #386fbc)");
        const values = new Map<string, string>();
        const doc = {
            documentElement: {
                dataset: {},
                style: {
                    removeProperty: (key: string) => values.delete(key),
                    setProperty: (key: string, value: string) => values.set(key, value),
                },
            },
        } as unknown as Document;
        applySkinTheme(skin, "light", doc);
        skin.tokens.buttons.light.mode = "solid";
        Object.assign(skin.tokens.light, { primary: "#123456", primaryHover: "#234567", primaryActive: "#345678", primaryForeground: "#ffffff" });
        applySkinTheme(skin, "light", doc);
        expect(values.get("--button-primary-bg")).toBe("#123456");
        expect(values.get("--button-primary-hover-bg")).toBe("#234567");
        expect(values.get("--button-primary-active-bg")).toBe("#345678");
        applySkinTheme(DEFAULT_CLASSIC_SKIN, "dark", doc);
        expect(values.get("--background")).toBe(DEFAULT_CLASSIC_SKIN.tokens.dark.canvas);
        // 深色模式主按钮前景跟随该模式的主色对比度，不再固定白色。
        expect(values.get("--button-primary-fg")).toBe("#240a0f");
    });

    test("legacy themes without a button block fall back to solid actions", () => {
        for (const original of [DEFAULT_CLASSIC_SKIN, duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"])]) {
            const legacy = JSON.parse(JSON.stringify(original));
            delete legacy.tokens.buttons;
            expect(normalizeSkinDefinition(legacy).tokens.buttons.light.mode).toBe("solid");
        }
    });

    test("invalid modes, angles and CSS injection cannot become runtime gradients", () => {
        for (const patch of [{ mode: "url(x)" }, { angle: 361 }, { angle: -1 }, { angle: 1.5 }, { angle: NaN }, { start: "red; background:url(x)" }, { foreground: "white" }]) {
            const skin = duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"]);
            Object.assign(skin.tokens.buttons.light, patch);
            expect(isSkinButtonFill(skin.tokens.buttons.light)).toBe(false);
            expect(getSkinButtonAppearance(normalizeSkinDefinition(skin), "light").background).toBe(skin.tokens.light.primary);
        }
    });
});
