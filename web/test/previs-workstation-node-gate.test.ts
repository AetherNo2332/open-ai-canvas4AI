import { describe, expect, test } from "bun:test";

import { canvasNodeHasCustomContent } from "../src/lib/canvas/canvas-node-custom-content";
import { isPrevisWorkstationNode } from "../src/lib/canvas/previs/previs-node";
import { CanvasNodeType, type CanvasNodeData, type CanvasNodeMetadata } from "../src/types/canvas";

function node(type: string, metadata: CanvasNodeMetadata = {}): CanvasNodeData {
    return { id: "node-1", type, title: "节点", position: { x: 0, y: 0 }, width: 360, height: 220, metadata } as unknown as CanvasNodeData;
}

describe("预演台工作站判定", () => {
    test("只有 workflowKind=shot 的节点是可打开的工作站", () => {
        expect(isPrevisWorkstationNode(node(CanvasNodeType.Video, { workflowKind: "shot", previsSceneId: "scene" }))).toBe(true);
        // 服务端回写的产物节点同样带 previsSceneId，但不是工作站
        expect(isPrevisWorkstationNode(node(CanvasNodeType.Video, { workflowKind: "reference_video", previsSceneId: "scene" }))).toBe(false);
        expect(isPrevisWorkstationNode(node(CanvasNodeType.Image, { workflowKind: "reference_set", previsSceneId: "scene" }))).toBe(false);
        // 存量脏数据：只有 previsSceneId、没有 workflowKind
        expect(isPrevisWorkstationNode(node(CanvasNodeType.Video, { previsSceneId: "scene" }))).toBe(false);
        expect(isPrevisWorkstationNode(null)).toBe(false);
    });

    test("产物节点继续走媒体渲染器，不会被渲染成预演台卡片", () => {
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Image, { workflowKind: "reference_set", previsSceneId: "scene", content: "https://example.test/a.png" }), false)).toBe(false);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Video, { workflowKind: "reference_video", previsSceneId: "scene", storageKey: "resource:a" }), false)).toBe(false);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Video, { previsSceneId: "scene" }), false)).toBe(false);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Video, { workflowKind: "shot", previsSceneId: "scene" }), false)).toBe(true);
    });

    test("其他自定义面板分支保持不变", () => {
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Script), false)).toBe(true);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Config), false)).toBe(true);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.BatchTable), false)).toBe(true);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Image, { workflowKind: "character", characterAssetId: "asset-1" }), false)).toBe(true);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Text, { workflowKind: "story_input" }), true)).toBe(false);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Text, { workflowKind: "story_input" }), false)).toBe(true);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Image, { workflowKind: "styleboard", content: "x" }), false)).toBe(false);
        expect(canvasNodeHasCustomContent(node(CanvasNodeType.Image, { workflowKind: "styleboard" }), false)).toBe(true);
    });
});
