import type { CanvasNodeData, StoryboardRow } from "@/types/canvas";

export function storyboardDeclaredNodeIds(script: CanvasNodeData, row: StoryboardRow, nodes: CanvasNodeData[], includeFirstFrame: boolean) {
    const characters = (row.characters || []).flatMap((character) => {
        if (character.characterImageNodeId) return [character.characterImageNodeId];
        if (!character.characterAssetId) return [];
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
    const target = nodes.find((n) => n.id === nodeId);
    for (const script of nodes) {
        const row = script.metadata?.storyboard?.rows.find((r) => r.imageNodeId === nodeId || r.videoNodeId === nodeId);
        if (row) return storyboardDeclaredNodeIds(script, row, nodes, target?.type === "video" && script.metadata?.storyboardVideoInputMode === "keyframe");
    }
    const bindings = target?.metadata?.generationSpec?.referenceBindings ?? target?.metadata?.inputBindings;
    return bindings === undefined ? undefined : [...bindings].sort((a, b) => a.order - b.order).flatMap((b) => (b.nodeId ? [b.nodeId] : []));
}
