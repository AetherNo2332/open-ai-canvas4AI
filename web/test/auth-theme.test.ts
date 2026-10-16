import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { DEFAULT_CLASSIC_SKIN, duplicateSkinDefinition, skinCSSVariables } from "../src/lib/skin-themes";
import { getAntThemeConfig } from "../src/lib/app-theme";

const source = (path: string) => readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8");

describe("authentication follows the application theme", () => {
    test("checkbox ticks use their skin's contrasting foreground in both modes", () => {
        const skin = duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"]);
        for (const mode of ["light", "dark"] as const) {
            skin.tokens[mode].primary = mode === "dark" ? "#f5f5f5" : "#171717";
            skin.tokens[mode].primaryForeground = mode === "dark" ? "#131313" : "#ffffff";
            const config = getAntThemeConfig(mode === "dark", skin);
            expect(config.components?.Checkbox?.colorWhite).toBe(skin.tokens[mode].primaryForeground);
        }
    });
    test("classic login surfaces have distinct light and dark palettes", () => {
        // globals.css 在不同平台上可能是 LF 或 CRLF；先归一化再分段，
        // 否则按字面换行查找 .dark 段会返回 -1，把断言变成永远失败。
        const css = source("styles/globals.css").replace(/\r\n/g, "\n");
        const darkStart = css.indexOf(".dark {\n    --background:");
        const dark = css.slice(darkStart);
        for (const [mode, background, panel, card] of [
            ["light", "#f7f6f3", "#f1efeb", "#ffffff"],
            ["dark", "#101010", "#181818", "#1f1f1f"],
        ] as const) {
            const variables = skinCSSVariables(DEFAULT_CLASSIC_SKIN, mode);
            const modeCSS = mode === "dark" ? dark : css.slice(0, darkStart);
            expect(variables["--auth-page-bg"]).toBe(background);
            expect(variables["--auth-panel-bg"]).toBe(panel);
            expect(variables["--auth-card-bg"]).toBe(card);
            for (const key of ["--auth-page-bg", "--auth-panel-bg", "--auth-card-bg", "--auth-accent", "--auth-muted"]) {
                expect(modeCSS).toContain(`${key}: ${variables[key]};`);
            }
        }
    });

    test("auth inherits the global Ant Design provider instead of forcing dark controls", () => {
        const scene = source("pages/auth/auth-scene.tsx");
        expect(scene).not.toContain("ConfigProvider");
        expect(scene).not.toContain("getAntThemeConfig");
        expect(scene).not.toContain("auth-card-dark");
        expect(scene.match(/<main className="([^"]+)"/)?.[1]).not.toContain("text-white");
        expect(scene).not.toContain("<video");
        expect(scene).toContain("auth-scene-title");
        // The centered form keeps the configured brand logo in the resolved theme.
        expect(scene).toContain("useThemeStore((state) => state.theme)");
        expect(scene).toContain("<BrandLogo theme={theme}");
        expect(source("components/layout/app-providers.tsx")).toContain("getAntThemeConfig(dark, appearance.activeSkin)");
    });

    test("form labels, links and notices do not assume white text", () => {
        for (const page of ["login", "register", "forgot-password"]) {
            const form = source(`pages/auth/${page}.tsx`);
            expect(form).not.toMatch(/(?:text|border|bg)-white/);
            expect(form).not.toMatch(/text-(?:blue|amber)-[123]00/);
            expect(form).toContain("auth-scene-label");
            expect(form).toContain("auth-scene-icon");
        }
        expect(source("pages/auth/register.tsx")).toContain("auth-scene-notice");
        expect(source("pages/auth/forgot-password.tsx")).toContain("auth-scene-notice");
    });

    test("focus and autofill keep theme-aware foregrounds and visible focus", () => {
        const css = source("pages/auth/auth-scene.css");
        expect(css).toContain("input:-webkit-autofill");
        expect(css).toContain("-webkit-text-fill-color: var(--foreground) !important");
        expect(css).toContain("box-shadow: 0 0 0 1000px var(--auth-card-bg, var(--background)) inset !important");
        expect(css).toContain(".auth-scene-link:focus-visible");
        expect(css).toContain("outline: 2px solid var(--ring)");
    });

    test("custom auth palettes are preserved per mode", () => {
        const skin = duplicateSkinDefinition(DEFAULT_CLASSIC_SKIN, ["classic"]);
        skin.tokens.light.authPanel = "#f6ebe3";
        skin.tokens.light.authCard = "#fffdf9";
        skin.tokens.dark.authPanel = "#21120e";
        skin.tokens.dark.authCard = "#361f17";
        expect(skinCSSVariables(skin, "light")["--auth-panel-bg"]).toBe("#f6ebe3");
        expect(skinCSSVariables(skin, "light")["--auth-card-bg"]).toBe("#fffdf9");
        expect(skinCSSVariables(skin, "dark")["--auth-panel-bg"]).toBe("#21120e");
        expect(skinCSSVariables(skin, "dark")["--auth-card-bg"]).toBe("#361f17");
    });
});
