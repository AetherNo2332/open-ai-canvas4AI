import { describe, expect, test } from "bun:test";

import {
    MAX_USER_SUBMAP_PRESET_BYTES,
    MAX_USER_SUBMAP_PRESET_NAME,
    MAX_USER_SUBMAP_PRESET_NODES,
    exportSubmapSnapshot,
    instantiateSubmapSnapshot,
    snapshotRefIds,
    userSubmapPresetBytes,
    validateUserSubmapSnapshot,
} from "@/lib/canvas/canvas-submap-snapshots";
import { projectReferenceConnections } from "@/lib/canvas/canvas-reference-fields";
import type { UserSubmapPresetSnapshot } from "@/services/api/submap-presets";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata } from "@/types/canvas";

function node(id: string, type: CanvasNodeType, metadata: CanvasNodeMetadata = {}, scopeId?: string): CanvasNodeData {
    return { id, type, title: id, position: { x: 0, y: 0 }, width: 320, height: 180, scopeId, metadata };
}

function submap(id = "submap-1"): CanvasNodeData {
    return { ...node(id, CanvasNodeType.Submap), title: "镜头 1", width: 920, height: 600 };
}

function sequentialAllocator() {
    const counters = new Map<string, number>();
    return (prefix: string) => {
        const next = (counters.get(prefix) || 0) + 1;
        counters.set(prefix, next);
        return `${prefix}-${next}`;
    };
}

function sourceNodes(): CanvasNodeData[] {
    const videoSpec = {
        version: 1 as const,
        mode: "video" as const,
        prompt: "视频提示词",
        options: {},
        textInputMode: "prompt-only" as const,
        referenceBindings: [
            { id: "node:submap-1__text-1", nodeId: "submap-1__text-1", mediaType: "text" as const, role: "source-text" as const, order: 0, resolution: "latest" as const },
            { id: "node:text-9", nodeId: "text-9", mediaType: "text" as const, role: "source-text" as const, order: 1, resolution: "latest" as const },
            { id: "node:submap-1__image-1", nodeId: "submap-1__image-1", mediaType: "image" as const, role: "first-frame" as const, order: 2, resolution: "latest" as const },
        ],
    };
    return [
        submap(),
        node(
            "submap-1__text-1",
            CanvasNodeType.Text,
            {
                content: "台词正文",
                referenceNodeIds: ["submap-1__image-1", "image-outside"],
                referenceTextNodeIds: ["submap-1__text-2", "text-outside"],
                inputBindings: [
                    { id: "node:submap-1__text-2", nodeId: "submap-1__text-2", mediaType: "text", role: "source-text", order: 0, resolution: "latest" },
                    { id: "node:text-outside", nodeId: "text-outside", mediaType: "text", role: "source-text", order: 1, resolution: "latest" },
                ],
            },
            "submap-1",
        ),
        node(
            "submap-1__image-1",
            CanvasNodeType.Image,
            {
                generationSpec: {
                    version: 1,
                    mode: "image",
                    prompt: "油画风格",
                    options: {},
                    textInputMode: "append-sources",
                    referenceBindings: [{ id: "node:submap-1__text-1", nodeId: "submap-1__text-1", mediaType: "text", role: "source-text", order: 0, resolution: "latest" }],
                },
                referenceTextNodeIds: ["submap-1__text-1"],
                videoStartFrameNodeId: "submap-1__text-2",
                videoEndFrameNodeId: "frame-outside",
            },
            "submap-1",
        ),
        node("submap-1__text-2", CanvasNodeType.Text, { content: "参考" }, "submap-1"),
        node(
            "submap-1__video-1",
            CanvasNodeType.Video,
            {
                generationSpec: videoSpec,
                referenceTextNodeIds: ["submap-1__text-1"],
                videoStartFrameNodeId: "submap-1__image-1",
            },
            "submap-1",
        ),
        node("image-outside", CanvasNodeType.Image, { content: "组外" }),
    ];
}

function snapshotWithContent(content: string): UserSubmapPresetSnapshot {
    return {
        version: 1,
        title: "大小边界",
        nodes: [{ localId: "text-1", type: CanvasNodeType.Text, title: "文本", metadata: { content } }],
    };
}

describe("exportSubmapSnapshot", () => {
    test("普通节点快照：去 scope 前缀、完整 metadata 往返、三组声明引用与生成引用局部化并裁掉组外", () => {
        const snapshot = exportSubmapSnapshot(submap(), sourceNodes(), "我的预制");

        expect(snapshot.version).toBe(1);
        expect(snapshot.title).toBe("我的预制");
        expect(snapshot.nodes).toHaveLength(4);
        expect(snapshot).not.toHaveProperty("connections");

        const text = snapshot.nodes[0];
        expect(text.localId).toBe("text-1");
        expect(text.metadata?.content).toBe("台词正文");
        expect(text.metadata?.referenceNodeIds).toEqual(["image-1"]);
        expect(text.metadata?.referenceTextNodeIds).toEqual(["text-2"]);
        expect((text.metadata?.inputBindings as Array<{ nodeId: string; id: string; order: number }>).map((binding) => [binding.id, binding.nodeId, binding.order])).toEqual([["node:text-2", "text-2", 0]]);

        const image = snapshot.nodes[1];
        expect(image.localId).toBe("image-1");
        expect(image.metadata?.generationSpec?.prompt).toBe("油画风格");
        expect(image.metadata?.generationSpec?.referenceBindings.map((binding) => [binding.id, binding.nodeId])).toEqual([["node:text-1", "text-1"]]);
        expect(image.metadata?.videoStartFrameNodeId).toBe("text-2");
        expect(image.metadata?.videoEndFrameNodeId).toBeUndefined();

        const video = snapshot.nodes[3];
        expect(video.metadata?.generationSpec?.referenceBindings.map((binding) => [binding.id, binding.nodeId, binding.order])).toEqual([
            ["node:text-1", "text-1", 0],
            ["node:image-1", "image-1", 1],
        ]);
    });

    test("空子树拒绝，不带 scope 前缀的子节点拒绝", () => {
        expect(() => exportSubmapSnapshot(submap(), [submap()], "空")).toThrow();
        const malformed = [submap(), node("text-1", CanvasNodeType.Text, {}, "submap-1")];
        expect(() => exportSubmapSnapshot(submap(), malformed, "畸形")).toThrow();
    });
});

describe("instantiateSubmapSnapshot", () => {
    test("跨画布插入两次：两次各自发号，原件与两个副本分别引用自己的子节点", () => {
        const snapshot = exportSubmapSnapshot(submap(), sourceNodes(), "我的预制");
        const second = instantiateSubmapSnapshot({ submap: submap("submap-2"), snapshot, allocateId: sequentialAllocator() });
        const third = instantiateSubmapSnapshot({ submap: submap("submap-3"), snapshot, allocateId: sequentialAllocator() });

        const verify = (children: CanvasNodeData[], scopeId: string) => {
            expect(children.map((child) => child.id)).toEqual([`${scopeId}__text-1`, `${scopeId}__image-1`, `${scopeId}__text-2`, `${scopeId}__video-1`]);
            expect(children.every((child) => child.scopeId === scopeId)).toBe(true);
            const text = children.find((child) => child.id === `${scopeId}__text-1`)!;
            const image = children.find((child) => child.id === `${scopeId}__image-1`)!;
            const video = children.find((child) => child.id === `${scopeId}__video-1`)!;
            expect(text.metadata?.content).toBe("台词正文");
            expect(text.metadata?.referenceNodeIds).toEqual([image.id]);
            expect(text.metadata?.referenceTextNodeIds).toEqual([`${scopeId}__text-2`]);
            expect(image.metadata?.referenceTextNodeIds).toEqual([text.id]);
            expect(image.metadata?.videoStartFrameNodeId).toBe(`${scopeId}__text-2`);
            expect(video.metadata?.videoStartFrameNodeId).toBe(image.id);
            expect(video.metadata?.referenceTextNodeIds).toEqual([text.id]);
            expect(video.metadata?.generationSpec?.referenceBindings.map((binding) => [binding.id, binding.nodeId])).toEqual([
                [`node:${scopeId}__text-1`, `${scopeId}__text-1`],
                [`node:${scopeId}__image-1`, `${scopeId}__image-1`],
            ]);
        };
        verify(second, "submap-2");
        verify(third, "submap-3");
        expect(second.some((child) => child.id.startsWith("submap-3__"))).toBe(false);
        expect(third.some((child) => child.id.startsWith("submap-2__"))).toBe(false);
    });

    test("前向引用仍能重映射，往返后快照自包含", () => {
        const first = exportSubmapSnapshot(submap(), sourceNodes(), "我的预制");
        const children = instantiateSubmapSnapshot({ submap: submap("submap-2"), snapshot: first, allocateId: sequentialAllocator() });
        const second = exportSubmapSnapshot(submap("submap-2"), [submap("submap-2"), ...children], "我的预制");

        const ids = new Set(second.nodes.map((snapshotNode) => snapshotNode.localId));
        expect(ids.size).toBe(second.nodes.length);
        for (const snapshotNode of second.nodes) {
            for (const ref of snapshotRefIds({ ...second, nodes: [snapshotNode] })) expect(ids.has(ref)).toBe(true);
        }
    });

    test("未注册节点类型在实例化时拒绝", () => {
        const snapshot = snapshotWithContent("正文");
        snapshot.nodes[0].type = "nonexistent-type";
        expect(() => validateUserSubmapSnapshot(snapshot)).toThrow();
        expect(() => instantiateSubmapSnapshot({ submap: submap(), snapshot, allocateId: sequentialAllocator() })).toThrow();
    });
});

describe("用户模板契约边界", () => {
    test("80 字名称、200 节点通过；81 字、201 节点拒绝", () => {
        const named = snapshotWithContent("正文");
        named.title = "名".repeat(MAX_USER_SUBMAP_PRESET_NAME);
        expect(() => validateUserSubmapSnapshot(named)).not.toThrow();
        named.title += "名";
        expect(() => validateUserSubmapSnapshot(named)).toThrow();

        const many: UserSubmapPresetSnapshot = {
            version: 1,
            title: "200 节点",
            nodes: Array.from({ length: MAX_USER_SUBMAP_PRESET_NODES }, (_, index) => ({ localId: `text-${index + 1}`, type: CanvasNodeType.Text, title: "文本" })),
        };
        expect(() => validateUserSubmapSnapshot(many)).not.toThrow();
        many.nodes.push({ localId: "audio-1", type: CanvasNodeType.Audio, title: "旁白" });
        expect(() => validateUserSubmapSnapshot(many)).toThrow();
    });

    test("512 KiB 通过，超 1 字节拒绝", () => {
        const one = snapshotWithContent("a");
        const padding = MAX_USER_SUBMAP_PRESET_BYTES - userSubmapPresetBytes(one) + 1;
        const atLimit = snapshotWithContent("a".repeat(padding));
        expect(userSubmapPresetBytes(atLimit)).toBe(MAX_USER_SUBMAP_PRESET_BYTES);
        expect(() => validateUserSubmapSnapshot(atLimit)).not.toThrow();
        const over = snapshotWithContent("a".repeat(padding + 1));
        expect(() => validateUserSubmapSnapshot(over)).toThrow("512KB");
    });

    test("错误版本、悬空引用、非字符串引用与重复 localId 拒绝", () => {
        expect(() => validateUserSubmapSnapshot({ ...snapshotWithContent("正文"), version: 2 })).toThrow();
        expect(() =>
            validateUserSubmapSnapshot({
                version: 1,
                title: "坏引用",
                nodes: [
                    { localId: "image-1", type: CanvasNodeType.Image, title: "首帧", metadata: { referenceTextNodeIds: ["text-9"] } },
                    { localId: "image-1", type: CanvasNodeType.Image, title: "重复" },
                ],
            }),
        ).toThrow();
        expect(() =>
            validateUserSubmapSnapshot({
                version: 1,
                title: "坏类型",
                nodes: [{ localId: "image-1", type: CanvasNodeType.Image, title: "首帧", metadata: { referenceTextNodeIds: [42] } }],
            }),
        ).toThrow();
    });

    test("生成绑定与输入绑定必须自包含且结构合法", () => {
        for (const mutate of [
            (node: NonNullable<UserSubmapPresetSnapshot["nodes"][number]>) => {
                node.metadata = { generationSpec: { referenceBindings: [{ nodeId: "text-9" }] } };
            },
            (node: NonNullable<UserSubmapPresetSnapshot["nodes"][number]>) => {
                node.metadata = { inputBindings: [{ nodeId: "text-9" }] };
            },
            (node: NonNullable<UserSubmapPresetSnapshot["nodes"][number]>) => {
                node.metadata = { generationSpec: { referenceBindings: "bad" } };
            },
            (node: NonNullable<UserSubmapPresetSnapshot["nodes"][number]>) => {
                node.metadata = { inputBindings: ["bad"] };
            },
            (node: NonNullable<UserSubmapPresetSnapshot["nodes"][number]>) => {
                node.metadata = { inputBindings: [{ nodeId: 42 }] };
            },
        ]) {
            const snapshot = snapshotWithContent("正文");
            mutate(snapshot.nodes[0]);
            expect(() => validateUserSubmapSnapshot(snapshot)).toThrow();
        }
    });
});

describe("快照边投影", () => {
    test("实例化后的声明引用可投影为完整边集，不需要持久 connections", () => {
        const snapshot = exportSubmapSnapshot(submap(), sourceNodes(), "我的预制");
        const children = instantiateSubmapSnapshot({ submap: submap("submap-2"), snapshot, allocateId: sequentialAllocator() });
        const connections: CanvasConnection[] = [];
        const projected = projectReferenceConnections([submap("submap-2"), ...children], connections, 1);
        expect(connections).toEqual([]);
        expect(projected.map((connection) => `${connection.fromNodeId}->${connection.toNodeId}`).sort()).toEqual([
            "submap-2__image-1->submap-2__video-1",
            "submap-2__text-1->submap-2__image-1",
            "submap-2__text-1->submap-2__video-1",
            "submap-2__text-2->submap-2__text-1",
        ]);
        expect(snapshotRefIds(snapshot).length).toBeGreaterThan(0);
    });
});
