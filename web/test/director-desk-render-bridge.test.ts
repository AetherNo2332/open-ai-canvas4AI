import { expect, test } from "bun:test";
import { createPrevisScene } from "@/lib/canvas/previs/previs-scene";
import { createDirectorRenderBridge } from "@/integrations/director-desk/render-bridge";

test("render bridge deduplicates identical previews and forwards cancellation", async () => {
    const scene = createPrevisScene();
    let submissions = 0;
    let signal: AbortSignal | undefined;
    let resolveResult: ((result: { taskId: string; status: "queued" }) => void) | undefined;
    const bridge = createDirectorRenderBridge({
        submit: async (_input, options) => {
            submissions += 1;
            signal = options.signal;
            return await new Promise((resolve) => { resolveResult = resolve; });
        },
    });
    const first = bridge.preview(scene, { shotId: scene.activeShotId, time: 0 });
    const second = bridge.preview(scene, { shotId: scene.activeShotId, time: 0 });
    expect(submissions).toBe(1);
    bridge.cancel(scene.id, scene.activeShotId, 0);
    expect(signal?.aborted).toBe(true);
    resolveResult?.({ taskId: "task-1", status: "queued" });
    expect(await first).toEqual(await second);
});

test("canvas exports forward kind and render options through the preview task bridge", async () => {
    const scene = createPrevisScene();
    const calls: unknown[] = [];
    const bridge = createDirectorRenderBridge({ submit: async (input, options) => {
        calls.push({ input, kind: options.kind });
        return { taskId: "export-1", status: "queued" };
    } });
    await bridge.exportToCanvas(scene, "composition-frame", { shotId: scene.activeShotId });
    await bridge.exportToCanvas(scene, "whitebox-video", { shotId: scene.activeShotId });
    expect(calls).toHaveLength(2);
    expect(calls[0]).toMatchObject({ input: { sourceSceneId: scene.id, shotId: scene.activeShotId }, kind: "composition-frame" });
    expect(calls[1]).toMatchObject({ kind: "whitebox-video" });
});

