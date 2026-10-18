import { http } from "./request";

export type SnapshotSummary = { id: string; source: string; instanceId: string; buildCommit: string; schemaVersion: string; toolCount: number; createdAt: string; toolNames?: string[] };
export type SnapshotListQuery = { page: number; pageSize: number; source?: string; tool?: string; version?: string };
export type ToolDifference = { toolName: string; path: string; kind: string; before?: unknown; after?: unknown };
export type SnapshotWorker = {
    instanceId: string;
    bootId: string;
    snapshotId: string;
    status: "matched" | "mismatch" | "unknown" | "offline" | "invalid";
    online: boolean;
    manualDraining: boolean;
    revision: number;
    lastHeartbeatAt: string;
    differences: ToolDifference[];
    affectedRuns: number;
    unfinishedRuns?: number;
    artifactFileState?: "loaded" | "changed" | "unavailable" | "unknown";
};
export type SnapshotAlert = { code: string; severity: "warning" | "error"; title: string; description: string; workerIds: string[]; toolNames: string[]; firstSeenAt?: string; lastSeenAt?: string; resolvedAt?: string; backendSnapshotId?: string; workerSnapshotId?: string; historicalOnly?: boolean };
export type SnapshotHealth = { generatedAt: string; backend: SnapshotSummary; workers: SnapshotWorker[]; alerts: SnapshotAlert[]; history?: SnapshotAlert[]; affectedRuns: number; truncated?: boolean };
export type SnapshotDetail = SnapshotSummary & {
    artifact: { schemaVersion: string; tools: { type: string; function: { name: string; description?: string; parameters: unknown } }[] };
    references?: { workerIds?: string[]; workers?: string[]; affectedRuns?: number; runs?: { runId: string }[] };
};
export type SnapshotComparison = { differences: ToolDifference[]; compatible: boolean; comparatorVersion?: string; checkedAt?: string };
const base = "/admin/agent";
export const getSnapshotHealth = (signal?: AbortSignal) => http.get<SnapshotHealth>(`${base}/tool-snapshot-health`, { signal });
export const listToolSnapshots = (params: SnapshotListQuery, signal?: AbortSignal) => http.get<{ snapshots: SnapshotSummary[]; total: number; page?: number; pageSize?: number }>(`${base}/tool-snapshots`, { params, signal });
export const getToolSnapshot = (id: string, signal?: AbortSignal) => http.get<SnapshotDetail>(`${base}/tool-snapshots/${encodeURIComponent(id)}`, { signal });
export const compareToolSnapshots = (left: string, right: string, signal?: AbortSignal) => http.get<SnapshotComparison>(`${base}/tool-snapshots/compare`, { params: { left, right }, signal });
export const recheckToolSnapshots = (signal?: AbortSignal) => http.post<SnapshotHealth>(`${base}/tool-snapshot-checks`, {}, { signal });
export const setWorkerDraining = (id: string, body: { expectedRevision: number; draining: boolean; reason: string }) => http.patch<unknown>(`${base}/runtime-instances/${encodeURIComponent(id)}/drain`, body);
