import type { AgentRunSnapshot } from "@/sdk/agent";
import type { DramaModelChoice } from "@/services/api/drama";

function hasDramaModelRoute(model?: DramaModelChoice) {
    return Boolean(model?.logicalModelId || (model?.channelId && model?.modelKey));
}

/**
 * The script-specific model is optional. An empty override object must not
 * shadow the project's configured text model.
 */
export function dramaAgentTextModel(models?: { scriptText?: DramaModelChoice; text?: DramaModelChoice }) {
    if (hasDramaModelRoute(models?.scriptText)) return models?.scriptText;
    if (hasDramaModelRoute(models?.text)) return models?.text;
    return undefined;
}

/**
 * Legacy drama links are only a pointer to a server-owned run. Validate the
 * persisted business identity before opening the shared panel.
 */
export function validatedDramaAgentRunId(run: Pick<AgentRunSnapshot, "id" | "scope" | "agentDefinitionId">, projectId: string) {
    if (!run.scope) throw new Error("历史分析链接缺少业务归属，无法安全恢复");
    if (run.scope.type !== "drama" || run.scope.id !== projectId) throw new Error("该分析属于其他作品");
    if (run.agentDefinitionId && run.agentDefinitionId !== "drama.analysis") throw new Error("该链接不是短剧分析会话");
    return run.id;
}
