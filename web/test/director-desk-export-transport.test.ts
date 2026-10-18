import { expect, test } from "bun:test";
import { createDirectorDeskExportTransport, ensureDirectorDeskCanvasSession } from "@/integrations/director-desk/canvas-export-transport";
import type { Operation } from "@/lib/canvas/command-contract.generated";
import { createStoryboardRow } from "@/lib/canvas/canvas-project-domain";

test("export transport submits durable previs task and synchronizes materialized document", async () => {
    const calls: unknown[] = [];
    const session = {
        state: { projected: { nodes: [] } },
        submitPrevis: async (...args: unknown[]) => {
            calls.push(args);
            return { taskId: "task-1" };
        },
        submit: async () => undefined,
        reconnect: async () => {
            session.state.projected.nodes = [{ id: "previs-video-task-1" }] as any;
        },
    };
    const transport = createDirectorDeskExportTransport({ session: () => session as any, queryTask: async (id) => ({ id, status: "succeeded" }), readMaterialization: async (taskId) => ({ taskId, state: "done", outcome: "applied", attempts: 0 }) });
    await expect(transport.submitPrevis({ canvasId: "canvas-1", sceneId: "scene-1", shotId: "shot-1", kind: "whitebox-video", options: { duration: 5, fps: 24 } }, "op-1")).resolves.toEqual({ taskId: "task-1" });
    expect(calls).toEqual([[{ sceneId: "scene-1", shotId: "shot-1", options: { duration: 5, fps: 24 } }, { operationId: "op-1" }]]);
    await transport.waitForMaterialization("task-1");
    expect((await transport.readCanvas("canvas-1")).nodes).toHaveLength(1);
});

test("export lazily opens the canvas command session when the page has not connected it", async () => {
    const calls: string[] = [];
    const session = { state: { projected: { nodes: [] } }, submit: async () => undefined, submitPrevis: async () => ({ taskId: "task-1" }), reconnect: async () => undefined };
    let currentScope: string | null = null;
    const result = await ensureDirectorDeskCanvasSession(
        { canvasId: "canvas-1", scope: "user-1" },
        {
            getScope: () => currentScope,
            getSession: () => undefined,
            connect: async (scope) => {
                calls.push(`connect:${scope}`);
                currentScope = scope;
            },
            open: async (canvasId) => {
                calls.push(`open:${canvasId}`);
                return session as any;
            },
        },
    );
    expect(result).toBe(session);
    expect(calls).toEqual(["connect:user-1", "open:canvas-1"]);
});

test("shot export uses dedicated structured storyboard and preserves unrelated nodes", async () => {
    const calls: Operation[][] = [];
    const session = {
        state: { projected: { nodes: [{ id: "other-board", type: "script" }] } },
        submit: async (ops: Operation[]) => {
            calls.push(ops);
        },
    };
    const transport = createDirectorDeskExportTransport({ session: () => session as any, queryTask: async (id) => ({ id, status: "queued" }), readMaterialization: async (taskId) => ({ taskId, state: "waiting", attempts: 0 }) });
    await transport.writeStoryboard("canvas-1", [createStoryboardRow(1, { id: "shot-1", durationSeconds: 5, videoMotionPrompt: "走进房间", previsSceneId: "scene-1" })]);
    expect(calls.flat()).toMatchObject([
        { type: "add_node", id: "director-storyboard-scene-1", nodeType: "script" },
        { type: "edit_storyboard", id: "director-storyboard-scene-1", action: "append", rowId: "shot-1", patch: { durationSeconds: 5, videoMotionPrompt: "走进房间" } },
    ]);
    expect(calls.flat().some((op) => op.id === "other-board")).toBe(false);
    expect(calls.flat()[1].patch).not.toHaveProperty("previsSceneId");
    expect(calls.flat()[1].patch).not.toHaveProperty("status");
});
