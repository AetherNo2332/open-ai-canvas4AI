import { describe, expect, test } from "bun:test";

import { buildDramaSegmentGroup, materializeDramaSegmentReferences } from "@/lib/canvas/drama-canvas-projection";
import { projectReferenceConnections } from "@/lib/canvas/canvas-reference-fields";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData } from "@/types/canvas";
import type { DramaSegment } from "@/services/api/drama";

function node(id: string, type: CanvasNodeType, metadata: CanvasNodeData["metadata"] = {}): CanvasNodeData {
    return { id, type, title: id, position: { x: 0, y: 0 }, width: 100, height: 100, metadata };
}

function connection(fromNodeId: string, toNodeId: string, id = `conn-${fromNodeId}`): CanvasConnection {
    return { id, fromNodeId, toNodeId };
}

describe("短剧投送引用物化", () => {
    test("参考图、文本和首帧分别写入字段与 generationSpec", () => {
        const nodes = [
            node("text-1", CanvasNodeType.Text, { content: "台词" }),
            node("image-1", CanvasNodeType.Image),
            node("image-2", CanvasNodeType.Image),
            node("video-1", CanvasNodeType.Video, {
                prompt: "雨夜回眸",
                composerContent: "雨夜回眸",
                generationMode: "video",
                videoStartFrameNodeId: "image-2",
            }),
        ];
        const connections = [connection("image-1", "video-1"), connection("text-1", "video-1"), connection("image-2", "video-1")];

        const result = materializeDramaSegmentReferences(nodes, connections, 1);
        const video = result.nodes.find((item) => item.id === "video-1")!;

        expect(video.metadata?.referenceNodeIds).toEqual(["image-1"]);
        expect(video.metadata?.referenceTextNodeIds).toEqual(["text-1"]);
        expect(video.metadata?.videoStartFrameNodeId).toBe("image-2");
        expect(video.metadata?.generationSpec?.referenceBindings.map((binding) => [binding.nodeId, binding.role, binding.order])).toEqual([
            ["image-1", "reference", 0],
            ["text-1", "source-text", 1],
            ["image-2", "first-frame", 2],
        ]);
        expect(result.connections.map((item) => item.fromNodeId)).toEqual(["image-1", "text-1", "image-2"]);
        expect(result.connections.every((item) => item.managedBy === "reference-fields-v1")).toBe(true);
    });

    test("连线顺序决定参考图编号，首帧不重复进入普通引用", () => {
        const nodes = [node("image-1", CanvasNodeType.Image), node("image-2", CanvasNodeType.Image), node("image-3", CanvasNodeType.Image), node("video-1", CanvasNodeType.Video, { composerContent: "镜头", videoStartFrameNodeId: "image-3" })];
        const connections = [connection("image-2", "video-1"), connection("image-1", "video-1"), connection("image-3", "video-1")];

        const result = materializeDramaSegmentReferences(nodes, connections, 1);
        const video = result.nodes.find((item) => item.id === "video-1")!;

        expect(video.metadata?.referenceNodeIds).toEqual(["image-2", "image-1"]);
        expect(video.metadata?.generationSpec?.referenceBindings.map((binding) => binding.nodeId)).toEqual(["image-2", "image-1", "image-3"]);
    });

    test("旧格式文档原样返回", () => {
        const nodes = [node("image-1", CanvasNodeType.Image), node("video-1", CanvasNodeType.Video, { composerContent: "镜头" })];
        const connections = [connection("image-1", "video-1")];

        const result = materializeDramaSegmentReferences(nodes, connections, undefined);

        expect(result.nodes).toBe(nodes);
        expect(result.connections).toBe(connections);
        expect(result.nodes[1].metadata?.generationSpec).toBeUndefined();
    });

    test("剧情段整组投送后视频引用自身首帧和参考图", () => {
        const segment = {
            id: "seg-1",
            revision: 1,
            index: 0,
            title: "雨夜",
            shotIds: [],
            durationMs: 5000,
            videoPrompt: "雨夜天台回眸",
            generated: "",
            custom: false,
            promptStatus: "ready",
            videoStatus: "ready",
            videoModel: {},
            referenceMode: "auto",
            audioReferenceMode: "auto",
            request: {
                operation: "image_to_video",
                seconds: 5,
                maxVideoSeconds: 10,
                aspectRatio: "16:9",
                generateAudio: false,
                firstFrameResourceId: "res-frame",
                firstFramePending: false,
                references: [
                    { castId: "cast-1", label: "参考图1", kind: "character", name: "阿岚", resourceId: "res-1", sent: true },
                    { castId: "cast-2", label: "参考图2", kind: "scene", name: "天台", resourceId: "res-2", sent: true },
                ],
                referenceCapacity: 2,
                modelMaxImages: 2,
                header: "保持角色一致",
            },
        } as unknown as DramaSegment;
        const group = buildDramaSegmentGroup({
            projectId: "drama-1",
            episode: { id: "episode-1", number: 1 },
            segment,
            shots: [],
            castById: new Map(),
            aspectRatio: "16:9",
            media: { assetIdOf: (resourceId) => `asset:${resourceId}`, urlOf: (resourceId) => `https://files/${resourceId}` },
        });

        const result = materializeDramaSegmentReferences(group.nodes, group.connections, 1);
        const video = result.nodes.find((item) => item.id === group.videoNodeId)!;
        const frames = result.nodes.filter((item) => item.type === CanvasNodeType.Image).map((item) => item.id);

        expect(video.metadata?.referenceNodeIds).toEqual(frames.slice(0, 2));
        expect(video.metadata?.videoStartFrameNodeId).toBe(frames[2]);
        expect(video.metadata?.referenceNodeIds).not.toContain(frames[2]);
        expect(video.metadata?.generationSpec?.referenceBindings.map((binding) => binding.role)).toEqual(["reference", "reference", "first-frame"]);
        expect(result.connections).toHaveLength(3);
    });

    test("字段文档只保存节点时，短剧投送重开仍恢复引用边", () => {
        const nodes = [node("image-1", CanvasNodeType.Image), node("image-2", CanvasNodeType.Image), node("video-1", CanvasNodeType.Video, { composerContent: "镜头", videoStartFrameNodeId: "image-2" })];
        const materialized = materializeDramaSegmentReferences(nodes, [connection("image-1", "video-1"), connection("image-2", "video-1")], 1);
        const reopenedNodes = JSON.parse(JSON.stringify(materialized.nodes)) as CanvasNodeData[];

        const reopened = projectReferenceConnections(reopenedNodes, [], 1);

        expect(reopened.map((item) => item.fromNodeId)).toEqual(["image-1", "image-2"]);
        expect(reopened.every((item) => item.managedBy === "reference-fields-v1")).toBe(true);
        expect(reopenedNodes.find((item) => item.id === "video-1")?.metadata?.referenceNodeIds).toEqual(["image-1"]);
    });
});
