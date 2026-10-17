import type { SnapshotDetail } from "@/services/api/tool-snapshots";

export function SnapshotReferences({ references }: { references?: SnapshotDetail["references"] }) {
    const workers = [...new Set([...(references?.workerIds ?? []), ...(references?.workers ?? [])])];
    const runs = references?.runs ?? [];
    return (
        <div aria-label="快照关联引用">
            <p>
                引用 worker（{workers.length}）：{workers.length ? workers.join("、") : "无已记录引用"}
            </p>
            <p>关联任务（{runs.length}）</p>
            {runs.length ? (
                <ul>
                    {runs.map((run) => (
                        <li key={run.runId}>
                            <code>{run.runId}</code>
                        </li>
                    ))}
                </ul>
            ) : (
                <p>无已记录任务引用</p>
            )}
        </div>
    );
}
