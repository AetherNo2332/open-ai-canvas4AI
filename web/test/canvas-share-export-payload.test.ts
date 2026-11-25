import { describe, expect, test } from "bun:test";

import { normalizeSharedCanvas } from "@/lib/canvas/canvas-relation-normalize";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function node(id: string, type: CanvasNodeType, metadata: CanvasNodeData["metadata"] = {}): CanvasNodeData {
    return { id, type, title: id, position: { x: 0, y: 0 }, width: 320, height: 180, metadata };
}

describe("share payload is field-only", () => {
    test("分享快照只带字段时，页面自己重建边", () => {
        const nodes = [node("image-1", CanvasNodeType.Image), node("video-1", CanvasNodeType.Video, { referenceNodeIds: ["image-1"] })];
        const normalized = normalizeSharedCanvas({ nodes });
        expect(normalized.nodes[1]?.metadata?.referenceNodeIds).toEqual(["image-1"]);
        expect(normalized.connections.map((connection) => `${connection.fromNodeId}->${connection.toNodeId}`)).toEqual(["image-1->video-1"]);
    });

    test("旧分享快照只带 connections 时反解成字段，悬空端点剔除", () => {
        const nodes = [node("image-1", CanvasNodeType.Image), node("video-1", CanvasNodeType.Video)];
        const normalized = normalizeSharedCanvas({
            nodes,
            connections: [
                { id: "legacy", fromNodeId: "image-1", toNodeId: "video-1" },
                { id: "dangling", fromNodeId: "deleted", toNodeId: "video-1" },
            ],
        });
        expect(normalized.nodes[1]?.metadata?.referenceNodeIds).toEqual(["image-1"]);
        expect(normalized.connections).toEqual([{ id: "legacy", fromNodeId: "image-1", toNodeId: "video-1" }]);
    });

    test("同一份字段的有边 / 无边分享快照得到同一投影", () => {
        const nodes = [node("image-1", CanvasNodeType.Image), node("video-1", CanvasNodeType.Video, { referenceNodeIds: ["image-1"] })];
        const withEdge = normalizeSharedCanvas({ nodes, connections: [{ id: "conn:r1:video-1:image-1", fromNodeId: "image-1", toNodeId: "video-1" }] });
        const withoutEdge = normalizeSharedCanvas({ nodes });
        expect(withEdge.nodes).toEqual(withoutEdge.nodes);
        expect(withEdge.connections).toEqual(withoutEdge.connections);
    });

    test("分享快照从安全的节点绑定恢复生成关系并去重", () => {
        const nodes = [
            node("text-1", CanvasNodeType.Text),
            node("image-1", CanvasNodeType.Image),
            node("video-1", CanvasNodeType.Video, {
                videoStartFrameNodeId: "image-1",
                inputBindings: [
                    { id: "prompt", nodeId: "text-1", role: "source-text", mediaType: "text", order: 0, resolution: "latest" },
                    { id: "frame", nodeId: "image-1", role: "first-frame", mediaType: "image", order: 1, resolution: "latest" },
                ],
            }),
        ];
        const normalized = normalizeSharedCanvas({ nodes });
        expect(normalized.connections.map(({ fromNodeId }) => fromNodeId).sort()).toEqual(["image-1", "text-1"]);
    });
});
