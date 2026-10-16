import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { canvasThemes } from "../src/lib/canvas-theme";
import { defaultCanvasAppearance, DEFAULT_CANVAS_BACKGROUND_MODE } from "../src/lib/canvas/canvas-appearance";

const source = (path: string) => readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8");

describe("auth, workspace chrome, and new canvas defaults", () => {
    test("auth has route-specific headings instead of the previous promotional media", () => {
        const scene = source("pages/auth/auth-scene.tsx");
        expect(scene).not.toContain("authHeroTitle");
        expect(scene).not.toContain("authVideoUrl");
        expect(scene).toContain('title: "创建你的账户"');
        expect(scene).toContain('title: "重新设置密码"');
        expect(scene).toContain("`登录到 ${appearance.brandName}`");
    });

    test("exposes a consistent GitHub icon link in the workspace top bar", () => {
        const topBar = source("components/layout/workspace-top-bar.tsx");

        expect(topBar).toContain("Github");
        expect(topBar).toContain("https://github.com/AetherNo2332/open-ai-canvas4AI/tree/main");
        expect(topBar).toContain('aria-label="打开 GitHub 仓库"');
        expect(topBar).toContain('className="app-workspace-topbar-icon-button"');
    });

    test("uses a black custom appearance for newly created canvases", () => {
        expect(DEFAULT_CANVAS_BACKGROUND_MODE).toBe("blank");
        expect(defaultCanvasAppearance()).toMatchObject({
            mode: "custom",
            custom: {
                baseTheme: "dark",
                backgroundColor: "#000000",
                gridColor: canvasThemes.dark.canvas.line.toUpperCase(),
            },
        });
        expect(source("stores/canvas/use-canvas-store.ts")).toContain("defaultCanvasAppearance()");
    });
});
