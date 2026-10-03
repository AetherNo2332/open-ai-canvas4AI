import { http } from "@/services/api/request";

export type AdminObservabilityOverview = {
    available: boolean;
    delayed: boolean;
    generatedAt: string;
    window: number;
    grafanaUrl?: string;
    worker: { online: number; busy: number; busyRatio: number };
    queue: { depth: number; oldestAgeSeconds: number };
    tasks: { total: number; completed: number; failed: number; retried: number; successRate: number; p50LatencyMs: number; p95LatencyMs: number; p99LatencyMs: number };
    tools: { calls: number; succeeded: number; successRate: number; stepsPerTask: number };
    llm: { calls: number; inputTokens: number; outputTokens: number; cachedTokens: number; reasoningTokens: number };
    cost: { microcredits?: number; perSuccessfulTask?: number; known: boolean };
    quality: { score?: number; feedbackCount: number };
    alerts: string[];
    recentFailures: Array<{ taskId: string; runId: string; traceId: string; status: string; reason: string; at: string }>;
};

export function getAdminObservabilityOverview(windowSeconds = 900) {
    return http.get<AdminObservabilityOverview>("/admin/observability/overview", { params: { window: windowSeconds } });
}

export function normalizeGrafanaUrl(raw?: string) {
    if (!raw) return "";
    try {
        const url = new URL(raw);
        if (url.protocol !== "http:" && url.protocol !== "https:") return "";
        if (url.username || url.password || !url.host) return "";
        return url.toString().replace(/\/$/, "");
    } catch {
        return "";
    }
}
