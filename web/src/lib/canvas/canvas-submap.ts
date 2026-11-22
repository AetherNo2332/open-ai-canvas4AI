import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

/**
 * submap 是画布内的子画布容器：作用域命名空间，不是视觉分区。
 * 子节点仍在顶层 nodes 数组里，靠 scopeId 归属；parentId 仍是背板的视觉容纳字段。
 * v1 不允许嵌套 submap。
 */
export function isSubmapNode(node?: CanvasNodeData | null): node is CanvasNodeData & { type: CanvasNodeType.Submap } {
    return node?.type === CanvasNodeType.Submap;
}

export function getSubmapChildren(submapId: string, nodes: CanvasNodeData[]) {
    return nodes.filter((node) => node.scopeId === submapId);
}

export function getSubmapChildIds(submapId: string, nodes: CanvasNodeData[]) {
    return new Set(getSubmapChildren(submapId, nodes).map((node) => node.id));
}

/** 全局 ID 去掉 `${scopeId}__` 前缀即 localId；localId 不落库。 */
export function canvasSubmapLocalId(scopeId: string, nodeId: string) {
    return nodeId.startsWith(`${scopeId}__`) ? nodeId.slice(scopeId.length + 2) : nodeId;
}

/** 节点在画布上的展示路径：submap 子节点显示 `submap-1 / image-1`。 */
export function canvasNodeDisplayPath(node: CanvasNodeData) {
    if (!node.scopeId) return node.id;
    return `${node.scopeId} / ${canvasSubmapLocalId(node.scopeId, node.id)}`;
}

/** 子节点所属的 submap；未归属或归属对象已不存在时返回 null。 */
export function getSubmapParent(node: CanvasNodeData, nodes: CanvasNodeData[]) {
    if (!node.scopeId) return null;
    const parent = nodes.find((candidate) => candidate.id === node.scopeId);
    return parent && isSubmapNode(parent) ? parent : null;
}

/** collapsed 只隐藏子节点，不改变作用域语义。 */
export function isNodeHiddenByCollapsedSubmap(node: CanvasNodeData, nodes: CanvasNodeData[]) {
    const parent = getSubmapParent(node, nodes);
    return Boolean(parent?.metadata?.collapsed);
}

/** 拖 submap 时同步位移的子节点 ID 集合（v1 只有一层）。 */
export function submapDragChildIds(submapId: string, nodes: CanvasNodeData[]) {
    return getSubmapChildIds(submapId, nodes);
}
