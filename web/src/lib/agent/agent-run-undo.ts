import type { AgentRunSnapshot } from "@/sdk/agent";
import type { AgentChatMessage } from "./chat-types";
import { agentToolCategory, agentToolName, agentToolStatus } from "./agent-tool-presentation";

const TERMINAL = new Set<AgentRunSnapshot["status"]>(["completed", "failed", "cancelled", "rejected"]);

export function agentRunUndoneMessageId(runId: string) {
    return `undone-${runId}`;
}

function belongsToRun(id: string, runId: string) {
    return id.startsWith(`${runId}:`) || id.startsWith(`canvas-${runId}-`);
}

function detailEventType(detail: unknown) {
    return detail && typeof detail === "object" ? (detail as Record<string, unknown>).eventType : undefined;
}

/** A terminal run that changed the canvas can offer one undo action. */
export function agentRunUndoOffer(run: Pick<AgentRunSnapshot, "id" | "status"> | null | undefined, messages: AgentChatMessage[]): string | null {
    if (!run?.id || !TERMINAL.has(run.status)) return null;
    if (messages.some((item) => item.id === agentRunUndoneMessageId(run.id) || (detailEventType(item.detail) === "canvas_undone" && belongsToRun(item.id, run.id)))) return null;
    const wrote = messages.some((item) => {
        if (item.role !== "tool" || !belongsToRun(item.id, run.id)) return false;
        const name = agentToolName(item.title || "", item.detail);
        const category = agentToolCategory(name, item.detail);
        return (category === "create" || category === "operate") && name !== "canvas_undo" && agentToolStatus(item.title || "", item.text, item.detail) === "completed";
    });
    return wrote ? run.id : null;
}
