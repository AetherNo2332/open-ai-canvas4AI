import { expect, test } from "bun:test";
import capabilities from "./fixtures/drawing-editor-capabilities.json";
import { attachCanvasCommandProject } from "../src/services/canvas-command-project-lifecycle";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
import { canvasDocumentChanges, projectCanvasOperations, type CanvasDocument } from "../src/lib/canvas/canvas-event-projection";
import { canvasEditorCommands } from "../src/lib/canvas/canvas-editor-commands";
import { canvasPersistentEditorDraft } from "../src/lib/canvas/canvas-editor-transient";
import { canvasDrawingDocumentId, canvasDrawingDocumentMetadata } from "../src/lib/canvas/canvas-drawing-engine";
import { persistCanvasDrawingEditorSave } from "../src/lib/canvas/canvas-drawing-document-sync";
import type { CanvasDrawingSnapshot } from "../src/lib/canvas/canvas-drawing-storage";
import { copyCanvasNodeGraph } from "../src/lib/canvas/canvas-node-copy";
import { createCanvasNode } from "../src/lib/canvas/canvas-project-domain";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";

// The fixture keeps the drawing and text paths published by commands.Capabilities().
const empty = (): CanvasDocument => ({ id: "canvas", nodes: [], connections: [] });
const commands = (before: CanvasDocument, nodes: CanvasNodeData[]) => canvasEditorCommands(before, canvasPersistentEditorDraft(before, { ...before, nodes }), capabilities);
const newDrawing = () => createCanvasNode(CanvasNodeType.Drawing, { x: 0, y: 0 }, { drawingEngine: "excalidraw" });
const shape = { id: "shape", type: "rectangle", x: 0, y: 0, width: 40, height: 30 };
const savedDocument = (): CanvasDrawingSnapshot => ({
    version: 2,
    engine: "excalidraw",
    snapshot: { type: "excalidraw", version: 2, source: "canvas", elements: [shape], appState: {}, files: {} },
    revision: 1,
    updatedAt: "2026-10-25T08:00:00.000Z",
    shapeCount: 1,
    pageCount: 1,
});

test("a new empty drawing saves as a plain node and names its document only locally", () => {
    const drawing = newDrawing();
    const operations = commands(empty(), [drawing]);
    expect(operations[0]).toMatchObject({ type: "add_node", id: drawing.id, nodeType: "drawing" });
    expect(operations).toContainEqual(expect.objectContaining({ type: "update_node", id: drawing.id, patch: expect.objectContaining({ drawingEngine: "excalidraw" }) }));
    expect(JSON.stringify(operations)).not.toContain("drawingId");
    // The host names the document when it first creates it; local drafts use that same name.
    expect(drawing.metadata?.drawingId).toBeUndefined();
    expect(canvasDrawingDocumentId(drawing)).toBe(`${drawing.id}-document`);
    expect(canvasDrawingDocumentId({ id: "legacy", metadata: { drawingId: "kept-document" } })).toBe("kept-document");
});

test("an unsaved empty drawing does not block other new nodes in the same draft", () => {
    const drawing = newDrawing();
    const text = createCanvasNode(CanvasNodeType.Text, { x: 400, y: 0 });
    const operations = commands(empty(), [drawing, text]);
    expect(operations.filter((operation) => operation.type === "add_node").map((operation) => operation.id)).toEqual([drawing.id, text.id]);
});

test("the first drawing save creates the native document under the name its local draft used", () => {
    const drawing = newDrawing();
    const host = { ...empty(), nodes: [drawing] };
    const saved = { ...drawing, metadata: { ...drawing.metadata, ...canvasDrawingDocumentMetadata(drawing, savedDocument()) } };
    expect(commands(host, [saved])).toEqual([
        {
            type: "edit_drawing",
            id: drawing.id,
            engine: "excalidraw",
            drawingId: `${drawing.id}-document`,
            drawingSnapshot: { type: "excalidraw", version: 2, source: "canvas", elements: [], appState: {}, files: {} },
            drawingOperations: [{ type: "upsert", id: "shape", record: shape }],
            drawingElementIds: ["shape"],
        },
    ]);
});

test("a drawing first saved without an explicit name saves again without a false conflict", async () => {
    const drawing = newDrawing();
    const document = savedDocument();
    const named = { ...drawing, metadata: { ...drawing.metadata, ...canvasDrawingDocumentMetadata(drawing, document) } };
    let receipt: CanvasDrawingSnapshot | undefined;
    await persistCanvasDrawingEditorSave({
        node: { ...drawing, metadata: { ...drawing.metadata, drawingDocument: document } },
        previous: document,
        createSave: async () => ({ snapshot: document.snapshot, preview: null, render: null }),
        saveLocal: async () => ({ ...document, revision: 2 }),
        publish: async (local) => local,
        currentNode: () => named,
        onSaved: (published) => {
            receipt = published;
        },
    });
    expect(receipt?.revision).toBe(2);
});

test("copying an empty drawing creates another empty drawing without an unsynced document name", () => {
    const source = { ...newDrawing(), id: "source" };
    const legacy = { ...source, metadata: { ...source.metadata, drawingId: "source-document", drawingRevision: 0, drawingShapeCount: 0, drawingPageCount: 1 } };
    for (const original of [source, legacy]) {
        const copied = copyCanvasNodeGraph([original], [], original.id, new Map([[original.id, "copy"]]), () => "edge").nodes[0];
        for (const key of ["drawingId", "drawingRevision", "drawingUpdatedAt", "drawingShapeCount", "drawingPageCount"] as const) expect(copied.metadata?.[key]).toBeUndefined();
        expect(canvasDrawingDocumentId(copied)).toBe("copy-document");
    }
    const withContent = { ...source, metadata: { ...source.metadata, ...canvasDrawingDocumentMetadata(source, savedDocument()) } };
    const copied = copyCanvasNodeGraph([withContent], [], "source", new Map([["source", "copy"]]), () => "edge").nodes[0];
    expect(copied.metadata).toMatchObject({ drawingId: "copy-document", drawingRevision: 1, drawingShapeCount: 1, drawingDocument: { revision: 1, snapshot: savedDocument().snapshot } });
});

test("a new empty drawing is acknowledged and found under the same document name after reopening", async () => {
    let document = empty();
    let revision = 1;
    let eventSeq = 0;
    const sent: any[] = [];
    const transport = {
        snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision, eventSeq, document: structuredClone(document), entityVersions: {} }),
        events: async () => [],
        execute: async (command) => {
            sent.push(command);
            const next = projectCanvasOperations(document, command.operations);
            const changes = canvasDocumentChanges(document, next);
            document = next;
            return { protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", clientId: "client", operationId: command.operationId, actorId: "owner", revision: ++revision, eventSeq: ++eventSeq, changes, entityVersions: {} };
        },
        receipt: async () => {
            throw new Error("unexpected receipt lookup");
        },
    } as CommandTransport;
    const open = async () => {
        const session = new CanvasCommandSession(transport, "client");
        await session.open("canvas");
        const view = { live: {} as CanvasDocument };
        const editor = await attachCanvasCommandProject({
            canvasId: "canvas",
            session,
            capabilities,
            refs: { nodes: { current: [] }, connections: { current: [] } },
            read: () => view.live,
            present: (projected) => {
                view.live = projected;
            },
            view: { read: async () => null, write: async () => {} },
            readViewport: () => ({ x: 0, y: 0, k: 1 }),
            presentViewport: () => {},
        });
        return { session, editor, view };
    };
    const first = await open();
    const drawing = newDrawing();
    first.view.live = { ...first.view.live, nodes: [drawing] };
    await first.editor.commit();
    expect(await first.session.flush()).toBe(true);
    expect(sent).toHaveLength(1);
    expect(sent[0].operations[0]).toMatchObject({ type: "add_node", id: drawing.id, nodeType: "drawing" });
    first.editor.dispose();
    first.session.close();

    const second = await open();
    const reopened = (second.view.live.nodes as CanvasNodeData[]).find((node) => node.id === drawing.id);
    expect(reopened).toBeDefined();
    expect(canvasDrawingDocumentId(reopened!)).toBe(canvasDrawingDocumentId(drawing));
    await second.editor.commit();
    expect(sent).toHaveLength(1);
    second.editor.dispose();
    second.session.close();
});
