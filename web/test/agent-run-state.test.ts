import { expect, test } from "bun:test";
import { reduceAgentRun } from "@/lib/canvas/agent-run-state";
import { emptyAgentContextUsage, reduceAgentContextUsage } from "@/lib/canvas/agent-context-usage";
import type { AgentEvent, AgentRun } from "@/services/api/agent";

const run: AgentRun = { id: "run", canvasId: "canvas", status: "running", permissionMode: "auto", createdAt: "", updatedAt: "" };
const event = (type: string, payload: Record<string, unknown>): AgentEvent => ({ runId: "run", eventId: "event", seq: 1, type, payload, createdAt: "now" });

test("Pi semantic compaction updates the run title and meter until it completes", () => {
    const requested = event("context_compaction_requested", { kind: "semantic_compaction", turns: 4, sourceBytes: 10000 });
    const active = reduceAgentRun(run, requested);
    expect(active?.contextCompaction).toMatchObject({ status: "requested", turnCount: 4, sourceBytes: 10000, resume: true });
    const usage = reduceAgentContextUsage(emptyAgentContextUsage("run"), requested);
    expect(usage.compactionPending).not.toBeNull();
    // Status-only durable events do not clear compaction before its receipt.
    const running = event("run_status", { status: "running" });
    expect(reduceAgentRun(active, running)?.contextCompaction).toEqual(active?.contextCompaction);
    expect(reduceAgentContextUsage(usage, running).compactionPending).toEqual(usage.compactionPending);
    const completed = event("context_compacted", { mode: "checkpoint", resume: true });
    expect(reduceAgentRun(active, completed)?.contextCompaction).toBeUndefined();
    expect(reduceAgentContextUsage(usage, completed).compactionPending).toBeNull();
});

test("reconnect snapshots restore and clear compaction even when run status stays running", () => {
    const snapshot = event("run_status", { status: "running", contextCompaction: { status: "running", turnCount: 4 } });
    const active = reduceAgentRun(run, snapshot);
    const usage = reduceAgentContextUsage(emptyAgentContextUsage("run"), snapshot);
    expect(active?.contextCompaction?.status).toBe("running");
    expect(usage.compactionPending?.turnCount).toBe(4);
    const cleared = event("run_status", { status: "running", contextCompaction: undefined });
    expect(reduceAgentRun(active, cleared)?.contextCompaction).toBeUndefined();
    expect(reduceAgentContextUsage(usage, cleared).compactionPending).toBeNull();
});

test("terminal status clears stale compaction and another run cannot alter current state", () => {
    const active = { ...run, contextCompaction: { status: "running" as const } };
    expect(reduceAgentRun(active, event("run_status", { status: "cancelled" }))?.contextCompaction).toBeUndefined();
    expect(reduceAgentRun(active, { ...event("context_compacted", {}), runId: "other-run" })).toBe(active);
    expect(reduceAgentRun(null, event("run_status", { status: "running" }))).toBeNull();
});
