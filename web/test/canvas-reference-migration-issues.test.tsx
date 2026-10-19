import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { CanvasReferenceMigrationContent } from "@/components/canvas/canvas-reference-migration-panel";
import { canvasReferenceMigrationIssues } from "@/lib/canvas/canvas-reference-migration-issues";
import type { CanvasNodeData } from "@/types/canvas";

const node = (id: string, metadata: CanvasNodeData["metadata"] = {}): CanvasNodeData => ({
    id,
    title: "雨夜参考图",
    type: "image",
    position: { x: 0, y: 0 },
    width: 300,
    height: 200,
    metadata,
});
const render = (nodes: CanvasNodeData[], extra = {}) =>
    renderToStaticMarkup(
        createElement(CanvasReferenceMigrationContent, {
            nodes,
            conflicts: [],
            choices: {},
            onChoose() {},
            onCancel() {},
            onApply() {},
            onLocate() {},
            ...extra,
        }),
    );

test("self references identify the owner with its image and require manual repair", () => {
    const nodes = [node("image-1", { content: "https://example.com/rain.png", inputBindings: [{ id: "self", nodeId: "image-1", mediaType: "image", role: "reference", resolution: "latest", order: 0 }] })];
    const before = structuredClone(nodes);
    const html = render(nodes);
    expect(html).toContain("雨夜参考图");
    expect(html).toContain("https://example.com/rain.png");
    expect(html).toContain("引用了自己");
    expect(html).toContain("定位到画布");
    expect(html).toContain("重新选择");
    expect(html).not.toContain("直接完成");
    expect(html).toMatch(/<button[^>]*disabled[^>]*>[\s\S]*?确认并升级/);
    expect(nodes).toEqual(before);
});

test("missing character references locate the actual storyboard row, not the missing node", () => {
    const board: CanvasNodeData = { ...node("board", { storyboard: { rows: [{ id: "row-internal", shotNumber: 3, characters: [{ characterName: "女主角", characterImageNodeId: "deleted-image" }] } as never] } }), type: "script", title: "雨夜分镜" };
    const html = render([board]);
    expect(html).toContain("雨夜分镜");
    expect(html).toContain("第 1 行");
    expect(html).toContain("出镜角色");
    expect(html).toContain("女主角");
    expect(html).toContain("素材已不存在");
    expect(html).toContain("无法显示原图");
    expect(html).not.toContain("没有需要");
});

test("valid references remain eligible for migration, even when media previews cannot load", () => {
    const html = render([node("image-1", { inputBindings: [{ id: "ref", nodeId: "image-2", mediaType: "image", role: "reference", resolution: "latest", order: 0 }] }), { ...node("image-2"), title: "场景图" }]);
    expect(html).not.toContain("需要手动处理");
    expect(html).toContain("确认并升级");
    expect(html).not.toContain("disabled");
});

test("a migration error never appears alongside a claim that migration can finish directly", () => {
    const html = render([], { error: "网络中断，请重试" });
    expect(html).toContain("网络中断");
    expect(html).not.toContain("直接完成");
    expect(html).not.toContain("没有需要逐项选择");
});

test("diagnostics report all broken input positions without treating resource-only references as missing nodes", () => {
    const nodes = [
        node("image-1", {
            generationSpec: {
                version: 1,
                mode: "image",
                prompt: "",
                options: {},
                textInputMode: "prompt-only",
                referenceBindings: [
                    { id: "first", nodeId: "deleted", role: "first-frame", mediaType: "image", order: 0, resolution: "latest" },
                    { id: "mask", nodeId: "image-1", role: "mask", mediaType: "image", order: 1, resolution: "latest" },
                    { id: "resource", resourceId: "resource-id", role: "reference", mediaType: "image", order: 2, resolution: "latest" },
                ],
            },
        }),
    ];
    expect(canvasReferenceMigrationIssues(nodes)).toMatchObject([
        { ownerNodeId: "image-1", sourceNodeId: "deleted", fieldLabel: "首帧图片", kind: "missing" },
        { ownerNodeId: "image-1", sourceNodeId: "image-1", fieldLabel: "遮罩图片", kind: "self" },
    ]);
});

test("manual conflict choices never hide independently declared character references", () => {
    const board: CanvasNodeData = {
        ...node("board", { storyboard: { rows: [{ id: "row-1", shotNumber: 1, assetBindings: [{ nodeId: "lost-asset", role: "prop", priority: 1 }], characters: [{ characterName: "女主角", characterImageNodeId: "lost-character" }] } as never] } }),
        type: "script",
    };
    expect(canvasReferenceMigrationIssues([board])).toHaveLength(2);
    expect(canvasReferenceMigrationIssues([board], { "storyboard-row:board:row-1": "connections" })).toMatchObject([{ sourceNodeId: "lost-character", fieldLabel: "出镜角色" }]);
});

test("batch text and image references point at the owning table row", () => {
    const table: CanvasNodeData = { ...node("table", { batchTable: { operation: "creative", concurrency: 1, rows: [{ id: "r", enabled: true, inputNodeIds: ["lost-image"], textNodeIds: ["lost-text"], prompt: "" }] } }), type: "batch-table" };
    expect(canvasReferenceMigrationIssues([table])).toMatchObject([
        { ownerNodeId: "table", sourceNodeId: "lost-image", location: "第 1 行", fieldLabel: "参考图片" },
        { ownerNodeId: "table", sourceNodeId: "lost-text", location: "第 1 行", fieldLabel: "参考文本" },
    ]);
    // 旧连线可能只覆盖图片列，不能因此隐藏同一行尚未修复的文本引用。
    expect(canvasReferenceMigrationIssues([table], { "batch-row:table:r": "connections" })).toHaveLength(2);
});
