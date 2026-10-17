import type { AgentContextCompactionState, AgentEvent, AgentRun } from "@/services/api/agent";

export function reduceAgentRun(current: AgentRun | null, event: AgentEvent): AgentRun | null {
    if (!current || current.id !== event.runId) return current;
    const payload = event.payload || {};
    if (event.type === "run_status") {
        const status = String(payload.status || current.status) as AgentRun["status"];
        const terminal = ["completed", "failed", "cancelled", "rejected"].includes(status);
        const compaction = Object.hasOwn(payload, "contextCompaction") ? payload.contextCompaction : current.contextCompaction;
        return {
            ...current,
            status,
            updatedAt: event.createdAt,
            revision: Number(payload.revision || 0),
            cleanupPending: Boolean(payload.cleanupPending),
            failureMessage: String(payload.failureMessage || ""),
            skillRuntimeMode: payload.skillRuntimeMode === "pi-native" || payload.skillRuntimeMode === "legacy-go" ? payload.skillRuntimeMode : current.skillRuntimeMode,
            skills: Object.hasOwn(payload, "skills") ? (payload.skills as AgentRun["skills"]) : current.skills,
            spentCredits: Number(payload.spentCredits || 0),
            step: Number(payload.step || 0),
            approval: payload.approval && typeof payload.approval === "object" ? (payload.approval as AgentRun["approval"]) : undefined,
            contextCompaction: !terminal && compaction && typeof compaction === "object" ? (compaction as AgentContextCompactionState) : undefined,
        };
    }
    if (event.type === "context_compaction_requested") {
        return {
            ...current,
            contextCompaction: {
                status: "requested",
                sourceBytes: Number(payload.sourceBytes || 0),
                turnCount: Number(payload.turnCount ?? payload.turns ?? 0),
                resume: payload.resume !== false,
                projectedTokens: Number(payload.projectedTokens || 0),
                usableInputTokens: Number(payload.usableInputTokens || 0),
                ratio: Number(payload.pressureRatio || 0),
                tokenSource: payload.tokenSource === "provider" ? "provider" : "estimate",
            },
        };
    }
    if (event.type === "context_compacted") return { ...current, contextCompaction: undefined };
    return current;
}
