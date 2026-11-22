import { describe, expect, test } from "bun:test";

import { CANVAS_DEFAULT_ANCHOR_RATIO, canvasEdgePresentationFromConnections, withCanvasEdgePresentation } from "@/lib/canvas/canvas-edge-presentation";
import { projectCanvasRelations } from "@/lib/canvas/canvas-node-inputs";
import { resolveCanvasNodeInputIds } from "@/lib/canvas/canvas-node-input-members";
import { mediaReferenceEdgeId, projectedRelationEdgeId, videoFrameEdgeId } from "@/lib/canvas/canvas-relation-edge-id";
import type { CanvasConnection, CanvasNodeData } from "@/types/canvas";

type GoldenEdge = {
    family: "R1" | "R1-frame" | "R1-text";
    fromNodeId: string;
    toNodeId: string;
    field: string;
    id: string;
};

type GoldenCase = {
    name: string;
    nodes: CanvasNodeData[];
    expected: GoldenEdge[];
};

type GoldenFixture = {
    version: number;
    cases: GoldenCase[];
};

const fixture = (await Bun.file(new URL("../../testdata/shortplay-canvas-port/relation-projection.json", import.meta.url)).json()) as GoldenFixture;

describe("shortplay relation projection (shared Go/Web golden)", () => {
    for (const testCase of fixture.cases) {
        test(testCase.name, () => {
            const first = projectCanvasRelations(testCase.nodes);
            const second = projectCanvasRelations(testCase.nodes);
            expect(second).toEqual(first);
            expect(
                first.map((relation) => ({
                    family: relation.family,
                    fromNodeId: relation.fromNodeId,
                    toNodeId: relation.toNodeId,
                    field: relation.field,
                    id: projectedRelationEdgeId(relation),
                })),
            ).toEqual(testCase.expected);
        });
    }

    test("输入成员按字段序，未声明的旧 connections 不补员", () => {
        const nodes: CanvasNodeData[] = [
            { id: "text-1", type: "text", title: "文本", position: { x: 0, y: 0 }, width: 100, height: 100 },
            { id: "image-1", type: "image", title: "图", position: { x: 0, y: 0 }, width: 100, height: 100 },
            { id: "image-2", type: "image", title: "旧边", position: { x: 0, y: 0 }, width: 100, height: 100 },
            {
                id: "video-1",
                type: "video",
                title: "视频",
                position: { x: 0, y: 0 },
                width: 100,
                height: 100,
                metadata: {
                    referenceNodeIds: ["image-1"],
                    videoStartFrameNodeId: "image-1",
                    referenceTextNodeIds: ["text-1"],
                },
            },
        ];
        expect(resolveCanvasNodeInputIds(nodes, "video-1")).toEqual(["image-1", "text-1"]);
    });

    test("边身份按族分化，锚点默认值不落库", () => {
        expect(mediaReferenceEdgeId("image-1", "video-1")).toBe("conn:r1:video-1:image-1");
        expect(videoFrameEdgeId("videoStartFrameNodeId", "image-1", "video-1")).toBe("conn:frame:videoStartFrameNodeId:video-1:image-1");
        const defaultConnection: CanvasConnection = {
            id: "conn-1",
            fromNodeId: "image-1",
            toNodeId: "video-1",
            fromAnchorRatio: CANVAS_DEFAULT_ANCHOR_RATIO,
        };
        expect(canvasEdgePresentationFromConnections([defaultConnection])).toBeUndefined();
        const moved: CanvasConnection = { ...defaultConnection, toAnchorRatio: 0.8 };
        expect(canvasEdgePresentationFromConnections([moved])).toEqual({ "conn-1": { toAnchorRatio: 0.8 } });
        expect(withCanvasEdgePresentation(defaultConnection, { "conn-1": { toAnchorRatio: 0.8 } }).toAnchorRatio).toBe(0.8);
    });
});
