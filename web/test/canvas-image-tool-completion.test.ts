import { expect, test } from "bun:test";
import { runBackendCanvasGenerationTask } from "../src/lib/canvas/canvas-project-generation";
import { createModelChannel, defaultConfig, encodeChannelModel } from "../src/stores/use-config-store";
import type { CreateTaskInput, GenerationTask } from "../src/services/api/task-center";
import type { GenerationTaskDependencies } from "../src/services/api/generation-task";

const channel = createModelChannel({ id: "channel", name: "Test", baseUrl: "https://example.test", apiKey: "test-key", interfaceType: "openai-image", models: ["image-model"] });
const config = { ...defaultConfig, channels: [channel], model: encodeChannelModel(channel.id, "image-model") };
function dependencies(mode: "image" | "text", consumeCanvas: () => Promise<any>) {
    let input: CreateTaskInput | undefined;
    const task: GenerationTask = {
        id: "task",
        type: `canvas_${mode}`,
        status: "succeeded",
        prompt: "Edit",
        attempts: 0,
        createdAt: "2026-10-25",
        updatedAt: "2026-10-25",
        resultJson: JSON.stringify(mode === "image" ? { mode, images: [{ dataUrl: "/api/resources/result/file", storageKey: "resource:result" }] } : { mode, text: "Detected text" }),
    };
    return {
        read: () => input,
        runtime: {
            createId: () => "operation",
            createTask: async (value) => {
                input = value;
                return task;
            },
            waitTask: async (_id, options) => {
                options?.onTaskUpdate?.(task);
                return task;
            },
            consumeCanvas,
        } satisfies GenerationTaskDependencies & { consumeCanvas: () => Promise<any> },
    };
}
test("image tools wait for host materialization and mark results that must not be uploaded again", async () => {
    const phases: string[] = [];
    const deps = dependencies("image", async () => {
        phases.push("materialized");
        return { taskId: "task", state: "done", attempts: 0 };
    });
    const result = await runBackendCanvasGenerationTask({ projectId: "canvas", nodeId: "child", mode: "image", prompt: "Edit", config, metadata: { sourceNodeId: "source", edit: "annotation" } }, deps.runtime);
    expect(phases).toEqual(["materialized"]);
    expect((result as any).canvasMaterialized).toBe(true);
    expect(result.images?.[0].storageKey).toBe("resource:result");
});
test("image text detection is read-only and does not require a synthetic canvas node", async () => {
    const deps = dependencies("text", async () => {
        throw new Error("read-only text detection must not materialize a canvas node");
    });
    const result = await runBackendCanvasGenerationTask({ projectId: "canvas", nodeId: "source-text-detect", mode: "text", prompt: "Read text", config, metadata: { sourceNodeId: "source", edit: "text-detection" } }, deps.runtime);
    expect(deps.read()?.projectId).toBeUndefined();
    expect(result.text).toBe("Detected text");
});

test.each(["emotion", "layer-decomposition"])("%s keeps intermediate provider outputs available for final composition", async (edit) => {
    const deps = dependencies("image", async () => {
        throw new Error("intermediate outputs must not replace final canvas media");
    });
    let bound = false;
    const result = await runBackendCanvasGenerationTask(
        {
            projectId: "canvas",
            nodeId: "child",
            mode: "image",
            prompt: "Edit",
            config,
            metadata: { sourceNodeId: "source", edit },
            onTaskCreated: () => {
                bound = true;
            },
        },
        deps.runtime,
    );
    expect(deps.read()?.projectId).toBeUndefined();
    expect(result.images?.[0].storageKey).toBe("resource:result");
    expect(result.canvasMaterialized).toBeUndefined();
    expect(bound).toBe(false);
});
