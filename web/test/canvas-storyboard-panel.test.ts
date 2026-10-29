import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, test } from "bun:test";

const source = (name: string) => readFileSync(resolve(import.meta.dir, `../src/pages/canvas/${name}`), "utf8");

describe("storyboard prompt panel boundaries", () => {
    test("creation and duplication clear the previous node panel", () => {
        const operations = source("use-canvas-node-operations.ts");
        expect(operations).toContain("if (type === CanvasNodeType.Script) setDialogNodeId(null)");
        expect(operations).toContain("if (source.type === CanvasNodeType.Script) setDialogNodeId(null)");
        expect(operations).toContain("primaryNode.type !== CanvasNodeType.Script ? primaryNode.id : null");
    });

    test("single and batch connection creation clear the previous panel", () => {
        const connections = source("use-canvas-connection-controller.ts");
        expect(connections.match(/if \(nodeType === CanvasNodeType.Script\) setDialogNodeId\(null\)/g)).toHaveLength(2);
    });

    test("focusing a storyboard does not open a panel", () => {
        expect(source("use-canvas-viewport-controller.ts")).toContain("node.type === CanvasNodeType.Drawing || node.type === CanvasNodeType.Script ? null : node.id");
    });

    test("click and overlay guards remain while the storyboard editor stays available", () => {
        const project = source("project.tsx");
        expect(project).toMatch(/else if \(node.type === CanvasNodeType.Script\) \{\s*setDialogNodeId\(null\)/);
        expect(project).toContain("dialogNode.type !== CanvasNodeType.Script");
        expect(project).toContain("onOpen={() => setScriptEditorNodeId(contentNode.id)}");
        expect(project).toContain("onGenerateScript={(prompt) => void generateScriptRows(contentNode.id, prompt)}");
    });

    test("storyboard generation strips legacy style instructions without sending project style", () => {
        const storyboard = source("use-canvas-storyboard.ts");
        expect(storyboard).toContain("stripProjectStyleExecutionBlocks(prompt)");
        expect(storyboard).not.toContain("projectStyle: storyboardContext.projectStyle");
    });
});

describe("storyboard table rendering boundaries", () => {
    const script = readFileSync(resolve(import.meta.dir, "../src/components/canvas/canvas-script-node.tsx"), "utf8");

    test("the in-node row list mounts only the visible window and keeps the edited row", () => {
        expect(script).toContain("storyboardVisibleRowWindow(scrollTop, tableHeight, STORYBOARD_ROW_HEIGHT, rows.length)");
        expect(script).not.toContain("storyboardCardRowWindow");
        expect(script).toContain("<StoryboardWindowedRows");
        expect(script).not.toMatch(/<Fragment key=\{`\$\{range\.start\}/);
        expect(script).toContain("storyboardMountedRowRanges(");
        expect(script).toContain("data-storyboard-row-id={row.id}");
        expect(script).toContain("setFocusedRowId(");
    });

    test("the scrolling table stays inside the canvas wheel boundary and scrolls both axes in its own container", () => {
        expect(script).toMatch(/data-canvas-wheel-scroll[\s\S]{0,200}aria-label="分镜镜头列表"/);
        expect(script).toContain("storyboard-scrollbar min-h-0 flex-1 overflow-auto");
        expect(script).not.toContain("transition: all");
    });

    test("the full-screen editor fills the viewport and re-measures its table on resize", () => {
        expect(script).toContain('container: { height: "calc(100dvh - 24px)", display: "flex", flexDirection: "column" }');
        expect(script).toContain("observeStoryboardElementSize(tableWrap");
        expect(script).toContain("ref={setTableWrap}");
        expect(script).toContain("storyboardEditorTableScrollY(tableHeight)");
        expect(script).not.toContain('y: "calc(78vh - 170px)"');
    });

    test("the duration cell keeps its number and unit inside the column", () => {
        expect(script).not.toContain('addonAfter="s"');
        expect(script).toContain('suffix="s"');
    });
});
