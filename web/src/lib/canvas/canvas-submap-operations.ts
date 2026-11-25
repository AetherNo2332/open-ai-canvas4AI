import type { CanvasNodeData } from "@/types/canvas";

import { getSubmapChildren, isSubmapNode } from "./canvas-submap";

/** 复制 submap 时参与复制的节点集合：容器 + 全部子节点。 */
export function submapCopySources(source: CanvasNodeData, nodes: CanvasNodeData[]) {
    return [source, ...getSubmapChildren(source.id, nodes)];
}

/** 节点自身是 submap，或直接归属于本批复制的 submap 时，副本必须保持独立作用域。 */
export function isCopiedSubmapTreeNode(node: CanvasNodeData, copiedSubmapIds: ReadonlySet<string>) {
    return copiedSubmapIds.has(node.id) || Boolean(node.scopeId && copiedSubmapIds.has(node.scopeId));
}

/** 复制后指向 scope 外的引用数量；外部边与外部引用都不复制。 */
export function countSubmapExternalReferences(nodes: CanvasNodeData[], copiedIds: ReadonlySet<string>) {
    const isLost = (id: string) => !copiedIds.has(id);
    let count = 0;
    for (const node of nodes) {
        if (!copiedIds.has(node.id)) continue;
        const metadata = node.metadata;
        if (!metadata) continue;
        count += (metadata.referenceNodeIds || []).filter(isLost).length;
        count += (metadata.referenceTextNodeIds || []).filter(isLost).length;
        count += (metadata.derivedFromNodeIds || []).filter(isLost).length;
        if (metadata.videoStartFrameNodeId && isLost(metadata.videoStartFrameNodeId)) count += 1;
        if (metadata.videoEndFrameNodeId && isLost(metadata.videoEndFrameNodeId)) count += 1;
    }
    return count;
}

/**
 * 为一份节点快照分配副本 ID。先发 submap 容器号，再按新容器发
 * `${newScopeId}__${type}-N` 子节点号，保证原始与副本各自引用自己的子树。
 */
export function allocateCopiedCanvasNodeIds(nodes: CanvasNodeData[], allocateId: (prefix: string) => string) {
    const idMap = new Map<string, string>();
    for (const node of nodes.filter(isSubmapNode)) idMap.set(node.id, allocateId(node.type));
    for (const node of nodes.filter((candidate) => !isSubmapNode(candidate))) {
        const copiedScopeId = node.scopeId ? idMap.get(node.scopeId) : undefined;
        idMap.set(node.id, allocateId(copiedScopeId ? `${copiedScopeId}__${node.type}` : node.type));
    }
    return idMap;
}
