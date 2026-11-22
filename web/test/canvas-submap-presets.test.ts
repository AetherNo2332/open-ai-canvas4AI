import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, test } from "bun:test";

import { writeCanvasNodePrompt } from "../src/lib/canvas/canvas-node-prompt";
import { createSequentialIdAllocator, prepareSequentialIdBatch, type SequentialCounters } from "../src/lib/canvas/canvas-node-id";
import { isolateCopiedNodeMetadata } from "../src/lib/canvas/canvas-node-copy";
import { projectReferenceConnections } from "../src/lib/canvas/canvas-reference-fields";
import { allocateCopiedCanvasNodeIds } from "../src/lib/canvas/canvas-submap-operations";
import { EMPTY_SUBMAP_PRESET_ID, getSubmapPreset, instantiateSubmapPreset, listSubmapPresetOptions, replaceSubmapChildrenWithPreset, resolveSubmapPresetId } from "../src/lib/canvas/canvas-submap-presets";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata } from "../src/types/canvas";

function submap(): CanvasNodeData {
    return { id: "submap-1", type: CanvasNodeType.Submap, title: "镜头 1", position: { x: 100, y: 200 }, width: 920, height: 360 };
}

function sequentialAllocator() {
    const counters = new Map<string, number>();
    return (prefix: string) => {
        const next = (counters.get(prefix) || 0) + 1;
        counters.set(prefix, next);
        return `${prefix}-${next}`;
    };
}

function childIds(nodes: CanvasNodeData[]) {
    return nodes.filter((node) => node.scopeId === "submap-1").map((node) => node.id);
}

function managedConnections(nodes: CanvasNodeData[], connections: CanvasConnection[] = []) {
    return projectReferenceConnections(nodes, connections, 1) as Array<CanvasConnection & { managedBy?: string; referenceOwnerNodeId?: string }>;
}

describe("submap 预置清单", () => {
    test("含空白与 storyboard 两个选项", () => {
        const options = listSubmapPresetOptions();
        expect(options.map((option) => option.value)).toEqual([EMPTY_SUBMAP_PRESET_ID, "storyboard"]);
        expect(getSubmapPreset(EMPTY_SUBMAP_PRESET_ID)?.children).toEqual([]);
        expect(getSubmapPreset("storyboard")?.children.length).toBe(6);
    });

    test("storyboard 逐字段镜像后端 manifest（作者契约 parity）", () => {
        const backendPath = fileURLToPath(new URL("../../backend/internal/canvas/submapworkflow/testdata/storyboard.json", import.meta.url));
        const backend = JSON.parse(readFileSync(backendPath, "utf8"));
        const preset = getSubmapPreset("storyboard")!;
        expect(preset.version).toBe(backend.version);
        expect(preset.id).toBe(backend.id);
        expect(preset.children).toEqual(backend.children);
        expect(preset.connections).toEqual(backend.connections);
    });

    test("已登记 workflowId 解析到预置，未知或缺失回落到空白", () => {
        expect(resolveSubmapPresetId({ workflowId: "storyboard" })).toBe("storyboard");
        expect(resolveSubmapPresetId({ workflowId: "retired-thing" })).toBe(EMPTY_SUBMAP_PRESET_ID);
        expect(resolveSubmapPresetId(undefined)).toBe(EMPTY_SUBMAP_PRESET_ID);
    });
});

describe("submap 形式下拉接线", () => {
    const read = (relative: string) => readFileSync(new URL(`../${relative}`, import.meta.url), "utf8").replace(/\r\n/g, "\n");

    test("标题栏、容器、世界层、页面与节点操作共用同一条回调链", () => {
        const header = read("src/components/canvas/canvas-submap-node-header.tsx");
        const node = read("src/components/canvas/canvas-submap-node.tsx");
        const worldLayers = read("src/pages/canvas/canvas-project-world-layers.tsx");
        const project = read("src/pages/canvas/project.tsx");
        const nodeOps = read("src/pages/canvas/use-canvas-node-operations.ts");
        expect(header).toContain("listSubmapPresetOptions()");
        expect(header).toContain("onPresetChange(data.id, String(value))");
        expect(node).toContain("onPresetChange={onPresetChange}");
        expect(worldLayers).toContain("onPresetChange={props.onSubmapPresetChange}");
        expect(project).toContain("applySubmapPreset,");
        expect(project).toContain("onSubmapPresetChange={applySubmapPreset}");
        expect(project).toContain("onSubmapSavePreset={openSubmapPresetSave}");
        expect(project).toContain("CanvasSubmapSavePresetModal");
        expect(worldLayers).toContain("onSavePreset={props.onSubmapSavePreset}");
        expect(node).toContain("onSavePreset={onSavePreset}");
        expect(header).toContain("onSavePreset(data.id)");
        expect(nodeOps).toContain("replaceSubmapChildren({");
        expect(nodeOps).toContain("createUserSubmapPreset(");
        expect(nodeOps).toContain("instantiateSubmapSnapshot({");
        expect(nodeOps).toContain("getSubmapPreset(presetId)");
    });
});

describe("submap 预置实例化", () => {
    test("按复合前缀发号，scopeId 指向容器，标题来自 manifest", () => {
        const { children } = instantiateSubmapPreset({ submap: submap(), preset: getSubmapPreset("storyboard")!, allocateId: sequentialAllocator() });
        expect(children.map((node) => node.id)).toEqual(["submap-1__text-1", "submap-1__text-2", "submap-1__text-3", "submap-1__text-4", "submap-1__image-1", "submap-1__video-1"]);
        expect(children.every((node) => node.scopeId === "submap-1")).toBe(true);
        expect(children.map((node) => node.title)).toEqual(["图片提示词", "视频提示词", "台词/旁白", "备注", "首帧", "视频"]);
    });

    test("prompt / seconds / startFrame 同时写入平铺字段与生成合同", () => {
        const { children } = instantiateSubmapPreset({ submap: submap(), preset: getSubmapPreset("storyboard")!, allocateId: sequentialAllocator() });
        const image = children.find((node) => node.type === CanvasNodeType.Image)!;
        const video = children.find((node) => node.type === CanvasNodeType.Video)!;
        expect(image.metadata?.generationSpec?.prompt).toBe("@文本1");
        expect(video.metadata?.generationSpec?.prompt).toBe("@文本1\n@文本2\n台词/旁白：@文本3");
        expect(video.metadata?.seconds).toBe("4");
        expect(video.metadata?.generationSpec?.options.durationSeconds).toBe(4);
        expect(video.metadata?.videoStartFrameNodeId).toBe(image.id);
    });

    test("连线顺序决定 @文本N 的引用槽位，目标写 referenceTextNodeIds", () => {
        const { children } = instantiateSubmapPreset({ submap: submap(), preset: getSubmapPreset("storyboard")!, allocateId: sequentialAllocator() });
        const image = children.find((node) => node.type === CanvasNodeType.Image)!;
        const video = children.find((node) => node.type === CanvasNodeType.Video)!;
        expect(image.metadata?.referenceTextNodeIds).toEqual(["submap-1__text-1"]);
        expect(video.metadata?.referenceTextNodeIds).toEqual(["submap-1__text-1", "submap-1__text-2", "submap-1__text-3"]);
    });

    test("生成合同的文本绑定按连接顺序排列，首帧绑定指向自己的图片子节点", () => {
        const { children } = instantiateSubmapPreset({ submap: submap(), preset: getSubmapPreset("storyboard")!, allocateId: sequentialAllocator() });
        const image = children.find((node) => node.type === CanvasNodeType.Image)!;
        const video = children.find((node) => node.type === CanvasNodeType.Video)!;
        expect(image.metadata?.generationSpec?.referenceBindings.map((binding) => [binding.nodeId, binding.role])).toEqual([["submap-1__text-1", "source-text"]]);
        expect(video.metadata?.generationSpec?.referenceBindings.map((binding) => [binding.nodeId, binding.role])).toEqual([
            ["submap-1__text-1", "source-text"],
            ["submap-1__text-2", "source-text"],
            ["submap-1__text-3", "source-text"],
            ["submap-1__image-1", "first-frame"],
        ]);
    });

    test("字段投影出 4 条文本引用边 + 1 条首帧边，无持久化 connections 依赖", () => {
        const container = submap();
        const { children } = instantiateSubmapPreset({ submap: container, preset: getSubmapPreset("storyboard")!, allocateId: sequentialAllocator() });
        const edges = managedConnections([container, ...children]);
        expect(edges.filter((edge) => edge.managedBy === "reference-fields-v1").length).toBe(5);
        const frame = edges.find((edge) => edge.fromNodeId === "submap-1__image-1" && edge.toNodeId === "submap-1__video-1");
        expect(frame?.managedBy).toBe("reference-fields-v1");
        expect(edges.every((edge) => children.some((node) => node.id === edge.toNodeId))).toBe(true);
    });

    test("预置在容器内容区内按三列网格排布，绝对坐标", () => {
        const container = submap();
        const { children } = instantiateSubmapPreset({ submap: container, preset: getSubmapPreset("storyboard")!, allocateId: sequentialAllocator() });
        expect(children.every((node) => node.position.x >= container.position.x && node.position.y > container.position.y)).toBe(true);
    });
});

describe("submap 预置反复切换不发号递增", () => {
    const STORYBOARD_IDS = ["submap-1__text-1", "submap-1__text-2", "submap-1__text-3", "submap-1__text-4", "submap-1__image-1", "submap-1__video-1"];

    test("storyboard→空白→storyboard 反复 3 轮，子节点 ID 恒为 text-1..4 / image-1 / video-1", () => {
        const container = submap();
        let nodes = [container];
        let connections: CanvasConnection[] = [];
        let idCounters: SequentialCounters = {};

        for (let round = 0; round < 3; round += 1) {
            const batch = prepareSequentialIdBatch(nodes, connections, [], idCounters, container.id);
            idCounters = batch.counters;
            const result = replaceSubmapChildrenWithPreset({
                nodes,
                connections,
                submapId: container.id,
                presetId: "storyboard",
                referenceFieldsVersion: 1,
                allocateId: createSequentialIdAllocator(batch.taken, idCounters),
            })!;
            nodes = result.nodes;
            connections = result.connections;
            expect(childIds(nodes)).toEqual(STORYBOARD_IDS);
            expect(idCounters["submap-1__text"]).toBe(4);
            expect(nodes.find((node) => node.id === container.id)?.metadata).toMatchObject({ workflowId: "storyboard", workflowVersion: 1 });

            const blankBatch = prepareSequentialIdBatch(nodes, connections, [], idCounters, container.id);
            idCounters = blankBatch.counters;
            const blank = replaceSubmapChildrenWithPreset({
                nodes,
                connections,
                submapId: container.id,
                presetId: EMPTY_SUBMAP_PRESET_ID,
                referenceFieldsVersion: 1,
                allocateId: createSequentialIdAllocator(blankBatch.taken, idCounters),
            })!;
            nodes = blank.nodes;
            connections = blank.connections;
            expect(childIds(nodes)).toEqual([]);
            expect(nodes.find((node) => node.id === container.id)?.metadata?.workflowId).toBeUndefined();
        }
    });

    test("切换空白时清掉外部节点指向旧子节点的引用字段与生成绑定", () => {
        const container = submap();
        const batch = prepareSequentialIdBatch([container], [], [], {}, container.id);
        const first = replaceSubmapChildrenWithPreset({
            nodes: [container],
            connections: [],
            submapId: container.id,
            presetId: "storyboard",
            referenceFieldsVersion: 1,
            allocateId: createSequentialIdAllocator(batch.taken, batch.counters),
        })!;
        const external = writeCanvasNodePrompt(
            {
                id: "video-9",
                type: CanvasNodeType.Video,
                title: "外部视频",
                position: { x: 0, y: 0 },
                width: 320,
                height: 240,
                metadata: {
                    referenceTextNodeIds: ["submap-1__text-1"],
                    generationSpec: {
                        version: 1,
                        mode: "video",
                        prompt: "",
                        options: {},
                        textInputMode: "prompt-only",
                        referenceBindings: [{ id: "node:submap-1__text-1", nodeId: "submap-1__text-1", mediaType: "text", role: "source-text", order: 0, resolution: "latest" }],
                    },
                },
            },
            "外部提示词",
        );
        const cleared = replaceSubmapChildrenWithPreset({
            nodes: [...first.nodes, external],
            connections: first.connections,
            submapId: container.id,
            presetId: EMPTY_SUBMAP_PRESET_ID,
            referenceFieldsVersion: 1,
            allocateId: createSequentialIdAllocator([]),
        })!;
        const nextExternal = cleared.nodes.find((node) => node.id === "video-9")!;
        expect(nextExternal.metadata?.referenceTextNodeIds).toEqual([]);
        expect(nextExternal.metadata?.generationSpec?.referenceBindings).toEqual([]);
    });
});

describe("submap 预置编辑与整组复制", () => {
    test("修改提示词后复制，原件和副本分别引用自己的子节点", () => {
        const container = submap();
        const { children } = instantiateSubmapPreset({ submap: container, preset: getSubmapPreset("storyboard")!, allocateId: sequentialAllocator() });
        const nodes = [container, ...children].map((node) => (node.type === CanvasNodeType.Image ? writeCanvasNodePrompt(node, "edited image prompt") : node));
        const copySources = nodes;
        const idMap = allocateCopiedCanvasNodeIds(copySources, createSequentialIdAllocator(nodes.map((node) => node.id)));
        expect(idMap.get("submap-1")).toBe("submap-2");

        const copies = copySources.slice(1).map((source) => ({
            ...source,
            id: idMap.get(source.id)!,
            scopeId: idMap.get(source.scopeId!)!,
            metadata: isolateCopiedNodeMetadata(source, idMap, { dropExternalReferences: true }),
        }));
        const originalVideo = nodes.find((node) => node.id === "submap-1__video-1")!;
        const copiedVideo = copies.find((node) => node.id === "submap-2__video-1")!;
        expect(originalVideo.metadata?.generationSpec?.referenceBindings.map((binding) => binding.nodeId)).toEqual(["submap-1__text-1", "submap-1__text-2", "submap-1__text-3", "submap-1__image-1"]);
        expect(copiedVideo.metadata?.generationSpec?.referenceBindings.map((binding) => binding.nodeId)).toEqual(["submap-2__text-1", "submap-2__text-2", "submap-2__text-3", "submap-2__image-1"]);
        expect(copiedVideo.metadata?.referenceTextNodeIds).toEqual(["submap-2__text-1", "submap-2__text-2", "submap-2__text-3"]);
        expect(copiedVideo.metadata?.videoStartFrameNodeId).toBe("submap-2__image-1");
        expect(copies.find((node) => node.id === "submap-2__image-1")?.metadata?.generationSpec?.prompt).toBe("edited image prompt");

        const metadata: CanvasNodeMetadata = copiedVideo.metadata!;
        expect(metadata.generationSpec?.referenceBindings[3].role).toBe("first-frame");
    });
});
