import { describe, expect, test } from "bun:test";

import { applyFrameDrop } from "@/lib/canvas/canvas-frame";
import { isolateCopiedNodeMetadata } from "@/lib/canvas/canvas-node-copy";
import { allocateCopiedCanvasNodeIds, countSubmapExternalReferences, isCopiedSubmapTreeNode, submapCopySources } from "@/lib/canvas/canvas-submap-operations";
import { removeCanvasNodes } from "@/lib/canvas/canvas-project-domain";
import { canvasNodeDisplayPath, canvasSubmapLocalId, getSubmapChildIds, getSubmapChildren, isNodeHiddenByCollapsedSubmap, isSubmapNode } from "@/lib/canvas/canvas-submap";
import { clampSubmapChildPositions, fitSubmapToChildren, nextSubmapChildPosition, SUBMAP_CHILD_GAP } from "@/lib/canvas/canvas-submap-layout";
import { createSequentialIdAllocator } from "@/lib/canvas/canvas-node-id";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function submap(id = "submap-1", collapsed = false): CanvasNodeData {
    return { id, type: CanvasNodeType.Submap, title: "镜头 1", position: { x: 0, y: 0 }, width: 920, height: 360, metadata: { collapsed } };
}

function child(id: string, type: CanvasNodeType, scopeId: string, extra: Partial<CanvasNodeData> = {}): CanvasNodeData {
    return { id, type, title: id, position: { x: 40, y: 60 }, width: 200, height: 120, scopeId, ...extra };
}

describe("submap 作用域", () => {
    test("子节点按 scopeId 归属，不按 parentId", () => {
        const nodes = [submap(), child("submap-1__image-1", CanvasNodeType.Image, "submap-1"), child("submap-1__text-1", CanvasNodeType.Text, "submap-1"), child("image-9", CanvasNodeType.Image, "")];
        expect(getSubmapChildren("submap-1", nodes).map((node) => node.id)).toEqual(["submap-1__image-1", "submap-1__text-1"]);
        expect(getSubmapChildIds("submap-1", nodes).has("image-9")).toBe(false);
    });

    test("localId 与展示路径由作用域前缀派生", () => {
        expect(canvasSubmapLocalId("submap-1", "submap-1__image-1")).toBe("image-1");
        expect(canvasSubmapLocalId("submap-1", "submap-12__image-1")).toBe("submap-12__image-1");
        expect(canvasNodeDisplayPath(child("submap-1__image-1", CanvasNodeType.Image, "submap-1"))).toBe("submap-1 / image-1");
        expect(canvasNodeDisplayPath(child("image-1", CanvasNodeType.Image, ""))).toBe("image-1");
    });

    test("折叠只隐藏子节点，不改归属", () => {
        const collapsed = submap("submap-1", true);
        const nodes = [collapsed, child("submap-1__image-1", CanvasNodeType.Image, "submap-1")];
        expect(isNodeHiddenByCollapsedSubmap(nodes[1], nodes)).toBe(true);
        expect(nodes[1].scopeId).toBe("submap-1");
        expect(isSubmapNode(nodes[0])).toBe(true);
    });

    test("拖动子节点时夹紧到容器内容区，拖动容器不触发夹紧", () => {
        const nodes = [submap(), child("submap-1__image-1", CanvasNodeType.Image, "submap-1", { position: { x: 9999, y: -500 } })];
        const clamped = clampSubmapChildPositions(nodes, new Set(["submap-1__image-1"]));
        const moved = clamped.find((node) => node.id === "submap-1__image-1")!;
        expect(moved.position.x).toBeLessThanOrEqual(920 - 24 - moved.width);
        expect(moved.position.y).toBeGreaterThanOrEqual(36 + 24);
        expect(moved.scopeId).toBe("submap-1");
        expect(clampSubmapChildPositions(nodes, new Set(["submap-1"]))).toBe(nodes);
    });
});

describe("submap 布局", () => {
    test("容器尺寸撑到包住子节点并保留标题栏与内边距", () => {
        const nodes = [submap(), child("submap-1__image-1", CanvasNodeType.Image, "submap-1", { position: { x: 40, y: 60 } }), child("submap-1__image-2", CanvasNodeType.Image, "submap-1", { position: { x: 1000, y: 700 } })];
        const fitted = fitSubmapToChildren(nodes, "submap-1").find((node) => node.id === "submap-1")!;
        expect(fitted.width).toBeGreaterThanOrEqual(1224);
        expect(fitted.height).toBeGreaterThanOrEqual(844);
    });

    test("只增不减，空容器不变，新子节点在底部另起一行", () => {
        const big = { ...submap(), width: 3000, height: 2000 };
        const one = child("submap-1__image-1", CanvasNodeType.Image, "submap-1");
        expect(fitSubmapToChildren([big, one], "submap-1")[0]).toBe(big);
        const empty = [submap()];
        expect(fitSubmapToChildren(empty, "submap-1")).toBe(empty);
        expect(nextSubmapChildPosition(submap(), [one])).toEqual({ x: 40, y: 60 + 120 + SUBMAP_CHILD_GAP });
    });
});

describe("submap 删除、复制与引用", () => {
    test("删除 submap 级联删除全部子节点，删除子节点不影响容器", () => {
        const nodes = [submap(), child("submap-1__image-1", CanvasNodeType.Image, "submap-1"), child("submap-1__text-1", CanvasNodeType.Text, "submap-1"), child("image-9", CanvasNodeType.Image, "")];
        const result = removeCanvasNodes(nodes, new Set(["submap-1"]));
        expect([...result.removedIds]).toEqual(expect.arrayContaining(["submap-1", "submap-1__image-1", "submap-1__text-1"]));
        expect(result.nodes.map((node) => node.id)).toEqual(["image-9"]);
        expect(removeCanvasNodes(nodes, new Set(["submap-1__image-1"])).nodes.map((node) => node.id)).toEqual(["submap-1", "submap-1__text-1", "image-9"]);
    });

    test("复制整棵子树，外部引用计数覆盖两个参考族与首尾帧", () => {
        const nodes = [
            submap(),
            child("submap-1__image-1", CanvasNodeType.Image, "submap-1", { metadata: { referenceNodeIds: ["image-9", "submap-1__image-1"] } }),
            child("submap-1__video-1", CanvasNodeType.Video, "submap-1", { metadata: { videoStartFrameNodeId: "image-9", videoEndFrameNodeId: "submap-1__image-1", referenceTextNodeIds: ["text-9"] } }),
        ];
        expect(submapCopySources(nodes[0], nodes).map((node) => node.id)).toEqual(["submap-1", "submap-1__image-1", "submap-1__video-1"]);
        expect(countSubmapExternalReferences(nodes, new Set(["submap-1", "submap-1__image-1", "submap-1__video-1"]))).toBe(3);
    });

    test("复制 submap 时容器自身也属于独立作用域，无关顶层节点不属于", () => {
        const nodes = [submap(), child("submap-1__image-1", CanvasNodeType.Image, "submap-1"), child("image-9", CanvasNodeType.Image, "")];
        const copiedSubmapIds = new Set(["submap-1"]);
        expect(isCopiedSubmapTreeNode(nodes[0], copiedSubmapIds)).toBe(true);
        expect(isCopiedSubmapTreeNode(nodes[1], copiedSubmapIds)).toBe(true);
        expect(isCopiedSubmapTreeNode(nodes[2], copiedSubmapIds)).toBe(false);
    });

    test("复制后原件和副本分别引用自己的子节点，外部引用不复制", () => {
        const sources = [
            submap(),
            child("submap-1__text-1", CanvasNodeType.Text, "submap-1", { metadata: { content: "提示词" } }),
            child("submap-1__image-1", CanvasNodeType.Image, "submap-1", { metadata: { referenceNodeIds: ["submap-1__text-1", "image-9"] } }),
            child("submap-1__video-1", CanvasNodeType.Video, "submap-1", { metadata: { videoStartFrameNodeId: "submap-1__image-1", referenceTextNodeIds: ["submap-1__text-1", "text-9"] } }),
        ];
        const allocate = createSequentialIdAllocator(sources.map((node) => node.id));
        const idMap = allocateCopiedCanvasNodeIds(sources, allocate);
        const copy = sources.slice(1).map((source) => ({
            ...source,
            id: idMap.get(source.id)!,
            scopeId: idMap.get(source.scopeId!)!,
            metadata: isolateCopiedNodeMetadata(source, idMap, { dropExternalReferences: true }),
        }));
        expect(copy.find((node) => node.id === "submap-2__image-1")?.metadata?.referenceNodeIds).toEqual(["submap-2__text-1"]);
        expect(copy.find((node) => node.id === "submap-2__video-1")?.metadata?.videoStartFrameNodeId).toBe("submap-2__image-1");
        expect(copy.find((node) => node.id === "submap-2__video-1")?.metadata?.referenceTextNodeIds).toEqual(["submap-2__text-1"]);
        expect(sources[2].metadata?.referenceNodeIds).toEqual(["submap-1__text-1", "image-9"]);
    });

    test("拖进背板不会改变 submap 子节点归属", () => {
        const frame: CanvasNodeData = { id: "frame-1", type: CanvasNodeType.Frame, title: "背板", position: { x: 0, y: 0 }, width: 800, height: 600, metadata: { frame: { collapsed: false, expandedWidth: 800, expandedHeight: 600 } } };
        const nodes = [frame, submap(), child("submap-1__image-1", CanvasNodeType.Image, "submap-1")];
        const next = applyFrameDrop(nodes, new Set(["submap-1__image-1"]), "frame-1");
        expect(next.find((node) => node.id === "submap-1__image-1")?.parentId).toBeUndefined();
        expect(next.find((node) => node.id === "submap-1__image-1")?.scopeId).toBe("submap-1");
    });
});
