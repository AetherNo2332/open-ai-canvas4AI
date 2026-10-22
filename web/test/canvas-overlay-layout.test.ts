import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const read = (path: string) => readFileSync(resolve(import.meta.dir, "../src", path), "utf8");

describe("画布叠层安全区", () => {
    test("小地图跟随工作区安全区，避免压住左侧栏和工作区轨道", () => {
        const minimap = read("components/canvas/canvas-mini-map.tsx");
        const workspaceCss = read("components/canvas/canvas-workspace-panel.css");

        expect(minimap).toContain("canvas-workspace-minimap");
        expect(workspaceCss).toContain(".canvas-main-with-workspace .canvas-workspace-minimap");
        expect(workspaceCss).toContain("left: calc(var(--canvas-workspace-inset) + var(--canvas-inset-x))");
    });

    test("工具栏定位于不会被 Agent 压缩的外壳，展开 Agent 不改变居中基准", () => {
        const toolbar = read("components/canvas/canvas-toolbar.tsx");
        const project = read("pages/canvas/project.tsx");
        const globalCss = read("styles/globals.css");

        expect(toolbar).not.toContain("agentFloating");
        expect(globalCss).not.toContain("--canvas-toolbar-right-reserve");
        const sectionEnd = project.indexOf("</section>", project.indexOf("<section data-canvas-editor"));
        expect(project.indexOf("<CanvasToolbar", project.indexOf("<section data-canvas-editor"))).toBeGreaterThan(sectionEnd);
        expect(project).toContain("!versions.preview && (!focusMode || focusDockRevealed)");
        expect(globalCss).toContain(".canvas-agent-panel[data-agent-compact]");
    });

    test("模型思考正文保留内边距和长文本换行，不扩大面板", () => {
        const css = read("components/canvas/canvas-cloud-agent.css");
        const content = css.match(/\.agent-reasoning-content\s*\{([^}]+)\}/)?.[1] || "";
        const text = css.match(/\.agent-reasoning-text\s*\{([^}]+)\}/)?.[1] || "";
        expect(content).toContain("padding: var(--space-2) var(--space-4) var(--space-3)");
        expect(content).toContain("overflow-x: hidden");
        expect(content).toContain("scrollbar-gutter: stable");
        expect(text).toContain("white-space: pre-wrap");
        expect(text).toContain("overflow-wrap: anywhere");
    });
});
