import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const read = (path: string) => readFileSync(resolve(import.meta.dir, "../src", path), "utf8");

describe("画布资产批量下载入口", () => {
    test("选择工具栏提供按节点名下载入口", () => {
        const definitions = read("lib/canvas/tool-registry/definitions/selection-toolbar-tools.tsx");
        expect(definitions).toContain('label: "下载选中媒体"');
        expect(definitions).toContain("onDownloadSelected");
        expect(definitions).toContain("downloadableMediaCount");
    });

    test("画布页面把选中节点交给批量下载回调", () => {
        const project = read("pages/canvas/project.tsx");
        const toolbar = read("pages/canvas/canvas-project-selection-toolbar.tsx");
        expect(project).toContain("downloadSelectedMedia");
        expect(toolbar).toContain("onDownloadSelected");
        expect(toolbar).toContain("downloadableMediaCount");
    });
});
