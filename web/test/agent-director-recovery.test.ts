import { describe, expect, test } from "bun:test";

import { findPendingCanvasDirectorPreview } from "@/lib/canvas/agent-director-recovery";
import type { AgentConversation, AgentConversationPage, AgentRunReceipt, AgentRunSnapshot } from "@/sdk/agent";

const conversation = (overrides: Partial<AgentConversation>): AgentConversation =>
    ({
        id: "conversation-1",
        canvasId: "canvas-1",
        title: "预演",
        latestRunId: "run-1",
        status: "running",
        scope: { type: "canvas", id: "canvas-1" },
        agentDefinitionId: "canvas.default",
        resumable: true,
        archived: false,
        revision: 2,
        createdAt: "2026-11-23T00:00:00Z",
        updatedAt: "2026-11-23T00:00:00Z",
        ...overrides,
    }) as AgentConversation;

const run = (overrides: Partial<AgentRunSnapshot>): AgentRunReceipt<AgentRunSnapshot> => ({
    run: {
        id: "run-1",
        canvasId: "canvas-1",
        status: "running",
        permissionMode: "read",
        createdAt: "2026-11-23T00:00:00Z",
        updatedAt: "2026-11-23T00:00:00Z",
        ...overrides,
    } as AgentRunSnapshot,
});

describe("cross-page director preview recovery", () => {
    test("pages the directory and returns only the requested canvas's live wait", async () => {
        const calls: unknown[] = [];
        const runs: string[] = [];
        const deadline = new Date(Date.now() + 60_000).toISOString();
        const pages = [
            {
                items: [
                    conversation({ id: "done", latestRunId: "run-done", status: "completed" }),
                    conversation({ id: "other", latestRunId: "run-other", canvasId: "canvas-2", scope: { type: "canvas", id: "canvas-2" }, waitKind: "director_preview" }),
                ],
                nextCursor: "cursor-1",
            },
            {
                items: [
                    conversation({ id: "target", latestRunId: "run-target", waitKind: "director_preview" }),
                    conversation({ id: "archived", latestRunId: "run-archived", archived: true }),
                ],
            },
        ];
        const client = {
            async listConversations(page: { before?: string; limit?: number }) {
                calls.push(page);
                return pages.shift() as AgentConversationPage<AgentConversation>;
            },
            async getRun(id: string) {
                runs.push(id);
                return run({
                    id,
                    waitKind: "director_preview",
                    waitId: "dpv_1",
                    pendingDirectorPreview: {
                        runId: id,
                        conversationId: "target",
                        canvasId: "canvas-1",
                        nodeId: "previs-scene-1",
                        sceneId: "scene-1",
                        shotId: "shot-1",
                        requestId: "dpv_1",
                        duration: 3,
                        fps: 24,
                        deadline,
                    },
                });
            },
        };

        expect(await findPendingCanvasDirectorPreview(client, "canvas-1")).toEqual({
            runId: "run-target",
            requestId: "dpv_1",
            canvasId: "canvas-1",
            nodeId: "previs-scene-1",
            sceneId: "scene-1",
            shotId: "shot-1",
            deadline,
        });
        expect(calls).toEqual([{ before: undefined, limit: 100 }, { before: "cursor-1", limit: 100 }]);
        expect(runs).toEqual(["run-target"]);
    });

    test("ignores a projection with a different wait id or an expired deadline", async () => {
        const expired = new Date(Date.now() - 1).toISOString();
        let current = 0;
        const client = {
            async listConversations() {
                return {
                    items: [
                        conversation({ id: "first", latestRunId: "run-first" }),
                        conversation({ id: "second", latestRunId: "run-second" }),
                        conversation({ id: "third", latestRunId: "run-third" }),
                    ],
                };
            },
            async getRun() {
                current += 1;
                const waitId = current === 1 ? "another-request" : current === 2 ? "dpv_1" : "dpv_3";
                return run({
                    waitKind: "director_preview",
                    waitId,
                    pendingDirectorPreview: {
                        runId: `run-${["first", "second", "third"][current - 1]}`,
                        conversationId: "conversation",
                        canvasId: "canvas-1",
                        sceneId: "scene-1",
                        requestId: waitId,
                        duration: 3,
                        fps: 24,
                        deadline: current === 1 ? new Date(Date.now() + 60_000).toISOString() : expired,
                    },
                });
            },
        };

        expect(await findPendingCanvasDirectorPreview(client, "canvas-1")).toBeNull();
    });
});
