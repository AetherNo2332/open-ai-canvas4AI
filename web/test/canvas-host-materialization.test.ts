import { expect, test } from "bun:test";
import { waitCanvasHostMaterialization } from "../src/services/canvas-host-materialization";
import { canvasPersistentEditorDraft } from "../src/lib/canvas/canvas-editor-transient";
test("host completion is synchronized before success with no browser output mutation", async () => {
    const sequence: string[] = [];
    let polls = 0;
    const result = await waitCanvasHostMaterialization("task", {
        read: async () => ({ taskId: "task", state: ++polls === 1 ? "pending" : "done", outcome: polls === 1 ? undefined : "text_edited", attempts: 0 }),
        delay: async () => {
            sequence.push("wait");
        },
        synchronize: async () => {
            sequence.push("events");
        },
    });
    expect(sequence).toEqual(["wait", "events"]);
    expect(result.outcome).toBe("text_edited");
});
test("materialization errors and cancellation never become a browser fallback", async () => {
    let sync = 0;
    await expect(
        waitCanvasHostMaterialization("task", {
            read: async () => ({ taskId: "task", state: "pending", message: "写回待重试", attempts: 1 }),
            synchronize: async () => {
                sync++;
            },
        }),
    ).rejects.toThrow("写回待重试");
    const controller = new AbortController();
    controller.abort();
    await expect(
        waitCanvasHostMaterialization("task", {
            signal: controller.signal,
            read: async () => {
                throw new Error("should not read");
            },
            synchronize: async () => {
                sync++;
            },
        }),
    ).rejects.toThrow();
    expect(sync).toBe(0);
});
test("task progress remains display state while creative edits and canonical task identity survive", () => {
    const before = { id: "canvas", nodes: [{ id: "text", metadata: { content: "old", taskId: "host-task", status: "loading", taskProgress: 0 } }] };
    const draft = {
        id: "canvas",
        nodes: [
            { id: "text", metadata: { content: "typed", taskId: "wrong", status: "success", taskProgress: 100 } },
            { id: "child", metadata: { prompt: "creative prompt", status: "loading", taskId: "forged" } },
        ],
    };
    const result = canvasPersistentEditorDraft(before, draft) as any;
    expect(result.nodes[0].metadata).toEqual({ content: "typed", taskId: "host-task", status: "loading", taskProgress: 0 });
    expect(result.nodes[1].metadata).toEqual({ prompt: "creative prompt" });
    expect(draft.nodes[1].metadata.taskId).toBe("forged");
});
