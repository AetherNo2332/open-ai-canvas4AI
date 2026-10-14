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

    test("Agent 打开时底部工具栏使用右侧安全区并向可用画布居中", () => {
        const toolbar = read("components/canvas/canvas-toolbar.tsx");
        const project = read("pages/canvas/project.tsx");
        const globalCss = read("styles/globals.css");

        expect(toolbar).toContain("canvas-toolbar-agent-open");
        expect(globalCss).toContain("--canvas-toolbar-agent-offset");
        expect(project).toContain("agentOpen={assistantOpen}");
    });
});
