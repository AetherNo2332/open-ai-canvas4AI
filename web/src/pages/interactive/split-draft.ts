import type { SplitIssue, SplitRow } from "@/lib/interactive/split-types";

export interface SplitDraft {
    id: string;
    projectId: string;
    /** 同一项目内递增；重新拆分 +1，旧草稿保留。 */
    revision: number;
    createdAt: number;
    promptHash: string;
    title: string;
    rows: SplitRow[];
    issues: SplitIssue[];
    /** 提交后立即落盘，崩溃后可用同一幂等键恢复，不会重复计费。 */
    taskId?: string;
    adoptedAt?: number;
    nodeIdByRowKey?: Record<string, string>;
}

const STORAGE_KEY = "yingce.interactive.splitDrafts.v1";

type Store = Record<string, SplitDraft[]>;

function readStore(): Store {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (!raw) return {};
        const parsed = JSON.parse(raw) as unknown;
        if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
        return parsed as Store;
    } catch {
        return {};
    }
}

function writeStore(store: Store) {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(store));
    } catch {
        // 配额满时保留内存态即可；草稿丢失不应打断导入流程。
    }
}

export function listDrafts(projectId: string): SplitDraft[] {
    return [...(readStore()[projectId] ?? [])].sort((a, b) => b.revision - a.revision);
}

export function latestDraft(projectId: string): SplitDraft | null {
    return listDrafts(projectId)[0] ?? null;
}

export function nextRevision(projectId: string): number {
    const drafts = readStore()[projectId] ?? [];
    return drafts.reduce((max, draft) => Math.max(max, draft.revision), 0) + 1;
}

export function createDraft(input: { projectId: string; title: string; promptHash: string; rows: SplitRow[]; issues: SplitIssue[]; taskId?: string }): SplitDraft {
    const draft: SplitDraft = {
        id: `split_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 6)}`,
        projectId: input.projectId,
        revision: nextRevision(input.projectId),
        createdAt: Date.now(),
        promptHash: input.promptHash,
        title: input.title,
        rows: input.rows,
        issues: input.issues,
        ...(input.taskId ? { taskId: input.taskId } : {}),
    };
    const store = readStore();
    store[input.projectId] = [...(store[input.projectId] ?? []), draft];
    writeStore(store);
    return draft;
}

export function updateDraft(projectId: string, draftId: string, patch: Partial<SplitDraft>): SplitDraft | null {
    const store = readStore();
    const drafts = store[projectId] ?? [];
    const index = drafts.findIndex((draft) => draft.id === draftId);
    if (index < 0) return null;
    const next = { ...drafts[index], ...patch };
    drafts[index] = next;
    store[projectId] = drafts;
    writeStore(store);
    return next;
}

export function markAdopted(projectId: string, draftId: string, nodeIdByRowKey: Record<string, string>): SplitDraft | null {
    return updateDraft(projectId, draftId, { adoptedAt: Date.now(), nodeIdByRowKey });
}
