import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { defaultCanvasAppearance, NEW_CANVAS_BACKGROUND_MODE } from "../src/lib/canvas/canvas-appearance";

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

    test("new canvas defaults use the classic preset for the requested workspace tone", () => {
        expect(NEW_CANVAS_BACKGROUND_MODE).toBe("dots");
        for (const theme of ["light", "dark"] as const) {
            expect(defaultCanvasAppearance(theme)).toMatchObject({
                mode: "preset",
                preset: { id: theme, values: {
                    baseTheme: theme,
                    backgroundColor: theme === "light" ? "#F0F0F0" : "#000000",
                    gridColor: theme === "light" ? "#000000" : "#FFFFFF",
                } },
            });
        }
    });
});
