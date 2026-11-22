import { describe, expect, test } from "bun:test";

import { allocateSequentialId, createSequentialIdAllocator, nextSequentialId, prepareSequentialIdBatch, seedSequentialCounters, type SequentialCounters } from "@/lib/canvas/canvas-node-id";
import { allocateCopiedCanvasNodeIds } from "@/lib/canvas/canvas-submap-operations";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function node(id: string, type: CanvasNodeData["type"] = CanvasNodeType.Text): CanvasNodeData {
    return { id, type, title: id, position: { x: 0, y: 0 }, width: 320, height: 180, metadata: {} };
}

describe("画布顺序 ID", () => {
    test("不复用已删除编号，历史随机串不参与命名空间", () => {
        expect(nextSequentialId(["image-1", "image-2", "image-9"], "image")).toBe("image-10");
        expect(nextSequentialId(["image-1", "text-4"], "image")).toBe("image-2");
        expect(nextSequentialId(["image-1730000000000-a1b2c"], "image")).toBe("image-1");
    });

    test("同批分配与 reserve 都会推进高水位", () => {
        const allocate = createSequentialIdAllocator(["image-1"]);
        expect(allocate("image")).toBe("image-2");
        expect(allocate("image")).toBe("image-3");
        allocate.reserve("video-7");
        expect(allocate("video")).toBe("video-8");
        expect(allocate.counters).toEqual({ image: 3, video: 8 });
    });

    test("持久高水位优先于存活集合，删掉最大编号也不发回", () => {
        expect(nextSequentialId(["image-7", "image-8"], "image", { image: 10 })).toBe("image-11");
        const counters: SequentialCounters = { image: 10 };
        const first = createSequentialIdAllocator(["image-7"], counters);
        expect(first("image")).toBe("image-11");
        const second = createSequentialIdAllocator(["image-7"], counters);
        expect(second("image")).toBe("image-12");
    });

    test("seed 只识别明确顺序 ID", () => {
        expect(seedSequentialCounters(["image-1", "image-9", "text-4", "image-1730000000000-a1b2c"])).toEqual({ image: 9, text: 4 });
    });

    test("重置子画布作用域只清除该 scope 的复合高水位", () => {
        const nodes = [{ id: "submap-1" }, { id: "submap-1__text-8" }, { id: "submap-1__image-2" }, { id: "submap-2__text-3" }, { id: "text-9" }];
        const persisted = { "submap-1__text": 8, "submap-1__image": 2, "submap-2__text": 3, text: 9 };
        const { taken, counters } = prepareSequentialIdBatch(nodes, [], [], persisted, "submap-1");
        expect(taken).toEqual(["submap-1", "submap-2__text-3", "text-9"]);
        expect(counters).toEqual({ submap: 1, "submap-2__text": 3, text: 9 });
        const allocate = createSequentialIdAllocator(taken, counters);
        expect(allocate("submap-1__text")).toBe("submap-1__text-1");
        expect(allocate("submap-1__image")).toBe("submap-1__image-1");
        expect(allocate("submap-2__text")).toBe("submap-2__text-4");
    });

    test("分配→写回→删除→再分配仍不复用", () => {
        const canvas: { nodes: Array<{ id: string }>; connections: Array<{ id: string }>; idCounters?: SequentialCounters } = {
            nodes: [node("image-1"), node("image-2")],
            connections: [],
        };
        const created = allocateSequentialId(canvas, "image");
        canvas.idCounters = created.counters;
        canvas.nodes.push(node(created.id));
        canvas.nodes = canvas.nodes.filter((item) => item.id !== created.id);
        expect(allocateSequentialId(canvas, "image").id).toBe("image-4");
    });

    test("复制 submap 先发容器号再按新容器发子节点号", () => {
        const nodes = [
            { ...node("submap-1", CanvasNodeType.Submap), scopeId: undefined },
            { ...node("submap-1__text-1", CanvasNodeType.Text), scopeId: "submap-1" },
            { ...node("submap-1__video-1", CanvasNodeType.Video), scopeId: "submap-1" },
        ];
        const allocate = createSequentialIdAllocator(nodes.map((item) => item.id));
        const idMap = allocateCopiedCanvasNodeIds(nodes, allocate);
        expect(idMap.get("submap-1")).toBe("submap-2");
        expect(idMap.get("submap-1__text-1")).toBe("submap-2__text-1");
        expect(idMap.get("submap-1__video-1")).toBe("submap-2__video-1");
    });
});
