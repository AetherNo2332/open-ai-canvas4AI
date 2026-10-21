import type { CanvasNodeData, StoryboardRow } from "@/types/canvas";

export function storyboardDeclaredNodeIds(script: CanvasNodeData, row: StoryboardRow, nodes: CanvasNodeData[], includeFirstFrame: boolean) {
    const boundNodeIds = new Set((row.assetBindings || []).map((binding) => binding.nodeId));
    // 镜头已显式绑定某个角色卡版本时，不再按角色资产 ID 自动匹配其他版本。
    const explicitlyBoundCharacters = new Set(nodes.filter((node) => boundNodeIds.has(node.id)).map((node) => node.metadata?.characterAssetId).filter(Boolean));
    const characters = (row.characters || []).flatMap((character) => {
        if (character.characterImageNodeId) return [character.characterImageNodeId];
        if (!character.characterAssetId || explicitlyBoundCharacters.has(character.characterAssetId)) return [];
        const matches = nodes.filter((node) => node.metadata?.workflowKind === "character" && node.metadata.characterAssetId === character.characterAssetId);
        if (matches.length > 1) throw new Error("人物素材匹配多个节点，请明确选择人物图片");
        return matches.map((node) => node.id);
    });
    return Array.from(
        new Set([...(includeFirstFrame && row.imageNodeId ? [row.imageNodeId] : []), ...[...(row.assetBindings || [])].sort((a, b) => b.priority - a.priority).map((b) => b.nodeId), ...characters, ...(script.metadata?.storyboard?.referenceNodeIds || [])]),
    ).filter((id) => id !== script.id && nodes.some((n) => n.id === id));
}

// Undefined is reserved for an unnormalized legacy document. An explicit empty
// list must never fall back to visual connection lines.
export function canvasDeclaredInputNodeIds(nodeId: string, nodes: CanvasNodeData[]): string[] | undefined {
    return buildCanvasDeclaredInputResolver(nodes)(nodeId);
}

export function buildCanvasDeclaredInputResolver(nodes: CanvasNodeData[]) {
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    const outputOwners = new Map<string, { script: CanvasNodeData; row: StoryboardRow }>();
    for (const script of nodes) {
        for (const row of script.metadata?.storyboard?.rows || []) {
            for (const id of [row.imageNodeId, row.videoNodeId]) {
                if (id && !outputOwners.has(id)) outputOwners.set(id, { script, row });
            }
        }
    }
    return (nodeId: string): string[] | undefined => {
        const target = nodeById.get(nodeId);
        const owner = outputOwners.get(nodeId);
        if (owner) return storyboardDeclaredNodeIds(owner.script, owner.row, nodes, target?.type === "video" && owner.script.metadata?.storyboardVideoInputMode === "keyframe");
        const bindings = target?.metadata?.generationSpec?.referenceBindings ?? target?.metadata?.inputBindings;
        return bindings === undefined ? undefined : [...bindings].sort((a, b) => a.order - b.order).flatMap((b) => (b.nodeId ? [b.nodeId] : []));
    };
}
