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
