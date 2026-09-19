import type { CanvasProject } from "@/stores/canvas/use-canvas-store";
import type { CanvasConnection, CanvasNodeData } from "@/types/canvas";

type Change<T> = { before: T | null; after: T | null };
export type AgentCanvasPatch = {
    canvasId: string;
    baseRevision?: number;
    revision?: number;
    updatedAt: string;
    nodes: Change<CanvasNodeData>[];
    connections: Change<CanvasConnection>[];
};

function record(value: unknown): value is Record<string, unknown> {
    return value !== null && typeof value === "object" && !Array.isArray(value);
}

function equal(a: unknown, b: unknown): boolean {
    if (a === b) return true;
    if (record(a) && record(b)) {
        const keys = Object.keys(a);
        return keys.length === Object.keys(b).length && keys.every((key) => equal(a[key], b[key]));
    }
    return Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every((item, index) => equal(item, b[index]));
}

// Three-way merge: untouched local fields (e.g. a drag or prompt edit) survive.
// A concurrently changed field or a deleted node is a conflict, never an overwrite.
function mergeValue(current: unknown, before: unknown, after: unknown): unknown {
    if (equal(before, after) || equal(current, after)) return current;
    if (equal(current, before)) return after;
    if (record(current) && record(before) && record(after)) {
        const next = { ...current };
        for (const key of new Set([...Object.keys(before), ...Object.keys(after)])) {
            if (["__proto__", "constructor", "prototype"].includes(key)) throw new Error("无效的画布增量字段");
            const value = mergeValue(current[key], before[key], after[key]);
            if (value === undefined) delete next[key];
            else next[key] = value;
        }
        return next;
    }
    const keyed = mergeKeyedArray(current, before, after);
    if (keyed !== undefined) return keyed;
    throw new Error("Agent 画布增量与本地内容冲突，需要校准；已保留本地编辑");
}

function keyedItems(value: unknown) {
    if (!Array.isArray(value)) return null;
    const items = new Map<string, Record<string, unknown>>();
    for (const item of value) {
        if (!record(item) || typeof item.id !== "string" || !item.id || items.has(item.id)) return null;
        items.set(item.id, item);
    }
    return items;
}

/**
 * 以 id 为键的数组按 id 做三方合并。
 *
 * 分镜行、批量表任务行这类列表的正文很长，服务端为控制运行态体积只会下发"变化的那几行"；
 * 元素级合并让稀疏增量合法且安全：本地改过的行仍然按字段合并，服务端没提到的行原样保留。
 * 只有三边都是"带唯一 id 的数组"时才走这条路径，其余情况返回 undefined 交给原来的严格判定。
 */
function mergeKeyedArray(current: unknown, before: unknown, after: unknown): unknown {
    const currentItems = keyedItems(current);
    const beforeItems = keyedItems(before);
    const afterItems = keyedItems(after);
    if (!currentItems || !beforeItems || !afterItems) return undefined;
    const merged = new Map<string, unknown>(currentItems);
    const created: string[] = [];
    for (const id of afterItems.keys()) {
        if (!currentItems.has(id) && !beforeItems.has(id)) created.push(id);
    }
    for (const id of new Set<string>([...beforeItems.keys(), ...afterItems.keys()])) {
        const value = mergeValue(merged.get(id) ?? null, beforeItems.get(id) ?? null, afterItems.get(id) ?? null);
        if (value === null || value === undefined) merged.delete(id);
        else merged.set(id, value);
    }
    // 保持本地顺序，新增行按服务端返回的顺序追加在末尾。
    const result: unknown[] = [];
    for (const id of currentItems.keys()) if (merged.has(id)) result.push(merged.get(id));
    for (const id of created) if (merged.has(id)) result.push(merged.get(id));
    return result;
}

function mergeItems<T extends { id: string }>(items: T[], changes: Change<T>[]): T[] {
    if (!changes.length) return items;
    const next = new Map(items.map((item) => [item.id, item]));
    for (const { before, after } of changes) {
        const id = after?.id || before?.id;
        if (!id || (before && after && before.id !== after.id)) throw new Error("无效的画布增量节点");
        const merged = mergeValue(next.get(id) ?? null, before, after) as T | null;
        if (merged === null) next.delete(id);
        else next.set(id, merged);
    }
    const result = [...next.values()];
    return result.length === items.length && result.every((item, index) => item === items[index]) ? items : result;
}

export function applyAgentCanvasPatch(project: CanvasProject, patch: AgentCanvasPatch): CanvasProject {
    if (patch.canvasId !== project.id || !Array.isArray(patch.nodes) || !Array.isArray(patch.connections)) throw new Error("画布增量不属于当前画布或格式无效");
    const byId = new Map(project.nodes.map((node) => [node.id, node]));
    const nodeChanges = patch.nodes.map((change) => {
        if (!change.after) return change;
        const current = byId.get(change.after.id);
        const beforeTask = change.before?.metadata?.taskId;
        const afterTask = change.after.metadata?.taskId;
        if (beforeTask && current?.metadata?.taskId !== beforeTask && current?.metadata?.taskId !== afterTask) throw new Error("画布增量冲突：生成节点已删除或绑定另一任务，不能覆盖");
        if (!current || !beforeTask || beforeTask !== afterTask) return change;
        const terminal = (status: string | undefined) => ["succeeded", "failed", "cancelled"].includes(status || "");
        // A local task subscriber may already know a newer state than the SSE
        // checkpoint. Never turn its terminal result back into a pending task.
        if (terminal(current.metadata?.taskStatus) && !terminal(change.after.metadata?.taskStatus)) return { before: current, after: current };
        if (terminal(current.metadata?.taskStatus)) return change;
        const metadata = { ...change.before!.metadata };
        for (const key of ["status", "taskStatus", "taskProgress", "taskStage"] as const) {
            if (!equal(metadata[key], change.after.metadata?.[key])) Object.assign(metadata, { [key]: current.metadata?.[key] });
        }
        return { ...change, before: { ...change.before!, metadata } };
    });
    const nodes = mergeItems(project.nodes, nodeChanges);
    const connections = mergeItems(project.connections, patch.connections);
    if (nodes === project.nodes && connections === project.connections) return project;
    return { ...project, nodes, connections, updatedAt: patch.updatedAt || project.updatedAt };
}

export function mergeAgentCanvasEditor(previous: CanvasProject, incoming: CanvasProject, nodes: CanvasNodeData[], connections: CanvasConnection[]) {
    const changes = <T extends { id: string }>(before: T[], after: T[]): Change<T>[] => {
        const byId = new Map(before.map((item) => [item.id, item]));
        const afterIds = new Set(after.map((item) => item.id));
        return [
            ...after.filter((item) => !equal(item, byId.get(item.id))).map((item) => ({ before: byId.get(item.id) ?? null, after: item })),
            ...before.filter((item) => !afterIds.has(item.id)).map((item) => ({ before: item, after: null })),
        ];
    };
    return applyAgentCanvasPatch({ ...previous, nodes, connections }, {
        canvasId: previous.id,
        updatedAt: incoming.updatedAt,
        nodes: changes(previous.nodes, incoming.nodes),
        connections: changes(previous.connections, incoming.connections),
    });
}
