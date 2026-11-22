import type { UserSubmapPresetNode, UserSubmapPresetSnapshot } from "@/services/api/submap-presets";

import { FRAME_HEADER_HEIGHT, FRAME_PADDING } from "@/lib/canvas/canvas-frame";
import { canvasSubmapLocalId, getSubmapChildren } from "@/lib/canvas/canvas-submap";
import { createCanvasNode } from "@/lib/canvas/canvas-project-domain";
import type { CanvasNodeData, CanvasNodeMetadata, CanvasNodeTypeId } from "@/types/canvas";

/**
 * 用户自定义 submap 预制 = 一组普通画布节点的局部化快照。
 * 节点形态与画布一致，不做 storyboard manifest 的受限投影；唯一转换是
 * 全局 ID ↔ localId，以及声明式引用字段与生成引用的同步重映射。
 */

export const MAX_USER_SUBMAP_PRESET_NODES = 200;
export const MAX_USER_SUBMAP_PRESET_BYTES = 512 << 10;
export const MAX_USER_SUBMAP_PRESET_NAME = 80;
export const USER_SUBMAP_PRESET_VERSION = 1;

const REF_ARRAY_FIELDS = ["referenceNodeIds", "referenceTextNodeIds"] as const;
const REF_SCALAR_FIELDS = ["videoStartFrameNodeId", "videoEndFrameNodeId"] as const;
const LOCAL_ID_PATTERN = /^[a-z0-9][a-z0-9_-]{0,63}$/;

type BindingRecord = Record<string, unknown> & { id?: unknown; nodeId?: unknown; order?: unknown };

function isRecord(value: unknown): value is Record<string, unknown> {
    return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

function metadataRecord(metadata: CanvasNodeMetadata | undefined): Record<string, unknown> {
    return { ...((metadata || {}) as Record<string, unknown>) };
}

function remapBinding(binding: BindingRecord, mapping: ReadonlyMap<string, string>, dropExternal: boolean): BindingRecord | undefined {
    if (typeof binding.nodeId !== "string" || !binding.nodeId) return binding;
    const mapped = mapping.get(binding.nodeId);
    if (!mapped) return dropExternal ? undefined : binding;
    return { ...binding, nodeId: mapped, ...(binding.id === `node:${binding.nodeId}` ? { id: `node:${mapped}` } : {}) };
}

function remapBindings(value: unknown, mapping: ReadonlyMap<string, string>, dropExternal: boolean): unknown {
    if (!Array.isArray(value)) return value;
    return value
        .flatMap((binding) => {
            if (!isRecord(binding)) return [binding];
            const mapped = remapBinding(binding as BindingRecord, mapping, dropExternal);
            return mapped ? [mapped] : [];
        })
        .map((binding, order) => (isRecord(binding) ? { ...(binding as BindingRecord), order } : binding));
}

function remapMetadata(metadata: CanvasNodeMetadata | undefined, mapping: ReadonlyMap<string, string>, dropExternal: boolean): CanvasNodeMetadata | undefined {
    if (!metadata) return undefined;
    const result = metadataRecord(metadata);
    for (const field of REF_ARRAY_FIELDS) {
        const ids = result[field];
        if (!Array.isArray(ids)) continue;
        result[field] = ids.flatMap((id) => {
            if (typeof id !== "string") return [];
            const mapped = mapping.get(id);
            return mapped ? [mapped] : dropExternal ? [] : [id];
        });
    }
    for (const field of REF_SCALAR_FIELDS) {
        const id = result[field];
        if (typeof id !== "string") continue;
        const mapped = mapping.get(id);
        if (mapped) result[field] = mapped;
        else if (dropExternal) delete result[field];
    }
    const generationSpec = result.generationSpec;
    if (generationSpec && typeof generationSpec === "object" && !Array.isArray(generationSpec)) {
        const spec = generationSpec as Record<string, unknown>;
        if (Object.prototype.hasOwnProperty.call(spec, "referenceBindings")) {
            result.generationSpec = { ...spec, referenceBindings: remapBindings(spec.referenceBindings, mapping, dropExternal) };
        }
    }
    if (Object.prototype.hasOwnProperty.call(result, "inputBindings")) {
        result.inputBindings = remapBindings(result.inputBindings, mapping, dropExternal);
    }
    return result as CanvasNodeMetadata;
}

function bindingNodeIds(value: unknown, output: string[]) {
    if (!Array.isArray(value)) return;
    for (const binding of value) {
        if (isRecord(binding) && typeof binding.nodeId === "string") output.push(binding.nodeId);
    }
}

/** 快照自包含引用：三组声明字段与生成绑定都必须指向组内 localId。 */
function assertSnapshotReferences(nodes: UserSubmapPresetNode[], ids: ReadonlySet<string>) {
    const check = (node: UserSubmapPresetNode, field: string, value: string) => {
        if (value && !ids.has(value)) throw new Error(`节点 ${node.localId} 的 ${field} 引用了不存在的节点 ${value}`);
    };
    for (const node of nodes) {
        if (node.metadata !== undefined && !isRecord(node.metadata)) throw new Error(`节点 ${node.localId} 的 metadata 必须是对象`);
        const metadata = (node.metadata || {}) as Record<string, unknown>;
        for (const field of REF_ARRAY_FIELDS) {
            const values = metadata[field];
            if (!Array.isArray(values)) continue;
            for (const value of values) {
                if (typeof value !== "string") throw new Error(`节点 ${node.localId} 的 ${field} 必须是字符串数组`);
                check(node, field, value);
            }
        }
        for (const field of REF_SCALAR_FIELDS) {
            const value = metadata[field];
            if (value === undefined || value === null) continue;
            if (typeof value !== "string") throw new Error(`节点 ${node.localId} 的 ${field} 必须是字符串`);
            check(node, field, value);
        }
        if (metadata.generationSpec !== undefined && !isRecord(metadata.generationSpec)) throw new Error(`节点 ${node.localId} 的 generationSpec 必须是对象`);
        const generationSpec = isRecord(metadata.generationSpec) ? metadata.generationSpec : undefined;
        assertBindingList(node, "generationSpec.referenceBindings", generationSpec?.referenceBindings, check);
        assertBindingList(node, "inputBindings", metadata.inputBindings, check);
    }
}

function assertBindingList(node: UserSubmapPresetNode, field: string, value: unknown, check: (node: UserSubmapPresetNode, field: string, value: string) => void) {
    if (value === undefined) return;
    if (!Array.isArray(value)) throw new Error(`节点 ${node.localId} 的 ${field} 必须是数组`);
    for (const [index, binding] of value.entries()) {
        if (!isRecord(binding)) throw new Error(`节点 ${node.localId} 的 ${field}[${index}] 必须是对象`);
        if (!Object.prototype.hasOwnProperty.call(binding, "nodeId")) continue;
        if (typeof binding.nodeId !== "string" || !binding.nodeId) throw new Error(`节点 ${node.localId} 的 ${field}[${index}].nodeId 必须是非空字符串`);
        check(node, `${field}[${index}].nodeId`, binding.nodeId);
    }
}

function assertRegisteredNodeType(node: UserSubmapPresetNode) {
    try {
        createCanvasNode(node.type as CanvasNodeTypeId, { x: 0, y: 0 }, undefined, "preset-validation-node");
    } catch {
        throw new Error(`节点 ${node.localId} 的类型 ${node.type} 不是当前画布支持的节点类型`);
    }
}

export function userSubmapPresetBytes(snapshot: UserSubmapPresetSnapshot) {
    return new TextEncoder().encode(JSON.stringify(snapshot)).byteLength;
}

export function validateUserSubmapSnapshot(snapshot: UserSubmapPresetSnapshot) {
    if (!snapshot || typeof snapshot !== "object") throw new Error("模板快照无效");
    if (snapshot.version !== USER_SUBMAP_PRESET_VERSION) throw new Error(`模板版本 ${snapshot.version} 不受支持`);
    const title = typeof snapshot.title === "string" ? snapshot.title.trim() : "";
    if (!title) throw new Error("模板名称不能为空");
    if (Array.from(title).length > MAX_USER_SUBMAP_PRESET_NAME) throw new Error(`模板名称最多 ${MAX_USER_SUBMAP_PRESET_NAME} 个字`);
    if (!Array.isArray(snapshot.nodes) || !snapshot.nodes.length) throw new Error("模板没有可保存的子节点");
    if (snapshot.nodes.length > MAX_USER_SUBMAP_PRESET_NODES) throw new Error(`模板节点数超过上限 ${MAX_USER_SUBMAP_PRESET_NODES}`);
    const ids = new Set<string>();
    for (const node of snapshot.nodes) {
        if (!node || typeof node !== "object") throw new Error("模板节点无效");
        if (typeof node.localId !== "string" || !LOCAL_ID_PATTERN.test(node.localId)) throw new Error(`模板节点 localId 无效：${String(node.localId)}`);
        if (ids.has(node.localId)) throw new Error(`模板节点 localId 重复：${node.localId}`);
        ids.add(node.localId);
        if (typeof node.type !== "string" || !node.type) throw new Error(`模板节点 ${node.localId} 缺少类型`);
        if (typeof node.title !== "string" || !node.title.trim()) throw new Error(`模板节点 ${node.localId} 缺少标题`);
        assertRegisteredNodeType(node);
    }
    assertSnapshotReferences(snapshot.nodes, ids);
    if (userSubmapPresetBytes(snapshot) > MAX_USER_SUBMAP_PRESET_BYTES) throw new Error("模板快照超过 512KB");
}

/** 把画布里一棵 submap 子树导出成快照（global → local）。 */
export function exportSubmapSnapshot(submap: CanvasNodeData, nodes: CanvasNodeData[], title: string): UserSubmapPresetSnapshot {
    const children = getSubmapChildren(submap.id, nodes);
    if (!children.length) throw new Error("子画布没有子节点，无法保存为模板");
    const globalToLocal = new Map<string, string>();
    for (const node of children) {
        const localId = canvasSubmapLocalId(submap.id, node.id);
        if (localId === node.id) throw new Error(`节点 ${node.id} 不属于子画布 ${submap.id}`);
        globalToLocal.set(node.id, localId);
    }
    const snapshot: UserSubmapPresetSnapshot = {
        version: USER_SUBMAP_PRESET_VERSION,
        title: title.trim(),
        nodes: children.map((node): UserSubmapPresetNode => {
            const metadata = remapMetadata(node.metadata, globalToLocal, true);
            return {
                localId: canvasSubmapLocalId(submap.id, node.id),
                type: node.type,
                title: node.title.trim(),
                ...(metadata && Object.keys(metadata).length ? { metadata: metadata as Record<string, unknown> } : {}),
            };
        }),
    };
    validateUserSubmapSnapshot(snapshot);
    return snapshot;
}

/** 把快照实例化成 submap 的子节点（local → 新 global），布局按三列网格现算。 */
export function instantiateSubmapSnapshot({ submap, snapshot, allocateId }: { submap: CanvasNodeData; snapshot: UserSubmapPresetSnapshot; allocateId: (prefix: string) => string }): CanvasNodeData[] {
    validateUserSubmapSnapshot(snapshot);
    const localToId = new Map<string, string>();
    snapshot.nodes.forEach((node) => localToId.set(node.localId, allocateId(`${submap.id}__${node.type}`)));

    return snapshot.nodes.map((node, index) => {
        const type = node.type as CanvasNodeTypeId;
        const nodeId = localToId.get(node.localId)!;
        const gap = 16;
        const columns = 3;
        const cellWidth = (submap.width - FRAME_PADDING * 2 - gap * (columns - 1)) / columns;
        const position = {
            x: submap.position.x + FRAME_PADDING + (index % columns) * (cellWidth + gap),
            y: submap.position.y + FRAME_HEADER_HEIGHT + FRAME_PADDING + Math.floor(index / columns) * (240 + gap),
        };
        const metadata = remapMetadata(node.metadata as CanvasNodeMetadata | undefined, localToId, true);
        const created = createCanvasNode(type, position, metadata, nodeId);
        created.title = node.title.trim();
        created.position = position;
        created.scopeId = submap.id;
        return created;
    });
}

/** 快照内仍然指向组内节点的引用；供往返测试验证自包含。 */
export function snapshotRefIds(snapshot: UserSubmapPresetSnapshot): string[] {
    const ids = new Set(snapshot.nodes.map((node) => node.localId));
    const refs: string[] = [];
    for (const node of snapshot.nodes) {
        const metadata = (node.metadata || {}) as Record<string, unknown>;
        for (const field of REF_ARRAY_FIELDS) {
            const values = metadata[field];
            if (Array.isArray(values)) for (const value of values) if (typeof value === "string" && ids.has(value)) refs.push(value);
        }
        for (const field of REF_SCALAR_FIELDS) {
            const value = metadata[field];
            if (typeof value === "string" && ids.has(value)) refs.push(value);
        }
        const bindings: string[] = [];
        const generationSpec = metadata.generationSpec;
        if (generationSpec && typeof generationSpec === "object") bindingNodeIds((generationSpec as Record<string, unknown>).referenceBindings, bindings);
        bindingNodeIds(metadata.inputBindings, bindings);
        refs.push(...bindings.filter((id) => ids.has(id)));
    }
    return refs;
}
