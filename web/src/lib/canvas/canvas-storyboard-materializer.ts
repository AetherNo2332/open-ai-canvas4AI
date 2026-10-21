import { normalizeStoryboardAssetBindings, storyboardAssetRoleForNode } from "@/lib/canvas/canvas-storyboard-assets";
import { storyboardDeclaredNodeIds } from "./canvas-reference-inputs";
import { buildOrderedCanvasResourceReferences, canvasResourceMentionToken } from "@/lib/canvas/canvas-resource-references";
import { CanvasNodeType, type CanvasConnection, type CanvasNodeData, type StoryboardAssetBinding, type StoryboardRow } from "@/types/canvas";

export function storyboardRowReferenceNodeIds(
    scriptNode: CanvasNodeData,
    row: StoryboardRow,
    nodes: CanvasNodeData[],
    _connections: CanvasConnection[],
    includeFirstFrame: boolean,
    _targetNodeId?: string,
) {
    return storyboardDeclaredNodeIds(scriptNode,row,nodes,includeFirstFrame);
}

export function storyboardComposerContent(prompt: string, referenceNodeIds: string[], nodes: CanvasNodeData[]) {
    const referenceIds = Array.from(new Set(referenceNodeIds));
    const referenceNodes = referenceIds.flatMap((nodeId) => {
        const node = nodes.find((candidate) => candidate.id === nodeId);
        return node ? [node] : [];
    });
    const mentions = buildOrderedCanvasResourceReferences(referenceNodes).map(canvasResourceMentionToken);
    return mentions.length ? [`参考资产：${mentions.join(" ")}`, prompt.trim()].filter(Boolean).join("\n") : prompt.trim();
}

export function bindingForConnectedNode(node: CanvasNodeData): StoryboardAssetBinding | null {
    const role = storyboardAssetRoleForNode(node);
    return role ? { nodeId: node.id, role, priority: defaultRolePriority(role) } : null;
}

// 镜头资产选择保存前的强校验：镜头仍存在、分镜未锁定、所选节点仍是可预览资产。
// 返回规范化后的绑定，交给 set_row_assets 意图写入；引用线与提示词同步由意图层负责。
export function validateStoryboardRowAssetSelection(nodes: CanvasNodeData[], scriptNodeId: string, rowId: string, bindings: StoryboardAssetBinding[]) {
    const script = nodes.find((node) => node.id === scriptNodeId && node.type === CanvasNodeType.Script);
    const row = script?.metadata?.storyboard?.rows.find((item) => item.id === rowId);
    if (!script || !row) throw new Error("分镜或镜头已不存在，请重新选择");
    if (script.metadata?.locked) throw new Error("请先解锁分镜节点");
    const normalized = normalizeStoryboardAssetBindings(bindings, nodes);
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    if (normalized.length !== bindings.length || normalized.some((binding) => binding.nodeId === scriptNodeId || !isStoryboardPreviewAsset(nodeById.get(binding.nodeId)))) {
        throw new Error("所选资产已失效，请重新选择");
    }
    return normalized;
}

export function isStoryboardPreviewAsset(node: CanvasNodeData | undefined) {
    return Boolean(node && (node.type === CanvasNodeType.Image || node.type === CanvasNodeType.Drawing || node.type === CanvasNodeType.Video || node.type === CanvasNodeType.Audio || node.metadata?.workflowKind === "character"));
}

function defaultRolePriority(role: StoryboardAssetBinding["role"]) {
    if (role === "character") return 100;
    if (role === "environment") return 90;
    if (role === "prop" || role === "weapon" || role === "wardrobe") return 80;
    if (role === "motion" || role === "audio") return 70;
    return 60;
}
