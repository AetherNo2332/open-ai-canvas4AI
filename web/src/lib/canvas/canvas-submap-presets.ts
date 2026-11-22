import data from "@/lib/canvas/submap-presets.json";

import { FRAME_HEADER_HEIGHT, FRAME_PADDING } from "@/lib/canvas/canvas-frame";
import { writeCanvasNodePrompt } from "@/lib/canvas/canvas-node-prompt";
import { createCanvasNode, removeCanvasNodes } from "@/lib/canvas/canvas-project-domain";
import { projectReferenceConnections, pruneReferenceFields } from "@/lib/canvas/canvas-reference-fields";
import { fitSubmapToChildren } from "@/lib/canvas/canvas-submap-layout";
import { getSubmapChildIds, isSubmapNode } from "@/lib/canvas/canvas-submap";
import { synchronizeGenerationSpec, validateGenerationSpec, type ReferenceBinding } from "@/lib/canvas/generation-contract";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type CanvasNodeMetadata } from "@/types/canvas";

/**
 * submap 形式下拉的预置清单。
 *
 * storyboard 项逐字段镜像后端 submapworkflow/testdata/storyboard.json，由
 * canvas-submap-presets.test.ts 的 parity 测试守住；后端是 manifest 的作者契约真源，
 * 这里只做"把已审过的 manifest 就地实例化"，不再复制一份校验规则。容器已存在，
 * 这里只产出子节点 + 需要投影的边；节点归属创建即定型（scopeId），模板与实例之间
 * 没有活链接。
 */

export type SubmapPresetChild = {
    localId: string;
    type: string;
    title: string;
    prompt?: string;
    seconds?: string;
    startFrame?: string;
};

export type SubmapPreset = {
    version: number;
    id: string;
    title: string;
    children: SubmapPresetChild[];
    connections: Array<{ from: string; to: string }>;
};

const PRESETS = data.presets as unknown as SubmapPreset[];

/** 空白子画布的哨兵 id：选中即清空子树并抹掉 workflowId。 */
export const EMPTY_SUBMAP_PRESET_ID = "";

function isPreset(value: unknown): value is SubmapPreset {
    if (!value || typeof value !== "object") return false;
    const preset = value as Partial<SubmapPreset>;
    return typeof preset.id === "string" && typeof preset.title === "string" && typeof preset.version === "number" && Array.isArray(preset.children) && Array.isArray(preset.connections);
}

/** 作者契约：清单自身畸形时报具体原因，绝不静默回落成空白 submap。 */
function parsePresets(): SubmapPreset[] {
    if (!Array.isArray(data.presets)) throw new Error(`submap-presets.json 缺少 presets 数组`);
    return PRESETS.map((preset, index) => {
        if (!isPreset(preset)) throw new Error(`submap-presets.json presets[${index}] 结构不完整`);
        const seen = new Set<string>();
        for (const child of preset.children) {
            if (!/^(text|image|video)-\d+$/.test(child.localId)) throw new Error(`预置 ${preset.id} 的 localId ${child.localId} 不符合 <text|image|video>-N`);
            if (seen.has(child.localId)) throw new Error(`预置 ${preset.id} 的 localId ${child.localId} 重复`);
            seen.add(child.localId);
            if (!CHILD_TYPES.has(child.type)) throw new Error(`预置 ${preset.id} 的 localId ${child.localId} 含未知类型 ${child.type}`);
        }
        for (const edge of preset.connections) {
            if (!seen.has(edge.from) || !seen.has(edge.to)) throw new Error(`预置 ${preset.id} 的连线 ${edge.from}→${edge.to} 引用了不存在的 localId`);
        }
        return preset;
    });
}

const CHILD_TYPES = new Set<string>([CanvasNodeType.Text, CanvasNodeType.Image, CanvasNodeType.Video]);

const REGISTRY = parsePresets();

export function getSubmapPreset(id: string): SubmapPreset | undefined {
    return REGISTRY.find((preset) => preset.id === id);
}

/** 下拉选项：标题来自 manifest（JSON，不计入 i18n 硬编码门禁）。 */
export function listSubmapPresetOptions(): Array<{ value: string; label: string }> {
    return REGISTRY.map((preset) => ({ value: preset.id, label: preset.title }));
}

export type InstantiatedSubmapPreset = {
    children: CanvasNodeData[];
    /** 需要按字段重新投影边的目标节点（image/video 媒体节点）。 */
    projectedTargetIds: Set<string>;
};

/**
 * 把一个预置的 children/connections 实例化成 submap 的子节点。
 *
 * - ID 走调用方的顺序发号器，复合前缀 `${submapId}__${type}`，与后端发号同语义。
 * - 坐标按后端 cloudAgentSubmapChildPosition 的三列网格现算，不问模型。
 * - prompt / seconds / startFrame 与文本引用顺序同时写入旧平铺字段和
 *   generationSpec.referenceBindings：前者供关系投影与 Agent 文档读，后者是目标
 *   画布真实生成输入的连接投影真源。
 */
export function instantiateSubmapPreset({ submap, preset, allocateId }: { submap: CanvasNodeData; preset: SubmapPreset; allocateId: (prefix: string) => string }): InstantiatedSubmapPreset {
    const localToId = new Map<string, string>();
    const children: CanvasNodeData[] = [];

    preset.children.forEach((child, index) => {
        const type = child.type as CanvasNodeType.Text | CanvasNodeType.Image | CanvasNodeType.Video;
        const nodeId = allocateId(`${submap.id}__${type}`);
        localToId.set(child.localId, nodeId);

        const gap = 16;
        const columns = 3;
        const cellWidth = (submap.width - FRAME_PADDING * 2 - gap * (columns - 1)) / columns;
        const column = index % columns;
        const row = Math.floor(index / columns);
        const position = {
            x: submap.position.x + FRAME_PADDING + column * (cellWidth + gap),
            y: submap.position.y + FRAME_HEADER_HEIGHT + FRAME_PADDING + row * (240 + gap),
        };

        let node = createCanvasNode(type, position, undefined, nodeId);
        node.title = child.title;
        // createCanvasNode 把传入坐标当中心点；submap 子节点落位是左上角，与
        // clampSubmapChildPositions 的 min/max 口径一致，所以直接覆写为网格左上角。
        node.position = position;
        node.scopeId = submap.id;
        if (child.seconds) node = synchronizeGenerationSpec(node, { seconds: child.seconds });
        if (child.prompt) node = writeCanvasNodePrompt(node, child.prompt);
        children.push(node);
    });

    // startFrame 指向本地子节点，必须解析到真实发号 ID（模型永不编画布 ID）。
    for (let index = 0; index < preset.children.length; index += 1) {
        const child = preset.children[index];
        if (!child.startFrame) continue;
        const frameId = localToId.get(child.startFrame);
        if (!frameId) throw new Error(`预置 ${preset.id} 的 startFrame ${child.startFrame} 引用了不存在的 localId`);
        children[index] = { ...children[index], metadata: { ...children[index].metadata, videoStartFrameNodeId: frameId } };
    }

    // 连线顺序 = 槽位顺序：@文本N 的 N 由目标节点 referenceTextNodeIds 与
    // generationSpec.referenceBindings 的数组序决定。
    const referenceFields = new Map<string, string[]>();
    for (const edge of preset.connections) {
        const source = preset.children.find((child) => child.localId === edge.from)!;
        if (source.type !== CanvasNodeType.Text) throw new Error(`预置 ${preset.id} 的连线来源 ${edge.from} 不是文本节点`);
        const fromId = localToId.get(edge.from)!;
        const toId = localToId.get(edge.to)!;
        const ids = referenceFields.get(toId) || [];
        ids.push(fromId);
        referenceFields.set(toId, ids);
    }

    const projectedTargetIds = new Set<string>();
    for (let index = 0; index < children.length; index += 1) {
        const node = children[index];
        const textReferenceIds = referenceFields.get(node.id);
        const frameId = node.metadata?.videoStartFrameNodeId;
        if (!textReferenceIds?.length && !frameId) continue;
        const metadata: CanvasNodeMetadata = {
            ...node.metadata,
            ...(textReferenceIds?.length ? { referenceTextNodeIds: textReferenceIds } : {}),
        };
        const spec = metadata.generationSpec;
        if (spec) {
            const bindings: ReferenceBinding[] = (textReferenceIds || []).map((nodeId, order) => ({
                id: `node:${nodeId}`,
                nodeId,
                mediaType: "text",
                role: "source-text",
                order,
                resolution: "latest",
            }));
            if (frameId) {
                bindings.push({
                    id: `node:${frameId}`,
                    nodeId: frameId,
                    mediaType: "image",
                    role: "first-frame",
                    order: bindings.length,
                    resolution: "latest",
                });
            }
            metadata.generationSpec = validateGenerationSpec({ ...spec, referenceBindings: bindings });
        }
        children[index] = { ...node, metadata };
        projectedTargetIds.add(node.id);
    }

    return { children, projectedTargetIds };
}

export type ReplaceSubmapPresetResult = {
    nodes: CanvasNodeData[];
    connections: CanvasConnection[];
    removedIds: Set<string>;
};

/** 删除引用已移除节点的旧平铺字段，避免下一次后端 reconcile 整份拒绝。 */
function pruneLegacyReferenceFields(nodes: CanvasNodeData[], removedIds: ReadonlySet<string>) {
    let changed = false;
    const next = nodes.map((node) => {
        const metadata = node.metadata;
        if (!metadata) return node;
        const referenceNodeIds = metadata.referenceNodeIds?.filter((id) => !removedIds.has(id));
        const referenceTextNodeIds = metadata.referenceTextNodeIds?.filter((id) => !removedIds.has(id));
        const videoStartFrameNodeId = metadata.videoStartFrameNodeId && !removedIds.has(metadata.videoStartFrameNodeId) ? metadata.videoStartFrameNodeId : undefined;
        const videoEndFrameNodeId = metadata.videoEndFrameNodeId && !removedIds.has(metadata.videoEndFrameNodeId) ? metadata.videoEndFrameNodeId : undefined;
        const sameLengths = (referenceNodeIds?.length || 0) === (metadata.referenceNodeIds?.length || 0) && (referenceTextNodeIds?.length || 0) === (metadata.referenceTextNodeIds?.length || 0);
        if (sameLengths && videoStartFrameNodeId === metadata.videoStartFrameNodeId && videoEndFrameNodeId === metadata.videoEndFrameNodeId) return node;
        changed = true;
        return { ...node, metadata: { ...metadata, referenceNodeIds, referenceTextNodeIds, videoStartFrameNodeId, videoEndFrameNodeId } };
    });
    return changed ? next : nodes;
}

function withWorkflowMetadata(submap: CanvasNodeData, workflowId: string | undefined, workflowVersion: number | undefined): CanvasNodeData {
    const metadata = { ...submap.metadata };
    if (workflowId) {
        metadata.workflowId = workflowId;
        metadata.workflowVersion = workflowVersion;
    } else {
        delete metadata.workflowId;
        delete metadata.workflowVersion;
    }
    return { ...submap, metadata };
}

export type ReplaceSubmapChildrenOptions = {
    nodes: CanvasNodeData[];
    connections: CanvasConnection[];
    submapId: string;
    workflowId?: string;
    workflowVersion?: number;
    referenceFieldsVersion: unknown;
    allocateId: (prefix: string) => string;
    buildChildren: (submap: CanvasNodeData, allocateId: (prefix: string) => string) => CanvasNodeData[];
};

/**
 * 用任意模板整体替换一个 submap 的子树。
 *
 * 旧子节点、触碰旧子节点的连线、以及所有指向旧子节点的引用字段先清掉；新节点由
 * buildChildren 按模板实例化后，再用 referenceFieldsVersion 投影可见边。ID 由
 * 调用方交给 beginIdBatch(..., submapId) 重置局部高水位。
 */
export function replaceSubmapChildren({ nodes, connections, submapId, workflowId, workflowVersion, referenceFieldsVersion, allocateId, buildChildren }: ReplaceSubmapChildrenOptions): ReplaceSubmapPresetResult | null {
    const submap = nodes.find((node) => node.id === submapId && isSubmapNode(node));
    if (!submap || submap.metadata?.locked) return null;

    const previousChildIds = getSubmapChildIds(submapId, nodes);
    const removed = removeCanvasNodes(nodes, previousChildIds);
    const pruned = pruneReferenceFields(removed.nodes, removed.removedIds, referenceFieldsVersion);
    const remaining = pruneLegacyReferenceFields(pruned, removed.removedIds);
    const replacedSubmap = withWorkflowMetadata(submap, workflowId, workflowVersion);
    const baseNodes = remaining.map((node) => (node.id === submapId ? replacedSubmap : node));
    const children = buildChildren(replacedSubmap, allocateId);
    const nextNodes = fitSubmapToChildren([...baseNodes, ...children], submapId);
    const nextConnections = connections.filter((connection) => !removed.removedIds.has(connection.fromNodeId) && !removed.removedIds.has(connection.toNodeId));

    return {
        nodes: nextNodes,
        connections: projectReferenceConnections(nextNodes, nextConnections, referenceFieldsVersion),
        removedIds: removed.removedIds,
    };
}

/**
 * 用系统预置整体替换一个 submap 的子树。
 *
 * 旧子节点、触碰旧子节点的连线、以及所有指向旧子节点的引用字段先清掉；新节点按
 * manifest 顺序实例化后，再用 referenceFieldsVersion 投影可见边。ID 由调用方
 * 交给 beginIdBatch(..., submapId) 重置局部高水位，保证反复切模板仍是
 * text-1..4 / image-1 / video-1。
 */
export function replaceSubmapChildrenWithPreset({
    nodes,
    connections,
    submapId,
    presetId,
    referenceFieldsVersion,
    allocateId,
}: {
    nodes: CanvasNodeData[];
    connections: CanvasConnection[];
    submapId: string;
    presetId: string;
    referenceFieldsVersion: unknown;
    allocateId: (prefix: string) => string;
}): ReplaceSubmapPresetResult | null {
    const preset = getSubmapPreset(presetId);
    if (!preset) return null;
    return replaceSubmapChildren({
        nodes,
        connections,
        submapId,
        workflowId: preset.id || undefined,
        workflowVersion: preset.id ? preset.version : undefined,
        referenceFieldsVersion,
        allocateId,
        buildChildren: (submap, childAllocateId) => instantiateSubmapPreset({ submap, preset, allocateId: childAllocateId }).children,
    });
}

/** 供类型下拉展示：当前 submap 的 workflowId 归一到哨兵 id。 */
export function resolveSubmapPresetId(metadata: CanvasNodeMetadata | undefined, extraIds?: ReadonlySet<string>): string {
    const id = metadata?.workflowId;
    if (!id) return EMPTY_SUBMAP_PRESET_ID;
    if (getSubmapPreset(id) || extraIds?.has(id)) return id;
    return EMPTY_SUBMAP_PRESET_ID;
}
