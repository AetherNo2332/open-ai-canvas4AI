import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { canvasThemes } from "../src/lib/canvas-theme";
import { defaultCanvasAppearance, DEFAULT_CANVAS_BACKGROUND_MODE } from "../src/lib/canvas/canvas-appearance";

const source = (path: string) => readFileSync(new URL(`../src/${path}`, import.meta.url), "utf8");

describe("auth, workspace chrome, and new canvas defaults", () => {
    test("keeps the login media scrim dark when the dark skin maps neutral text to a light color", () => {
        const css = source("pages/auth/auth-scene.css");

        expect(css).toContain(".dark .auth-scene-media-scrim");
        expect(css).toContain("var(--site-canvas)");
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
