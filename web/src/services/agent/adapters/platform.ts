import type { AgentPanelAdapter } from "../panel-adapters";

export const platformAgentPanelAdapter: AgentPanelAdapter = {
    configId: "platform",
    owner: "platform-default",
    buildRequest: ({ prompt, idempotencyKey, selection }) => ({
        scope: { type: "platform", id: "self" },
        canvasId: "",
        prompt,
        permissionMode: "read_only",
        contextScope: [],
        budget: { maxCredits: 20, maxSteps: 12 },
        reasoningMode: selection.reasoningMode,
        ...(selection.model ? { model: selection.model } : {}),
        ...(selection.logicalModelId ? { logicalModelId: selection.logicalModelId } : {}),
        ...(!selection.logicalModelId && selection.channelId ? { channelId: selection.channelId, channelModelKey: selection.channelModelKey } : {}),
        ...(selection.skillIds.length ? { skillIds: selection.skillIds } : {}),
        idempotencyKey,
    }),
};
