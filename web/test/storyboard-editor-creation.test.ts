import { expect, test } from "bun:test";
import capabilities from "./fixtures/storyboard-editor-capabilities.json";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { canvasPersistentEditorDraft } from "../src/lib/canvas/canvas-editor-transient";
import { createCanvasNode } from "../src/lib/canvas/canvas-project-domain";
import { upsertProjectChapterStoryboard } from "../src/lib/canvas/project-chapter-storyboard";
import { CanvasNodeType } from "../src/types/canvas";

// The fixture keeps only renderer-used fields from commands.Capabilities().
test.each([undefined, 1])("storyboard factory maps to host commands with reference version %s", (referenceFieldsVersion) => {
    const base = { id: "canvas", referenceFieldsVersion, nodes: [], connections: [] };
    const node = createCanvasNode(CanvasNodeType.Script, { x: 0, y: 0 });
    const operations = canvasEditorCommands(base, canvasPersistentEditorDraft(base, { ...base, nodes: [node] }), capabilities);
    expect(operations[0]).toMatchObject({ type: "add_node", nodeType: "script", id: node.id });
    expect(operations).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ workflowKind: "script" }) }));
});

test("chapter import preserves its source and shot fields through creation commands", () => {
    const seed = upsertProjectChapterStoryboard([], [], { unit: { id: "chapter", title: "Chapter" }, shots: [{ id: "shot", unitId: "chapter", title: "Shot", position: 0, durationMs: 5000, description: "A scene" } as any] });
    const base = { id: "initial", referenceFieldsVersion: 1, nodes: [], connections: [] };
    const operations = canvasEditorCommands(base, { ...base, nodes: seed.nodes }, capabilities);
    expect(operations).toContainEqual(expect.objectContaining({ type: "update_node", patch: expect.objectContaining({ chapterId: "chapter", chapterTitle: "Chapter", workflowTitle: "章节分镜" }) }));
    expect(operations).toContainEqual(expect.objectContaining({ type: "edit_storyboard", action: "append", rowId: "project-shot:shot", patch: expect.objectContaining({ plotDescription: "A scene", durationSeconds: 5 }) }));
});
