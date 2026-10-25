// 诊断实际节点工厂、编辑 hook 与命令转换器；生成队列使用本地回调，不调用模型或 worker。
// 从 web/ 运行，传入当前后端能力目录和输出文件；发现失败操作时以退出码 1 结束。
import { Window } from "happy-dom";
import { readFileSync, writeFileSync } from "node:fs";
const dom = new Window({ url: "http://localhost/" });
for (const key of ["window", "document", "navigator", "localStorage", "HTMLElement", "HTMLInputElement", "HTMLTextAreaElement", "SVGElement", "Element", "Node", "Event", "CustomEvent", "ShadowRoot", "MutationObserver", "ResizeObserver", "DOMParser"])
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value: key === "window" ? dom : (dom as any)[key] });
for (const key of ["getComputedStyle", "requestAnimationFrame", "cancelAnimationFrame"]) Object.defineProperty(globalThis, key, { configurable: true, writable: true, value: (dom as any)[key].bind(dom) });
(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
const { act } = await import("react");
const { createRoot } = await import("react-dom/client");
const { QueryClient, QueryClientProvider } = await import("@tanstack/react-query");
const { App } = await import("antd");
const notices: any[] = [];
const notice = Object.fromEntries(["success", "error", "warning", "info"].map((k) => [k, (text: any) => notices.push({ kind: k, text })]));
App.useApp = () => ({ message: notice, modal: { confirm: ({ onOk }: any) => onOk?.() }, notification: notice }) as any;
const { createCanvasNode, createStoryboardRow, attachNodeToStoryboardRow } = await import("../src/lib/canvas/canvas-project-domain.ts");
const { canvasEditorCommands } = await import("../src/lib/canvas/canvas-editor-commands.ts");
const { canvasPersistentEditorDraft } = await import("../src/lib/canvas/canvas-editor-transient.ts");
const { copyCanvasNodeGraph } = await import("../src/lib/canvas/canvas-node-copy.ts");
const { CanvasNodeType } = await import("../src/types/canvas.ts");
const { useCanvasStoryboard } = await import("../src/pages/canvas/use-canvas-storyboard.ts");
const { useCanvasNodeOperations } = await import("../src/pages/canvas/use-canvas-node-operations.ts");
const { useCanvasPrevis } = await import("../src/pages/canvas/use-canvas-previs.ts");
const { useCanvasBatchTable } = await import("../src/pages/canvas/use-canvas-batch-table.ts");
const { useCanvasStyleWorkflow } = await import("../src/pages/canvas/use-canvas-style-workflow.ts");
const { useConfigStore } = await import("../src/stores/use-config-store.ts");
const { createDefaultMediaConversionState } = await import("../src/lib/media-conversion/contracts.ts");
const { canvasDrawingDocumentMetadata } = await import("../src/lib/canvas/canvas-drawing-engine.ts");
const { summarizeCanvasContext } = await import("../src/lib/canvas/canvas-context-summary.ts");
const { canvasNodeSearchContext, searchCanvasNodes } = await import("../src/lib/canvas/canvas-node-search.ts");
useConfigStore.setState({ isAiConfigReady: () => true });
const capabilityFile = process.argv[2];
const outputFile = process.argv[3];
if (!capabilityFile || !outputFile) throw Error("Usage: bun scripts/audit-canvas-node-save.tsx <capabilities.json> <output.json>");
const capabilities = JSON.parse(readFileSync(capabilityFile, "utf8"));
const results: any[] = [];
const empty = () => ({ id: "audit", nodes: [], connections: [], referenceFieldsVersion: 1 });
const doc = (nodes: any[]) => ({ ...empty(), nodes });
function check(name: string, before: any, after: any) {
    try {
        const ops = canvasEditorCommands(before, canvasPersistentEditorDraft(before, after), capabilities);
        let setupOps: any[] | undefined, setupError: string | undefined;
        try {
            setupOps = canvasEditorCommands(empty(), canvasPersistentEditorDraft(empty(), before), capabilities);
        } catch (error) {
            setupError = String(error);
        }
        results.push({ name, status: ops.length ? "converted" : "guarded-no-change", ops, setupOps, setupError, before, after });
    } catch (error) {
        results.push({ name, status: "error", error: String(error), before, after });
    }
}
for (const [type, metadata] of [
    ["text", { content: "正文验收", richText: { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "正文验收" }] }] }, fontSize: 18 }],
    ["markdown", { content: "# 标题\n\n正文 **加粗**" }],
    ["svg", { content: '<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><circle cx="50" cy="50" r="30" fill="blue"/></svg>' }],
    ["html", { content: "<main><h1>页面验收</h1></main>" }],
    ["chart", { content: '[{"月份":"1月","数量":12},{"月份":"2月","数量":18}]', chartKind: "line" }],
    ["panorama", { panoramaConfig: { projection: "cylindrical", sourceMode: "image", smartBase: false } }],
    ["colorgrade", { colorGrade: { brightness: 110, contrast: 90, saturate: 120, hueRotate: 15 } }],
    ["media-conversion", { mediaConversion: { ...createDefaultMediaConversionState(), operation: "lineart", updatedAt: "2026-10-26T00:00:00Z" } }],
    ["config", { workflowProvider: "runninghub", workflowTitle: "工作流验收", workflowParameters: { seed: 42, enabled: true } }],
    ["image", { composerContent: "图片提示词" }],
    ["video", { composerContent: "视频提示词", videoEditOperation: "image_to_video" }],
    ["audio", { composerContent: "音频提示词", audioSpeed: "1.2" }],
    ["frame", { frame: { collapsed: true, expandedWidth: 760, expandedHeight: 520 } }],
] as any[]) {
    const node = createCanvasNode(type, { x: 0, y: 0 });
    const base = doc([node]);
    const configured = { ...node, metadata: { ...node.metadata, ...metadata } };
    check(type + ":edit-content-config", base, doc([configured]));
    const copied = copyCanvasNodeGraph([configured], [], configured.id, new Map([[configured.id, configured.id + "-copy"]]), () => "copy-edge");
    check(type + ":configured-clipboard-paste", doc([configured]), doc([configured, ...copied.nodes]));
}
for (const type of Object.values(CanvasNodeType)) {
    const node = createCanvasNode(type as any, { x: 600, y: 450 });
    const base = doc([node]);
    check(type + ":create", empty(), base);
    check(type + ":rename-move-resize-lock", base, doc([{ ...node, title: "验收 " + type, position: { x: 10, y: 20 }, width: node.width + 10, height: node.height + 10, metadata: { ...node.metadata, locked: true } }]));
    const copy = copyCanvasNodeGraph([node], [], node.id, new Map([[node.id, node.id + "-copy"]]), () => "copy-edge");
    check(type + ":clipboard-paste", base, doc([node, ...copy.nodes]));
    check(type + ":delete", base, empty());
}
const nodesRef = { current: [] as any[] },
    connectionsRef = { current: [] as any[] },
    selectedNodeIdsRef = { current: new Set<string>() };
const setNodes = (value: any) => {
    nodesRef.current = typeof value === "function" ? value(nodesRef.current) : value;
};
const setConnections = (value: any) => {
    connectionsRef.current = typeof value === "function" ? value(connectionsRef.current) : value;
};
let extra: any = {};
let hooks: any;
const noop = () => {};
function Harness() {
    hooks = {
        story: useCanvasStoryboard({ nodesRef, connectionsRef, setNodes, setConnections, setSelectedNodeIds: noop, enqueueGenerationBatch: () => "audit-only-no-worker" }),
        operations: useCanvasNodeOperations({
            projectId: "audit",
            defaultDrawingEngine: "excalidraw",
            nodesRef,
            connectionsRef,
            selectedNodeIdsRef,
            getCanvasCenter: () => ({ x: 0, y: 0 }),
            setNodes,
            setConnections,
            setSelectedNodeIds: (s: any) => (selectedNodeIdsRef.current = s),
            setSelectedConnectionId: noop,
            setContextMenu: noop,
            setDialogNodeId: noop,
            onNodesDeleted: noop,
        }),
        previs: useCanvasPrevis({
            projectId: "audit",
            previsNodeId: null,
            previsScenes: [],
            nodesRef,
            connectionsRef,
            getCanvasCenter: () => ({ x: 0, y: 0 }),
            setNodes,
            setConnections,
            setSelectedNodeIds: noop,
            setSelectedConnectionId: noop,
            setPrevisNodeId: noop,
            updateProject: (_id: any, patch: any) => (extra = { ...extra, ...patch }),
        }),
        batch: useCanvasBatchTable({ nodesRef, connectionsRef, setNodes, setConnections, setSelectedNodeIds: noop, enqueueGenerationBatch: () => "audit-only-no-worker" }),
        style: useCanvasStyleWorkflow({ canvasId: "audit", nodesRef, selectedNodeIdsRef, getCanvasCenter: () => ({ x: 0, y: 0 }), setNodes, setSelectedNodeIds: noop, setSelectedConnectionId: noop, setDialogNodeId: noop, setStylePickerOpen: noop }),
    };
    return null;
}
const root = createRoot(dom.document.createElement("div"));
await act(async () =>
    root.render(
        <QueryClientProvider client={new QueryClient()}>
            <Harness />
        </QueryClientProvider>,
    ),
);
async function run(name: string, nodes: any[], fn: () => any, connections: any[] = []) {
    console.log("AUDIT " + name);
    nodesRef.current = structuredClone(nodes);
    connectionsRef.current = structuredClone(connections);
    extra = {};
    const before = { ...doc(structuredClone(nodes)), connections: structuredClone(connections) };
    try {
        await act(async () => {
            await fn();
        });
        check(name, before, { ...doc(nodesRef.current), connections: connectionsRef.current, ...extra });
    } catch (error) {
        results.push({ name, status: "producer-error", error: String(error) });
    }
}
const rows = [
    createStoryboardRow(1, { plotDescription: "清晨，人物走入篮球馆", imageGenerationPrompt: "清晨篮球馆全景", videoMotionPrompt: "缓缓推进", durationSeconds: 5 }),
    createStoryboardRow(2, { plotDescription: "人物拿起篮球", imageGenerationPrompt: "人物拿球近景", videoMotionPrompt: "固定机位", durationSeconds: 6 }),
];
const board = createCanvasNode(CanvasNodeType.Script, { x: 0, y: 0 }, { storyboard: { rows, referenceNodeIds: [], visibleColumns: ["shotNumber", "plotDescription", "assets"] } });
await run("storyboard:create-images", [board], () => hooks.story.createScriptImageNodes(board.id));
await run("storyboard:create-videos", [board], () => hooks.story.createScriptVideoNodes(board.id));
await run("storyboard:generate-images-draft", [board], () =>
    hooks.story.generateScriptImages(
        board.id,
        rows.map((r) => r.id),
    ),
);
await run("storyboard:generate-videos-draft", [board], () =>
    hooks.story.generateScriptVideos(
        board.id,
        rows.map((r) => r.id),
    ),
);
const directBoard = { ...board, metadata: { ...board.metadata, storyboardVideoInputMode: "direct" } };
await run("storyboard:generate-videos-direct-draft", [directBoard], () =>
    hooks.story.generateScriptVideos(
        board.id,
        rows.map((r) => r.id),
    ),
);
await run("storyboard:create-generate-videos-direct-draft", [directBoard], () => hooks.story.createAndGenerateScriptVideos(board.id));
await run("storyboard:action-board-draft", [board], () => hooks.story.createScriptActionBoards(board.id));
await run("storyboard:add-row", [board], () => hooks.story.addScriptRow(board.id));
await run("storyboard:edit-row", [board], () => hooks.story.updateScriptRow(board.id, rows[0].id, { plotDescription: "更新剧情", durationSeconds: 8 }));
await run("storyboard:remove-row", [board], () => hooks.story.removeScriptRow(board.id, rows[0].id));
const video = createCanvasNode(CanvasNodeType.Video, { x: 1000, y: 0 });
await run("storyboard:connect-row-video", [board, video], () => {
    nodesRef.current = attachNodeToStoryboardRow(nodesRef.current, { fromNodeId: board.id, toNodeId: video.id, fromHandleId: "row:" + rows[0].id });
});
await run("previs:create-shot", [], () => hooks.previs.createPrevisShot("empty"));
for (const template of ["monologue", "dialogue", "blocking", "product"]) await run("previs:template-" + template, [], () => hooks.previs.createPrevisShot(template));
const img = (id: string) => ({ ...createCanvasNode(CanvasNodeType.Image, { x: 0, y: 0 }, { content: "/api/resources/local/file", storageKey: "resource:local" }), id });
const images = [img("a"), img("b")];
const drawing = createCanvasNode(CanvasNodeType.Drawing, { x: 0, y: 0 }, { drawingEngine: "excalidraw" });
const drawingDocument = {
    version: 2 as const,
    engine: "excalidraw" as const,
    snapshot: { type: "excalidraw", version: 2, source: "canvas", elements: [{ id: "shape", type: "rectangle", x: 0, y: 0, width: 40, height: 30 }], appState: {}, files: {} },
    revision: 1,
    updatedAt: "2026-10-26T00:00:00Z",
    shapeCount: 1,
    pageCount: 1,
};
const savedDrawing = { ...drawing, metadata: { ...drawing.metadata, ...canvasDrawingDocumentMetadata(drawing, drawingDocument) } };
check("drawing:save-document", doc([drawing]), doc([savedDrawing]));
const copiedDrawing = copyCanvasNodeGraph([savedDrawing], [], drawing.id, new Map([[drawing.id, drawing.id + "-copy"]]), () => "edge");
check("drawing:document-clipboard-paste", doc([savedDrawing]), doc([savedDrawing, ...copiedDrawing.nodes]));
const conversion = createCanvasNode(CanvasNodeType.MediaConversion, { x: 0, y: 0 });
for (const status of ["processing", "error", "unavailable", "stale"] as const) {
    check("media-conversion:state-" + status, doc([images[0], conversion]), doc([images[0], { ...conversion, metadata: { ...conversion.metadata, mediaConversion: { ...createDefaultMediaConversionState(), status, sourceNodeId: "a" } } }]));
}
check(
    "media-conversion:complete",
    doc([images[0], conversion]),
    doc([
        images[0],
        {
            ...conversion,
            metadata: {
                ...conversion.metadata,
                content: "/api/resources/local/file",
                storageKey: "resource:local",
                mediaConversion: { ...createDefaultMediaConversionState(), status: "completed", sourceNodeId: "a", resultStorageKey: "resource:local", outputKind: "image", resultWidth: 320, resultHeight: 180 },
            },
        },
    ]),
);
const keyframeBoard = { ...board, metadata: { ...board.metadata, storyboardVideoInputMode: "keyframe", storyboard: { ...board.metadata.storyboard, rows: rows.map((row, index) => ({ ...row, imageNodeId: images[index].id })) } } };
await run("storyboard:generate-videos-keyframe-draft", [...images, keyframeBoard], () =>
    hooks.story.generateScriptVideos(
        board.id,
        rows.map((r) => r.id),
    ),
);
await run("group:create-storyboard", images, () => {
    selectedNodeIdsRef.current = new Set(["a", "b"]);
    hooks.operations.createStoryboardGroup();
});
await run("group:create-reference", images, () => {
    selectedNodeIdsRef.current = new Set(["a", "b"]);
    hooks.operations.createReferenceGroup();
});
await run("folder:create", [], () => hooks.operations.createFolder());
await run("style:apply-preset", [], () => hooks.style.applyCanvasStyleAsync({ id: "audit", title: "验收画风", description: "写实", prompt: "低饱和自然光", category: "写实", tags: [], imageUrl: "" }));
const batch = createCanvasNode(
    CanvasNodeType.BatchTable,
    { x: 0, y: 0 },
    {
        batchTable: {
            operation: "try_on",
            concurrency: 5,
            referenceColumns: [
                { id: "reference-1", label: "人物" },
                { id: "reference-2", label: "服装" },
            ],
            rows: [{ id: "batch-row", enabled: true, inputNodeIds: ["a", "b"], prompt: "换装验收" }],
        },
    },
);
const plainBatch = { ...batch, metadata: { ...batch.metadata, batchTable: { ...batch.metadata.batchTable, rows: batch.metadata.batchTable.rows.map((row) => ({ ...row, inputNodeIds: [] })) } } };
await run("batch:add-row", [plainBatch], () => hooks.batch.addRow(batch.id));
await run("batch:edit-row", [plainBatch], () => hooks.batch.updateRow(batch.id, "batch-row", { prompt: "新提示词", enabled: false }));
await run("batch:remove-row", [plainBatch], () => hooks.batch.removeRow(batch.id, "batch-row"));
await run("batch:configure", [plainBatch], () => hooks.batch.patchTable(batch.id, { concurrency: 5, globalPrompt: "全局验收" }));
nodesRef.current = structuredClone([...images, batch]);
connectionsRef.current = [];
const batchBefore = doc(structuredClone(nodesRef.current));
await act(async () => {
    hooks.batch.generateRows(batch.id);
});
await act(async () => {
    await hooks.batch.confirmBatchGenDialog({ imageModel: "audit-model", model: "audit-model", size: "1024x1024", quality: "standard", transparentBackground: "false", concurrency: 5 });
});
check("batch:generate-draft", batchBefore, { ...doc(nodesRef.current), connections: connectionsRef.current });
await act(async () => root.unmount());
const firstShot = createCanvasNode(CanvasNodeType.Video, { x: 0, y: 0 }, { workflowKind: "shot", shotIndex: 1 });
const observations = [
    {
        name: "shot-number-display",
        expected: "镜头 1",
        contextLabel: summarizeCanvasContext([firstShot], new Set([firstShot.id])).shotLabel,
        searchDescription: canvasNodeSearchContext(firstShot),
        wrongNumberMatches: searchCanvasNodes([firstShot], "镜头 2").length,
    },
];
writeFileSync(outputFile, JSON.stringify({ results, notices, observations }, null, 2));
console.log(
    JSON.stringify(
        results.map(({ name, status, error, ops }) => ({ name, status, error, ops: ops?.length })),
        null,
        2,
    ),
);
process.exit(
    results.some((result) => result.status === "error" || result.status === "producer-error") || observations.some((item) => item.contextLabel !== item.expected || item.searchDescription !== item.expected || item.wrongNumberMatches !== 0) ? 1 : 0,
);
