import type { AgentRun } from "@/services/api/agent";
import { agentToolCategory, agentToolName, agentToolStatus } from "./agent-tool-presentation";

type UndoMessage = { id: string; role: string; title?: string; text: string; detail?: unknown };

const TERMINAL = new Set(["completed", "failed", "cancelled", "rejected"]);

/** 撤销完成后的时间线行 ID：本地乐观插入与 SSE 回放的 canvas_undone 共用，避免重复。 */
export function agentRunUndoneMessageId(runId: string) {
    return `undone-${runId}`;
}

function belongsToRun(id: string, runId: string) {
    return id.startsWith(`${runId}:`) || id.startsWith(`canvas-${runId}-`);
}

function detailEventType(detail: unknown) {
    return detail && typeof detail === "object" ? (detail as Record<string, unknown>).eventType : undefined;
}

/**
 * 运行结束且本轮确实写过画布、尚未撤销时，返回可撤销的运行 ID。
 * 只看本轮自己的工具行；服务端仍会按归属、冲突与已提交任务做最终校验。
 */
export function agentRunUndoOffer(run: Pick<AgentRun, "id" | "status"> | null | undefined, messages: UndoMessage[]): string | null {
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
