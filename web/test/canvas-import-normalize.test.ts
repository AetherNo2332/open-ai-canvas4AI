import { describe, expect, test } from "bun:test";

import { normalizeImportedCanvasRelations } from "../src/lib/canvas/canvas-import-normalize";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";

function node(overrides: Partial<CanvasNodeData> = {}): CanvasNodeData {
    return { id: "n", type: CanvasNodeType.Text, title: "t", position: { x: 0, y: 0 }, width: 100, height: 100, ...overrides };
}

describe("canvas import relation normalize", () => {
    test("旧查看器入边反解成字段并保留文本内容", () => {
        const text = node({ id: "text-1", metadata: { content: "title,value" } });
        const nodes = [text, node({ id: "chart-1", type: CanvasNodeType.Chart })];
        const result = normalizeImportedCanvasRelations(nodes, [{ id: "old", fromNodeId: "text-1", toNodeId: "chart-1" }]);
        expect(result[0]).toBe(text);
        expect(result[1].metadata?.referenceTextNodeIds).toEqual(["text-1"]);
    });
    test("旧文档只带边时把媒体入边反解成 referenceNodeIds", () => {
        const nodes = [node({ id: "image-1", type: CanvasNodeType.Image }), node({ id: "video-1", type: CanvasNodeType.Video })];
        const result = normalizeImportedCanvasRelations(nodes, [{ id: "conn-1", fromNodeId: "image-1", toNodeId: "video-1" }]);
        expect(result.find((item) => item.id === "video-1")?.metadata?.referenceNodeIds).toEqual(["image-1"]);
    });

    test("已有字段项在前，边序补在末尾；同一来源只保留一次", () => {
        const nodes = [node({ id: "image-1", type: CanvasNodeType.Image }), node({ id: "image-2", type: CanvasNodeType.Image }), node({ id: "video-1", type: CanvasNodeType.Video, metadata: { referenceNodeIds: ["image-2"] } })];
        const result = normalizeImportedCanvasRelations(nodes, [
            { id: "a", fromNodeId: "image-2", toNodeId: "video-1" },
            { id: "b", fromNodeId: "image-1", toNodeId: "video-1" },
            { id: "c", fromNodeId: "image-1", toNodeId: "video-1" },
        ]);
        expect(result.find((item) => item.id === "video-1")?.metadata?.referenceNodeIds).toEqual(["image-2", "image-1"]);
    });

    test("文本目标与悬空来源都不写字段", () => {
        const nodes = [node({ id: "image-1", type: CanvasNodeType.Image }), node({ id: "video-1", type: CanvasNodeType.Video }), node({ id: "text-1" })];
        const result = normalizeImportedCanvasRelations(nodes, [
            { id: "text", fromNodeId: "image-1", toNodeId: "text-1" },
            { id: "dangling", fromNodeId: "missing", toNodeId: "video-1" },
        ]);
        expect(result).toBe(nodes);
    });

    test("带 handle 或业务关系类型的工作流边不反解成参考字段", () => {
        const nodes = [node({ id: "image-1", type: CanvasNodeType.Image }), node({ id: "video-1", type: CanvasNodeType.Video })];
        const result = normalizeImportedCanvasRelations(nodes, [
            { id: "row-output", fromNodeId: "image-1", toNodeId: "video-1", fromHandleId: "row:shot", relation: "storyboard-output" },
            { id: "to-column", fromNodeId: "image-1", toNodeId: "video-1", toHandleId: "batch-cell:col" },
        ]);
        expect(result).toBe(nodes);
    });

    test("边已全部落字段时返回原引用", () => {
        const nodes = [node({ id: "image-1", type: CanvasNodeType.Image }), node({ id: "video-1", type: CanvasNodeType.Video, metadata: { referenceNodeIds: ["image-1"] } })];
        expect(normalizeImportedCanvasRelations(nodes, [{ id: "conn-1", fromNodeId: "image-1", toNodeId: "video-1" }])).toBe(nodes);
    });

    test("已有声明里指向本批之外的项原样保留", () => {
        const nodes = [node({ id: "image-1", type: CanvasNodeType.Image }), node({ id: "video-1", type: CanvasNodeType.Video, metadata: { referenceNodeIds: ["existing-canvas-node"] } })];
        const result = normalizeImportedCanvasRelations(nodes, [{ id: "conn-1", fromNodeId: "image-1", toNodeId: "video-1" }]);
        expect(result.find((item) => item.id === "video-1")?.metadata?.referenceNodeIds).toEqual(["existing-canvas-node", "image-1"]);
    });
});
