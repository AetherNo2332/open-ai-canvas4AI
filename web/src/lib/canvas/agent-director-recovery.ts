import type { AgentConversation, AgentConversationPage, AgentRunReceipt, AgentRunSnapshot } from "@/sdk/agent";
import type { DirectorPreviewJob } from "./agent-director-launch";

type RecoveryClient = {
    listConversations(page: { before?: string; limit?: number }, signal?: AbortSignal): Promise<AgentConversationPage<AgentConversation>>;
    getRun(id: string, signal?: AbortSignal, query?: { eventLimit?: number }): Promise<AgentRunReceipt<AgentRunSnapshot>>;
};

/**
 * The directory is the durable index for canvases that are no longer mounted.
 * Read every page so an older conversation waiting on a preview is not hidden
 * by later completed turns.
 */
export async function findPendingCanvasDirectorPreview(client: RecoveryClient, canvasId: string, signal?: AbortSignal): Promise<DirectorPreviewJob | null> {
    let before: string | undefined;
    do {
        const page = await client.listConversations({ before, limit: 100 }, signal);
        signal?.throwIfAborted();
        for (const conversation of page.items) {
            if (conversation.archived || conversation.waitKind !== "director_preview" || conversation.scope.type !== "canvas" || conversation.scope.id !== canvasId) continue;
            const { run } = await client.getRun(conversation.latestRunId, signal, { eventLimit: 1 });
            signal?.throwIfAborted();
            const pending = run.pendingDirectorPreview;
            if (!pending || pending.canvasId !== canvasId || pending.requestId !== run.waitId || Date.parse(pending.deadline) <= Date.now()) continue;
            return {
                runId: pending.runId,
                requestId: pending.requestId,
                canvasId: pending.canvasId,
                nodeId: pending.nodeId,
                sceneId: pending.sceneId,
                shotId: pending.shotId,
                deadline: pending.deadline,
            };
        }
        before = page.nextCursor;
    } while (before);
    return null;
}
