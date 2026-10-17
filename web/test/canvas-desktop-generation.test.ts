import { expect, test } from "bun:test";
import { apiClient } from "../src/services/api/request";
import { createGenerationTaskSubscriptionService, waitForGenerationTask, type GenerationTask } from "../src/services/api/task-center";
import { generationTaskStatusLabel, generationTaskShowsProgress, canCancelGenerationTask } from "../src/lib/generation-task-display";
import { managedCanvasTaskRequest, inferCanvasTaskBinding, createCanvasGenerationContinuation } from "../src/services/canvas-generation-continuation";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
const uncertain = { id: "uncertain", status: "submission_uncertain", type: "text", prompt: "x", attempts: 1, createdAt: "now", updatedAt: "now" } as unknown as GenerationTask;
test("ordinary continuation binds an output to its actual batch or storyboard row", () => {
    const document = {
        nodes: [
            { id: "out", type: "image", metadata: {} },
            { id: "batch", type: "batch-table", metadata: { batchTable: { rows: [{ id: "r", outputNodeId: "out" }] } } },
        ],
    };
    expect(inferCanvasTaskBinding(document, "out", "image", "image")).toEqual({ targetNodeId: "batch", consumer: { kind: "batch", rowId: "r", outputNodeId: "out" } });
    const story = {
        nodes: [
            { id: "video", type: "video", metadata: {} },
            { id: "script", type: "script", metadata: { storyboard: { rows: [{ id: "shot", videoNodeId: "video" }] } } },
        ],
    };
    expect(inferCanvasTaskBinding(story, "video", "video", "video")).toEqual({ targetNodeId: "script", consumer: { kind: "storyboard", rowId: "shot", field: "videoNodeId", outputNodeId: "video" } });
    expect(inferCanvasTaskBinding(story, "script", "text", "storyboard")).toEqual({ targetNodeId: "script", consumer: { kind: "storyboard_document" } });
    expect(() => inferCanvasTaskBinding(document, "gone", "image", "image")).toThrow("canvas_generation_target_missing");
});
test("ordinary continuation commits new editor nodes before submitting one durable task", async () => {
    const steps: string[] = [];
    let sent: any;
    const session = new CanvasCommandSession(
        {
            snapshot: async () => ({
                protocolVersion: 1,
                canvasId: "canvas",
                documentEpoch: "epoch",
                revision: 1,
                eventSeq: 0,
                entityVersions: { "node:target": 1 },
                document: { nodes: [{ id: "target", type: "text", metadata: { content: "old" } }], connections: [] },
            }),
            events: async () => [],
            invoke: async (command, effect) => {
                steps.push("submit");
                sent = { command, effect };
                return {
                    taskId: "task",
                    receipt: { protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", clientId: "client", actorId: "owner", operationId: command.operationId, revision: 2, eventSeq: 1, changes: { entities: [] }, entityVersions: {} },
                };
            },
            recover: async () => {},
            execute: async () => {},
            receipt: async () => {},
        } as unknown as CommandTransport,
        "client",
    );
    await session.open("canvas");
    const continuation = createCanvasGenerationContinuation({
        session,
        commitEditor: async () => {
            steps.push("commit");
        },
        queryTask: async () => {
            steps.push("query");
            return { ...uncertain, status: "queued", id: "task" };
        },
        waitTask: async () => uncertain,
    });
    const { submitForNode } = continuation;
    const result = await submitForNode("target", { prompt: "x", input: { mode: "text", config: { channelId: "managed" }, metadata: { clientOperationId: "stable-operation" } } });
    expect(result.id).toBe("task");
    expect(steps).toEqual(["commit", "submit", "query"]);
    expect(sent.command.operationId).toBe("stable-operation");
    expect(sent.effect.request.targetNodeId).toBe("target");
    session.close();
});
test("managed canvas tasks retain creative and workflow inputs while omitting renderer credentials", () => {
    const input = {
        type: "canvas_image",
        prompt: "keep",
        operation: "edit",
        logicalModelId: "logical",
        input: {
            mode: "image",
            config: {
                channelId: "host-channel",
                model: "image-model",
                apiKey: "private",
                baseUrl: "https://private",
                headers: [{ key: "authorization", value: "private" }],
                capabilityConfig: { bad: true },
                size: "1024x1024",
                workflowId: "wf",
                workflowFields: { prompt: "creative" },
                runningHubUploadApiKey: "private",
            },
            referenceImages: [{ storageKey: "resource:owned" }],
            metadata: { nodeId: "node", providerOptions: { h3: { seconds: 6, apiKey: "private" } }, extension: "keep" },
            agentRequests: { tools: [] },
        },
    };
    const output = managedCanvasTaskRequest(input);
    expect(output).toMatchObject({
        mode: "image",
        prompt: "keep",
        operation: "edit",
        logicalModelId: "logical",
        input: {
            config: { channelId: "host-channel", model: "image-model", size: "1024x1024", workflowId: "wf", workflowFields: { prompt: "creative" } },
            referenceImages: [{ storageKey: "resource:owned" }],
            metadata: { extension: "keep", providerOptions: { h3: { seconds: 6 } } },
        },
    });
    const wire = JSON.stringify(output);
    expect(wire).not.toContain("private");
    expect(wire).not.toContain("agentRequests");
    expect(wire).not.toContain("capabilityConfig");
    expect(input.input.config.apiKey).toBe("private");
});
test("uncertain submission is labelled and offers neither false progress nor cancellation", () => {
    expect(generationTaskStatusLabel(uncertain)).toBe("提交结果待确认");
    expect(generationTaskShowsProgress({ ...uncertain, progress: 5 })).toBe(false);
    expect(canCancelGenerationTask(uncertain)).toBe(false);
});
test("uncertain upstream submission remains visible without entering ordinary task wait", async () => {
    let waiting = 0;
    const observed: GenerationTask[] = [];
    const service = createGenerationTaskSubscriptionService({
        queryTask: async () => uncertain,
        waitTask: async () => {
            waiting++;
            return uncertain;
        },
    });
    const dispose = service.subscribe(["uncertain"], (task) => observed.push(task));
    await new Promise((r) => setTimeout(r, 0));
    dispose();
    expect(observed.map((t) => t.id)).toEqual(["uncertain"]);
    expect(waiting).toBe(0);
});
test("uncertain submission ends polling and text waiting with an explicit recoverable reason", async () => {
    const previous = apiClient.request;
    const previousWindow = globalThis.window;
    const previousFetch = globalThis.fetch;
    let queries = 0;
    Object.assign(globalThis, {
        window: {
            dispatchEvent: () => {},
            setTimeout: () => {
                throw new Error("unexpected polling");
            },
        },
    });
    globalThis.fetch = (async () => {
        throw new Error("unexpected text stream");
    }) as typeof fetch;
    apiClient.request = (async () => {
        queries++;
        return { status: 200, data: { code: 0, data: uncertain, msg: "ok" } };
    }) as typeof apiClient.request;
    try {
        const error = await waitForGenerationTask("uncertain").catch((e) => e);
        expect(error).toMatchObject({ reason: "submission_uncertain", taskId: "uncertain" });
        expect(queries).toBe(1);
        const text = await waitForGenerationTask("uncertain", { initialTask: uncertain, useTextEvents: true }).catch((e) => e);
        expect(text).toMatchObject({ reason: "submission_uncertain", taskId: "uncertain" });
    } finally {
        apiClient.request = previous;
        Object.assign(globalThis, { window: previousWindow });
        globalThis.fetch = previousFetch;
    }
});
