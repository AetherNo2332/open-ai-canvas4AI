import type { AgentPanelAdapter } from "../panel-adapters";

export const dramaAgentPanelAdapter: AgentPanelAdapter = {
    configId: "drama",
    owner: "drama-default",
    buildRequest: ({ context, prompt, idempotencyKey, selection }) => {
        const episodeId = selection.episodeId || context.target?.dramaEpisodeId || "";
        if (!episodeId) throw new Error("请选择本轮分析剧集");
        return {
            scope: { type: "drama", id: context.scope?.id || "" },
            canvasId: "",
            dramaEpisodeId: episodeId,
            prompt,
            permissionMode: "read_only",
            contextScope: [],
            reasoningMode: selection.reasoningMode,
            ...(selection.model ? { model: selection.model } : {}),
            ...(selection.logicalModelId ? { logicalModelId: selection.logicalModelId } : {}),
            ...(!selection.logicalModelId && selection.channelId ? { channelId: selection.channelId, channelModelKey: selection.channelModelKey } : {}),
            ...(selection.skillIds.length ? { skillIds: selection.skillIds } : {}),
            budget: { maxCredits: selection.maxCredits ?? 20, maxSteps: 12 },
            idempotencyKey,
        };
    },
};
