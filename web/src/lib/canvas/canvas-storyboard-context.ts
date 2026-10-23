import type { CanvasNodeData } from "@/types/canvas";

export type StoryboardGenerationContext = {
    projectStyle?: {
        presetId?: string;
        title: string;
        prompt: string;
        profileJson?: string;
    };
    characters: Array<{
        assetId: string;
        versionId: string;
        name: string;
        definition: Record<string, unknown>;
    }>;
};

// 画风提供参考而非规划门槛；实际引用角色仍需使用已同步的资产版本。
export function resolveStoryboardGenerationContext(nodes: CanvasNodeData[]): StoryboardGenerationContext {
    const styleNode = nodes.find((node) => node.metadata?.workflowKind === "styleboard");
    const stylePrompt = String(styleNode?.metadata?.content || styleNode?.metadata?.prompt || "").trim();
    const stylePresetId = String(styleNode?.metadata?.stylePresetId || "").trim();

    // `workflowKind=character` is also used by standalone character-design
    // image workflows. Only nodes linked to a project character asset are
    // storyboard character cards and therefore participate in this check.
    const characterNodes = nodes.filter((node) => node.metadata?.workflowKind === "character" && node.metadata?.characterAssetId?.trim());
    const invalidCharacter = characterNodes.find((node) => !node.metadata?.characterAssetId?.trim() || !node.metadata?.characterVersionId?.trim() || !(node.metadata?.characterName || node.title).trim());
    if (invalidCharacter) throw new Error(`角色卡“${invalidCharacter.metadata?.characterName || invalidCharacter.title || "未命名角色"}”版本未同步，请刷新角色资产后再生成分镜`);

    return {
        projectStyle: styleNode && stylePrompt ? {
            presetId: stylePresetId || undefined,
            title: styleNode.title.replace(/^(?:项目)?画风\s*[·：:]?\s*/, "").trim() || styleNode.title,
            prompt: stylePrompt,
            profileJson: styleNode.metadata?.styleProfileJson,
        } : undefined,
        characters: characterNodes.map((node) => ({
            assetId: node.metadata!.characterAssetId!.trim(),
            versionId: node.metadata!.characterVersionId!.trim(),
            name: (node.metadata?.characterName || node.title).trim(),
            definition: node.metadata?.characterDefinition || { prompt: node.metadata?.characterPrompt || "" },
        })),
    };
}

// Expose the same generation prerequisites to the Agent before it starts a workflow.
export function inspectStoryboardReadiness(nodes: CanvasNodeData[]) {
    let generationContext: StoryboardGenerationContext | undefined;
    let blockingReason: string | undefined;
    try { generationContext = resolveStoryboardGenerationContext(nodes); }
    catch (error) { blockingReason = error instanceof Error ? error.message : "分镜上下文未就绪"; }
    const styleNode = nodes.find((node) => node.metadata?.workflowKind === "styleboard");
    const styleReady = Boolean(String(styleNode?.metadata?.content || styleNode?.metadata?.prompt || "").trim());
    const storyboards = nodes.filter((node) => node.type === "script").map((node) => {
        const rows = node.metadata?.storyboard?.rows || [];
        const incompleteRowIds = rows.filter((row) => !Number.isFinite(row.durationSeconds) || row.durationSeconds <= 0 || !row.videoMotionPrompt?.trim()).map((row) => row.id);
        return { nodeId: node.id, title: node.title, rowCount: rows.length, populated: rows.length > 0 && incompleteRowIds.length === 0, incompleteRowIds, totalDurationSeconds: rows.reduce((total, row) => total + (Number.isFinite(row.durationSeconds) && row.durationSeconds > 0 ? row.durationSeconds : 0), 0) };
    });
    return {
        canGenerateStoryboard: Boolean(generationContext), blockingReason,
        style: { ready: styleReady, nodeId: styleNode?.id, presetId: styleNode?.metadata?.stylePresetId, title: styleNode?.title },
        storyboards,
        nextActions: [
            "由当前 Agent 结合项目基调、上下文及适用技能提出拆镜方案，先询问用户确认，再编辑目标分镜表；画风预设不是规划前置条件。",
            ...(!styleReady ? ["当前没有画风正文，可根据剧情提出合适的基调并请用户确认，不必先创建画风节点。"] : []),
            ...(storyboards.some((item) => !item.populated) ? ["先 canvas_read_storyboard 读取真实行 ID，再 canvas_edit_storyboard 更新空行并按需新增镜头；正文或标题不代表已填充分镜表。"] : []),
        ],
    };
}
