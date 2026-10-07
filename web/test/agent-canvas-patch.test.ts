import { expect, test } from "bun:test";
import { applyAgentCanvasPatch, mergeAgentCanvasEditor, type AgentCanvasPatch } from "@/lib/canvas/agent-canvas-patch";
import { isCanvasNodeGenerating } from "@/lib/canvas/canvas-node-task-state";
import type { CanvasProject } from "@/stores/canvas/use-canvas-store";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const node: CanvasNodeData = { id: "video-1", title: "满月动画", type: CanvasNodeType.Video, position: { x: 10, y: 20 }, width: 320, height: 180, metadata: { status: "loading", taskId: "task-1", taskStatus: "running", prompt: "原始提示词" } };
const project: CanvasProject = { id: "canvas", title: "画布", nodes: [node], connections: [], chatSessions: [], activeChatId: null, viewport: { x: 12, y: 24, k: 1 }, createdAt: "2026-09-13", updatedAt: "2026-09-13" };
const failed: CanvasNodeData = { ...node, metadata: { ...node.metadata, status: "error", taskStatus: "failed", errorDetails: "上游拒绝" } };
const patch: AgentCanvasPatch = { canvasId: project.id, updatedAt: "2026-09-13T12:00:00Z", nodes: [{ before: node, after: failed }], connections: [] };

test("failed Agent task patches only its node and unlocks generation", () => {
    expect(isCanvasNodeGenerating(node)).toBe(true);
    const result = applyAgentCanvasPatch(project, patch);
    expect(result.nodes[0]).toEqual(failed);
    expect(isCanvasNodeGenerating(result.nodes[0])).toBe(false);
    expect(result.viewport).toBe(project.viewport);
    expect(result.connections).toBe(project.connections);
    expect(project.nodes[0].metadata?.status).toBe("loading");
});

test("unrelated local edits and editor-only dragging survive completion", () => {
    const local = { ...node, position: { x: 800, y: 400 }, metadata: { ...node.metadata, prompt: "正在编辑的新提示词" } };
    const merged = mergeAgentCanvasEditor(project, applyAgentCanvasPatch(project, patch), [local], []);
    expect(merged.nodes[0].position).toEqual(local.position);
    expect(merged.nodes[0].metadata?.prompt).toBe("正在编辑的新提示词");
    expect(merged.nodes[0].metadata?.status).toBe("error");
});

test("replay is idempotent and cannot roll a completed node back to loading", () => {
    const result = applyAgentCanvasPatch(project, patch);
    expect(applyAgentCanvasPatch(result, patch)).toBe(result);
    const old: AgentCanvasPatch = { ...patch, nodes: [{ before: { ...node, metadata: { status: "idle" } }, after: node }] };
    expect(() => applyAgentCanvasPatch(result, old)).toThrow("冲突");
    expect(result.nodes[0].metadata?.status).toBe("error");
});

test("new nodes and reference connections are applied atomically", () => {
    const draft = { ...node, id: "video-2", metadata: { status: "idle" as const } };
    const edge = { id: "edge", fromNodeId: node.id, toNodeId: draft.id };
    const result = applyAgentCanvasPatch(project, { ...patch, nodes: [{ before: null, after: draft }], connections: [{ before: null, after: edge }] });
    expect(result.nodes[0]).toBe(node);
    expect(result.nodes[1]).toEqual(draft);
    expect(result.connections).toEqual([edge]);
});

test("deleted nodes and a newer task binding are not overwritten", () => {
    expect(() => applyAgentCanvasPatch({ ...project, nodes: [] }, patch)).toThrow("冲突");
    // A replacement task can have the same running status; its task identity is a fence.
    const replacement = { ...node, metadata: { ...node.metadata, taskId: "task-2" } };
    expect(() => applyAgentCanvasPatch({ ...project, nodes: [replacement] }, patch)).toThrow();
});

test("task state covers Agent, manual submissions and historical terminal tasks", () => {
    for (const status of ["queued", "running"]) expect(isCanvasNodeGenerating({ ...node, metadata: { status: "idle", taskId: "task", taskStatus: status } })).toBe(true);
    for (const status of ["succeeded", "failed", "cancelled"]) expect(isCanvasNodeGenerating({ ...node, metadata: { status: "loading", taskId: "old", taskStatus: status } })).toBe(false);
    expect(isCanvasNodeGenerating({ ...node, metadata: { status: "loading" } })).toBe(true);
    expect(isCanvasNodeGenerating({ ...node, metadata: { status: "success", taskId: "historical" } })).toBe(false);
    expect(isCanvasNodeGenerating({ ...node, metadata: { status: "idle" } }, node.id)).toBe(true);
});


test("task progress ahead of the server checkpoint still accepts terminal state", () => {
    const queued = { ...node, metadata: { ...node.metadata, taskStatus: "queued", taskProgress: 0 } };
    const running = { ...node, metadata: { ...node.metadata, taskProgress: 67 } };
    const result = applyAgentCanvasPatch({ ...project, nodes: [running] }, { ...patch, nodes: [{ before: queued, after: failed }] });
    expect(result.nodes[0].metadata?.taskStatus).toBe("failed");
    expect(isCanvasNodeGenerating(result.nodes[0])).toBe(false);
});

test("full Agent refresh removes unchanged nodes but preserves conflicting local edits", () => {
    const remaining = { ...node, id: "remaining", metadata: {} };
    const previous = { ...project, nodes: [node, remaining] };
    const incoming = { ...previous, nodes: [remaining] };
    expect(mergeAgentCanvasEditor(previous, incoming, previous.nodes, []).nodes).toEqual([remaining]);
    const local = { ...node, title: "Unsaved local title" };
    expect(() => mergeAgentCanvasEditor(previous, incoming, [local, remaining], [])).toThrow("冲突");
    expect(local.title).toBe("Unsaved local title");
});

// 分镜/批量表这类"以行 id 为键的列表"：服务端为控制体积只下发变化的那几行，
// 前端必须按 id 做三方合并，未提到的行保持本地值。
const storyboard = (rows: Array<Record<string, unknown>>): CanvasNodeData => ({ id: "sb-1", title: "分镜", type: CanvasNodeType.Script, position: { x: 0, y: 0 }, width: 920, height: 360, metadata: { status: "idle", storyboard: { rows, nextOffset: 0, hasMore: false } } });
const row = (id: string, shot: number, patch: Record<string, unknown>) => ({ id, shotNumber: shot, plotDescription: `镜${shot}`, imageGenerationPrompt: `旧提示词${shot}`, ...patch });
const scriptProject = (rows: Array<Record<string, unknown>>): CanvasProject => ({ ...project, nodes: [storyboard(rows)] });

test("sparse storyboard row deltas merge by row id", () => {
    const rows = [row("r1", 1, {}), row("r2", 2, {}), row("r3", 3, {})];
    const before = storyboard([{ id: "r2", imageGenerationPrompt: "旧提示词2" }]);
    const after = storyboard([{ id: "r2", imageGenerationPrompt: "新提示词2" }]);
    const result = applyAgentCanvasPatch(scriptProject(rows), { ...patch, nodes: [{ before, after }] });
    const updated = (result.nodes[0].metadata?.storyboard as { rows: Array<Record<string, unknown>> }).rows;
    expect(updated.map((item) => item.imageGenerationPrompt)).toEqual(["旧提示词1", "新提示词2", "旧提示词3"]);
    expect(updated.map((item) => item.plotDescription)).toEqual(["镜1", "镜2", "镜3"]);
    expect(updated.map((item) => item.id)).toEqual(["r1", "r2", "r3"]);
});

test("local edits on untouched storyboard fields survive a sparse delta", () => {
    const rows = [row("r1", 1, {}), { ...row("r2", 2, {}), dialogue: "本地刚写的台词" }];
    const before = storyboard([{ id: "r2", imageGenerationPrompt: "旧提示词2" }]);
    const after = storyboard([{ id: "r2", imageGenerationPrompt: "新提示词2" }]);
    const result = applyAgentCanvasPatch(scriptProject(rows), { ...patch, nodes: [{ before, after }] });
    const updated = (result.nodes[0].metadata?.storyboard as { rows: Array<Record<string, unknown>> }).rows;
    const second = updated.find((item) => item.id === "r2") as Record<string, unknown>;
    expect(second.imageGenerationPrompt).toBe("新提示词2");
    expect(second.dialogue).toBe("本地刚写的台词");
});

test("sparse deltas append created rows and drop removed rows", () => {
    const rows = [row("r1", 1, {}), row("r2", 2, {})];
    // 协议：删除行下发完整旧行（否则无法与"本地改过"区分），新增行下发完整新行。
    const before = storyboard([row("r1", 1, {})]);
    const after = storyboard([{ id: "r3", shotNumber: 3, plotDescription: "镜3" }]);
    const result = applyAgentCanvasPatch(scriptProject(rows), { ...patch, nodes: [{ before, after }] });
    const updated = (result.nodes[0].metadata?.storyboard as { rows: Array<Record<string, unknown>> }).rows;
    expect(updated.map((item) => item.id)).toEqual(["r2", "r3"]);
    expect(updated[1].plotDescription).toBe("镜3");
});

test("a row the agent removed but the operator edited is a conflict, never a silent delete", () => {
    const rows = [row("r1", 1, {}), { ...row("r2", 2, {}), dialogue: "本地新台词" }];
    const before = storyboard([{ id: "r2", plotDescription: "镜2" }]);
    const after = storyboard([]);
    expect(() => applyAgentCanvasPatch(scriptProject(rows), { ...patch, nodes: [{ before, after }] })).toThrow("冲突");
});

test("server node and connection order applies without losing local content or additions", () => {
    const second = { ...node, id: "second", metadata: {} };
    const third = { ...second, id: "local" };
    const edges = [{ id: "e1", fromNodeId: node.id, toNodeId: second.id }, { id: "e2", fromNodeId: second.id, toNodeId: node.id }];
    const local = { ...node, title: "本地标题" };
    const result = applyAgentCanvasPatch({ ...project, nodes: [local, second, third], connections: edges }, {
        ...patch, nodes: [], connections: [], previousNodeOrder: [node.id, second.id], nodeOrder: [second.id, node.id],
        previousConnectionOrder: ["e1", "e2"], connectionOrder: ["e2", "e1"],
    });
    expect(result.nodes.map((item) => item.id)).toEqual([second.id, node.id, third.id]);
    expect(result.nodes[1]).toBe(local);
    expect(result.connections.map((item) => item.id)).toEqual(["e2", "e1"]);
});

test("full refresh carries order changes and rejects a competing local reorder atomically", () => {
    const a = { ...node, id: "a", metadata: {} }, b = { ...a, id: "b" }, c = { ...a, id: "c" };
    const before = { ...project, nodes: [a, b, c] }, after = { ...project, nodes: [c, a, b] };
    expect(mergeAgentCanvasEditor(before, after, [a, { ...b, title: "local" }, c], []).nodes.map((item) => item.id)).toEqual(["c", "a", "b"]);
    expect(() => mergeAgentCanvasEditor(before, after, [b, a, c], [])).toThrow("冲突");
    expect(before.nodes).toEqual([a, b, c]);
});

test("full row reorder merges untouched local fields and detects competing row order", () => {
    const rows = [row("r1", 1, {}), row("r2", 2, {}), row("r3", 3, {})];
    const local = [rows[0], { ...rows[1], dialogue: "本地台词" }, rows[2]];
    const reorder = { ...patch, nodes: [{ before: storyboard(rows), after: storyboard([rows[2], rows[0], { ...rows[1], imageGenerationPrompt: "新提示词" }]) }] };
    const result = applyAgentCanvasPatch(scriptProject(local), reorder);
    const updated = (result.nodes[0].metadata?.storyboard as { rows: Array<Record<string, unknown>> }).rows;
    expect(updated.map((item) => item.id)).toEqual(["r3", "r1", "r2"]);
    expect(updated[2]).toMatchObject({ dialogue: "本地台词", imageGenerationPrompt: "新提示词" });
    expect(() => applyAgentCanvasPatch(scriptProject([local[1], local[0], local[2]]), reorder)).toThrow("冲突");
});

test("remote deletion removes graph items; duplicate patches and replays preserve local edits", () => {
    const copy = { ...node, id: "copy", title: "副本", metadata: {} };
    const edge = { id: "edge", fromNodeId: node.id, toNodeId: copy.id };
    const changes = { ...patch, nodes: [{ before: node, after: null }, { before: null, after: copy }], connections: [{ before: edge, after: null }] };
    const result = applyAgentCanvasPatch({ ...project, connections: [edge] }, changes);
    expect(result.nodes).toEqual([copy]);
    expect(result.connections).toEqual([]);
    expect(applyAgentCanvasPatch(result, changes)).toBe(result);
    expect(() => applyAgentCanvasPatch({ ...project, nodes: [{ ...node, title: "local" }], connections: [edge] }, changes)).toThrow("冲突");
});

test("deleting a node conflicts with new local references instead of creating a dangling graph", () => {
    const kept = { ...node, id: "kept", metadata: {} };
    const deletion = { ...patch, nodes: [{ before: node, after: null }], connections: [] };
    const localEdge = { id: "local-edge", fromNodeId: node.id, toNodeId: kept.id };
    expect(() => applyAgentCanvasPatch({ ...project, nodes: [node, kept], connections: [localEdge] }, deletion)).toThrow("冲突");
    expect(() => applyAgentCanvasPatch({ ...project, nodes: [node, { ...kept, parentId: node.id }] }, deletion)).toThrow("冲突");
});

test("order patches cannot silently drop added nodes or accept ambiguous baselines", () => {
    const created = { ...node, id: "new", metadata: {} };
    expect(() => applyAgentCanvasPatch(project, { ...patch, nodes: [{ before: null, after: created }], previousNodeOrder: [node.id], nodeOrder: [node.id] })).toThrow();
    expect(() => applyAgentCanvasPatch(project, { ...patch, nodes: [], previousNodeOrder: [node.id], nodeOrder: [node.id, node.id] })).toThrow();
});

test("server additions preserve a nonconflicting local reorder", () => {
    const second = { ...node, id: "second", metadata: {} }, added = { ...second, id: "added" };
    const result = mergeAgentCanvasEditor({ ...project, nodes: [node, second] }, { ...project, nodes: [node, second, added] }, [second, node], []);
    expect(result.nodes.map((item) => item.id)).toEqual(["second", node.id, "added"]);
});

test("explicit node order restores deleted nodes at the middle or head and replay keeps that order", () => {
    const a = { ...node, id: "a", metadata: {} }, b = { ...a, id: "b" }, c = { ...a, id: "c" };
    const local = { ...a, title: "本地改名" };
    for (const order of [["a", "b", "c"], ["b", "a", "c"]]) {
        const restoration: AgentCanvasPatch = { ...patch, nodes: [{ before: null, after: b }], connections: [], previousNodeOrder: ["a", "c"], nodeOrder: order };
        const result = applyAgentCanvasPatch({ ...project, nodes: [local, c] }, restoration);
        expect(result.nodes.map((item) => item.id)).toEqual(order);
        expect(result.nodes.find((item) => item.id === "a")).toBe(local);
        expect(applyAgentCanvasPatch(result, restoration)).toBe(result);
    }
});

test("restoring a node into retained order rejects a concurrent local reorder atomically", () => {
    const a = { ...node, id: "a", metadata: {} }, b = { ...a, id: "b" }, c = { ...a, id: "c" };
    const current = { ...project, nodes: [c, a] };
    const restoration: AgentCanvasPatch = { ...patch, nodes: [{ before: null, after: b }], connections: [], previousNodeOrder: ["a", "c"], nodeOrder: ["a", "b", "c"] };
    expect(() => applyAgentCanvasPatch(current, restoration)).toThrow("顺序");
    expect(current.nodes).toEqual([c, a]);
});

test("full rows restore a deleted middle row while keeping nonconflicting local fields", () => {
    const a = row("a", 1, {}), b = row("b", 2, {}), c = row("c", 3, {});
    const localA = { ...a, dialogue: "本地新台词" };
    const restoration = { ...patch, nodes: [{ before: storyboard([a, c]), after: storyboard([a, b, { ...c, imageGenerationPrompt: "远端新提示词" }]) }] };
    const result = applyAgentCanvasPatch(scriptProject([localA, c]), restoration);
    const updated = (result.nodes[0].metadata?.storyboard as { rows: Array<Record<string, unknown>> }).rows;
    expect(updated.map((item) => item.id)).toEqual(["a", "b", "c"]);
    expect(updated[0].dialogue).toBe("本地新台词");
    expect(updated[2].imageGenerationPrompt).toBe("远端新提示词");
    expect(applyAgentCanvasPatch(result, restoration)).toEqual(result);
    expect(() => applyAgentCanvasPatch(scriptProject([c, localA]), restoration)).toThrow("顺序");
});

test("a full row tail append still preserves a nonconflicting local reorder", () => {
    const a = row("a", 1, {}), b = row("b", 2, {}), c = row("c", 3, {});
    const result = applyAgentCanvasPatch(scriptProject([c, { ...a, dialogue: "本地台词" }]), { ...patch, nodes: [{ before: storyboard([a, c]), after: storyboard([a, c, b]) }] });
    const updated = (result.nodes[0].metadata?.storyboard as { rows: Array<Record<string, unknown>> }).rows;
    expect(updated.map((item) => item.id)).toEqual(["c", "a", "b"]);
    expect(updated[1].dialogue).toBe("本地台词");
});
