import type { AnalyticsFilters, AdminAnalytics } from "@/services/api/auth";
import type { AdminObservabilityOverview } from "@/services/api/observability";

export type ObservabilityFailureRow = AdminAnalytics["failures"][number] & {
    taskId?: string;
    runId?: string;
    traceId?: string;
    source?: "api" | "agent";
};

export function buildObservabilityParams(filters: AnalyticsFilters): AnalyticsFilters {
    return { ...filters };
}

export function mergeObservabilityFailures(base: AdminAnalytics["failures"], recent: AdminObservabilityOverview["recentFailures"]): ObservabilityFailureRow[] {
    const rows: ObservabilityFailureRow[] = base.map((row) => ({ ...row, source: "api" }));
    for (const failure of recent) {
        rows.push({
            type: "Agent 任务",
            model: "Agent",
            count: 1,
            lastError: failure.reason || failure.status,
            lastSeenAt: failure.at,
            taskId: failure.taskId,
            runId: failure.runId,
            traceId: failure.traceId,
            source: "agent",
        });
    }
    return rows;
}
