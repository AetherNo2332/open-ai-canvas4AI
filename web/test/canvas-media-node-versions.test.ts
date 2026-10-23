import { expect, test } from "bun:test";
import { CanvasCommandSession, type CommandTransport } from "../src/services/canvas-command-session";
import { isCanvasNodeGenerating } from "../src/lib/canvas/canvas-node-task-state";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";

test("new-version command preview keeps the current playable output and task provenance", async () => {
    let sent: any;
    let finish!: (value: any) => void;
    const current = { id: "media", type: "image", metadata: { content: "/old.png", storageKey: "resource:old", taskId: "old-task", taskStatus: "succeeded", status: "success" } };
    const session = new CanvasCommandSession(
        {
            snapshot: async () => ({ protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 1, eventSeq: 0, entityVersions: { "node:media": 1 }, document: { nodes: [current], connections: [] } }),
            events: async () => [],
            invoke: async (command, effect) => {
                sent = effect;
                return new Promise((resolve) => {
                    finish = () =>
                        resolve({
                            taskId: "new-task",
                            versionId: "new-version",
                            receipt: { protocolVersion: 1, canvasId: "canvas", documentEpoch: "epoch", revision: 2, eventSeq: 1, actorId: "owner", clientId: "client", operationId: command.operationId, changes: { entities: [] }, entityVersions: {} },
                        });
                });
            },
            execute: async () => {},
            receipt: async () => {},
            recover: async () => {},
        } as unknown as CommandTransport,
        "client",
    );
    await session.open("canvas");
    const submission = session.submitTask({ intent: "new_version", targetNodeId: "media", task: { mode: "image", prompt: "next", input: {} } });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect((session.state.projected.nodes as any[])[0].metadata).toEqual(current.metadata);
    expect(sent.request.intent).toBe("new_version");
    finish({});
    await submission;
    session.close();
});

test("a retained successful current output still reports its pending version as busy", () => {
    const node = {
        id: "media",
        type: CanvasNodeType.Video,
        metadata: { status: "success", taskStatus: "succeeded", taskId: "old-task", mediaVersion: { epoch: "epoch", generation: 1, selectionRevision: 1, versionCount: 2, currentVersionId: "old", pendingVersionId: "new", activeTaskId: "new-task" } },
    } as CanvasNodeData;
    expect(isCanvasNodeGenerating(node)).toBe(true);
});
