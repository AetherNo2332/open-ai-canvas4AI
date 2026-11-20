import { createAgentClient } from "@/sdk/agent/client";
import { agentWebTransport } from "@/sdk/agent/http-transport";
import type { AgentRunRequest, AgentRunSnapshot } from "@/sdk/agent/contract";
import type { DramaModelChoice } from "./drama";

export const dramaAgent = createAgentClient({ transport: agentWebTransport });

/** Business rules, episode ownership, model and budget validation stay in Go. */
export function dramaAgentAnalysisRequest(input: { projectId: string; episodeId: string; model: DramaModelChoice; prompt: string; maxCredits: number; idempotencyKey: string }): AgentRunRequest {
    return {
        scope: { type: "drama", id: input.projectId },
        canvasId: "",
        dramaEpisodeId: input.episodeId,
        prompt: input.prompt,
        permissionMode: "read_only",
        contextScope: [],
        logicalModelId: input.model.logicalModelId,
        channelId: input.model.channelId,
        channelModelKey: input.model.modelKey,
        budget: { maxCredits: input.maxCredits, maxSteps: 12 },
        idempotencyKey: input.idempotencyKey,
    };
}

export const startDramaAgentAnalysis = (input: Parameters<typeof dramaAgentAnalysisRequest>[0]): Promise<{ run: AgentRunSnapshot }> => dramaAgent.startRun(dramaAgentAnalysisRequest(input));
