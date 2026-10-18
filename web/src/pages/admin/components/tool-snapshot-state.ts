import type { SnapshotDetail, SnapshotHealth, SnapshotWorker, SnapshotListQuery } from "@/services/api/tool-snapshots";

export function snapshotListQuery(params: URLSearchParams): SnapshotListQuery {
    const requested = Number(params.get("snapshotPage") || "1");
    return { page: Number.isSafeInteger(requested) && requested > 0 ? requested : 1, pageSize: 20, source: params.get("snapshotSource") || undefined, tool: params.get("snapshotTool") || undefined, version: params.get("snapshotVersion") || undefined };
}

export function snapshotSearchParams(previous: URLSearchParams, values: Record<string, string>) {
    const next = new URLSearchParams(previous);
    Object.entries(values).forEach(([key, value]) => (value ? next.set(key, value) : next.delete(key)));
    if (["snapshotSource", "snapshotTool", "snapshotVersion", "snapshotStatus"].some((key) => key in values)) next.set("snapshotPage", "1");
    return next;
}

export function healthPresentation(health: SnapshotHealth | null, error: string): { type: "warning" | "error" | "success"; title: string } {
    if (error || !health?.backend?.id || !Array.isArray(health.workers) || !Array.isArray(health.alerts)) return { type: "warning", title: "工具快照状态未知" };
    if (health.affectedRuns > 0 || health.workers.some((w) => w.status === "invalid") || health.alerts.some((a) => a.severity === "error")) return { type: "error", title: "工具快照异常 · 运行受影响" };
    if (health.truncated || !health.workers.length || health.alerts.length || health.workers.some((w) => !w.online || w.status !== "matched" || w.artifactFileState === "changed" || w.artifactFileState === "unavailable")) return { type: "warning", title: "工具快照需要关注" };
    return { type: "success", title: "当前在线 worker 工具快照一致" };
}

// No analytics filters enter this lifecycle. Hidden tabs abort in-flight reads.
export function startHealthPolling(options: {
    visible: () => boolean;
    listen: (fn: () => void) => () => void;
    interval: (fn: () => void, ms: number) => () => void;
    load: (signal: AbortSignal) => Promise<SnapshotHealth>;
    update: (health: SnapshotHealth | null, error: string) => void;
}) {
    let controller: AbortController | undefined;
    let stopped = false;
    const refresh = () => {
        controller?.abort();
        if (stopped || !options.visible()) return;
        const current = new AbortController();
        controller = current;
        void options.load(current.signal).then(
            (value) => {
                if (!current.signal.aborted && !stopped) options.update(value, "");
            },
            (error: unknown) => {
                if (!current.signal.aborted && !stopped) options.update(null, error instanceof Error ? error.message : "检测失败");
            },
        );
    };
    const unlisten = options.listen(refresh);
    const clear = options.interval(refresh, 15000);
    refresh();
    return () => {
        stopped = true;
        controller?.abort();
        unlisten();
        clear();
    };
}

export async function runDrainOperation(worker: SnapshotWorker, draining: boolean, reason: string, mutate: (id: string, body: { expectedRevision: number; draining: boolean; reason: string }) => Promise<unknown>, refresh: () => Promise<unknown>) {
    if (!reason.trim()) throw new Error("请填写操作原因");
    if (reason.trim().length > 500) throw new Error("操作原因最多 500 字");
    if (!Number.isFinite(worker.revision) || worker.revision < 1) throw new Error("实例版本未知，请重新检测");
    try {
        return await mutate(worker.instanceId, { expectedRevision: worker.revision, draining, reason: reason.trim() });
    } finally {
        await refresh();
    }
}

export function diagnosticExport(health: SnapshotHealth | null, details: SnapshotDetail[]) {
    const summary = (s: SnapshotHealth["backend"]) => ({ id: s.id, source: s.source, instanceId: s.instanceId, buildCommit: s.buildCommit, schemaVersion: s.schemaVersion, toolCount: s.toolCount, createdAt: s.createdAt });
    return {
        generatedAt: health?.generatedAt,
        backend: health?.backend ? summary(health.backend) : null,
        workers: (health?.workers ?? []).map((w) => ({
            instanceId: w.instanceId,
            snapshotId: w.snapshotId,
            status: w.status,
            online: w.online,
            manualDraining: w.manualDraining,
            revision: w.revision,
            affectedRuns: w.affectedRuns,
            differences: (w.differences ?? []).map((d) => ({ toolName: d.toolName, path: d.path, kind: d.kind, before: d.before, after: d.after })),
        })),
        snapshots: details.map((s) => ({
            ...summary(s),
            artifact: { schemaVersion: s.artifact.schemaVersion, tools: (s.artifact.tools ?? []).map((t) => ({ type: t.type, function: { name: t.function.name, description: t.function.description, parameters: t.function.parameters } })) },
        })),
    };
}
