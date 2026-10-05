import { expect, test } from "bun:test";

import { buildObservabilityParams, mergeObservabilityFailures } from "@/pages/admin/components/observability-view";

test("uses the analytics filter set for the agent overview request", () => {
    const filters = { from: "2026-09-01", to: "2026-10-01", userId: "user-1", model: "model-a", channelId: "channel-1", capability: "text" };

    expect(buildObservabilityParams(filters)).toEqual(filters);
});

test("merges recent agent failures into the deep-analysis failure rows", () => {
    const rows = mergeObservabilityFailures(
        [{ type: "上游服务", model: "model-a", count: 2, lastError: "timeout", lastSeenAt: "2026-10-01T10:00:00Z" }],
        [{ taskId: "task-1", runId: "run-1", traceId: "trace-1", status: "failed", reason: "tool failed", at: "2026-10-01T11:00:00Z" }],
    );

    expect(rows).toHaveLength(2);
    expect(rows[1]).toMatchObject({ type: "Agent 任务", model: "Agent", count: 1, lastError: "tool failed", taskId: "task-1", traceId: "trace-1" });
});
