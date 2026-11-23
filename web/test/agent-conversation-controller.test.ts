import { describe, expect, test } from "bun:test";
import type { AgentEvent, AgentRunSnapshot } from "@/sdk/agent";
import type { createAgentClient } from "@/sdk/agent/client";
import { AgentConversationController } from "@/services/agent/conversation-controller";
import type { AgentConversationStore } from "@/services/agent/conversation-storage";
import { migrateLegacyCanvasConversationDocument, migrateLegacyCanvasPendingSubmission } from "@/services/agent/conversation-storage";
import type { AgentConversationDocument, AgentConversationIdentity, AgentPendingSubmission } from "@/services/agent/conversation-types";

type Client = ReturnType<typeof createAgentClient>;

const identity: AgentConversationIdentity = {
    userScope: "user-a",
    scope: { type: "platform", id: "self" },
    agentDefinitionId: "platform.default",
};

function memoryStore(seed?: AgentConversationDocument) {
    const scopeKey = `${identity.userScope}:${identity.scope.type}:${identity.scope.id}:${identity.agentDefinitionId}`;
    const documents = new Map<string, AgentConversationDocument>();
    const drafts = new Map<string, string>();
    const pending = new Map<string, AgentPendingSubmission>();
    if (seed) documents.set(scopeKey, structuredClone(seed));
    const conversationKey = (conversationId: string) => `${scopeKey}:${conversationId}`;
    const store: AgentConversationStore = {
        async loadDocument() {
            return structuredClone(documents.get(scopeKey) || { version: 2, activeId: null, conversations: [] });
        },
        async saveDocument(_identity, document) {
            documents.set(scopeKey, structuredClone(document));
        },
        async loadDraft(_identity, conversationId) {
            return drafts.get(conversationKey(conversationId)) || "";
        },
        async saveDraft(_identity, conversationId, draft) {
            drafts.set(conversationKey(conversationId), draft);
        },
        async clearDraft(_identity, conversationId) {
            drafts.delete(conversationKey(conversationId));
        },
        async loadPending(_identity, conversationId) {
            return structuredClone(pending.get(conversationKey(conversationId)) || null);
        },
        async savePending(_identity, conversationId, value) {
            pending.set(conversationKey(conversationId), structuredClone(value));
        },
        async clearPending(_identity, conversationId) {
            pending.delete(conversationKey(conversationId));
        },
    };
    return { store, documents, drafts, pending, scopeKey, conversationKey };
}

function run(patch: Partial<AgentRunSnapshot> = {}): AgentRunSnapshot {
    return {
        id: "run-1",
        conversationId: "conversation-1",
        canvasId: "",
        status: "completed",
        permissionMode: "read_only",
        createdAt: "2026-11-23T00:00:00Z",
        updatedAt: "2026-11-23T00:00:01Z",
        ...patch,
    };
}

function event(seq: number, patch: Partial<AgentEvent> = {}): AgentEvent {
    return {
        eventId: `event-${seq}`,
        runId: "run-1",
        seq,
        type: "assistant_message",
        payload: { text: `回复 ${seq}` },
        createdAt: "2026-11-23T00:00:01Z",
        ...patch,
    };
}

function emptyPage<T>() {
    return { items: [] as T[] };
}

function fakeClient(overrides: Record<string, unknown> = {}): Client {
    const base = {
        async listConversations() {
            return emptyPage();
        },
        async listConversationRuns() {
            return emptyPage<AgentRunSnapshot>();
        },
        async getRun(id: string, _signal?: AbortSignal, query?: unknown) {
            return { run: run({ id, status: "running" }) };
        },
        async startRun() {
            return { run: run() };
        },
        async continueRun() {
            return { run: run() };
        },
        async sendMessage() {
            return { conversationId: "conversation-1", messageId: "message-1", delivery: "queued" as const };
        },
        async getConversationQueue() {
            return { conversationId: "conversation-1", latestRunId: "run-1", revision: 1, paused: false, items: [] };
        },
        async readEvents() {},
        async cancelRun() {
            return { accepted: true };
        },
        async decideApproval() {
            return { accepted: true };
        },
        async changePermission(_id: string, input: { permissionMode: AgentRunSnapshot["permissionMode"] }) {
            return { permissionMode: input.permissionMode, changed: true };
        },
        async archiveConversation() {
            return { archived: true };
        },
        async updateQueuedMessage() {
            return { conversationId: "conversation-1", messageId: "message-1", delivery: "queued" as const };
        },
        async cancelQueuedMessage() {
            return { conversationId: "conversation-1", messageId: "message-1", delivery: "cancelled" as const };
        },
        async updateConversationQueue() {
            return { conversationId: "conversation-1", latestRunId: "run-1", revision: 2, paused: true, items: [] };
        },
        ...overrides,
    };
    return base as unknown as Client;
}

describe("AgentConversationController", () => {
    test("changes a running run permission through the SDK and persists the confirmed mode", async () => {
        const seed: AgentConversationDocument = {
            version: 2,
            activeId: "local-permission",
            conversations: [
                {
                    id: "local-permission",
                    serverConversationId: "conversation-1",
                    title: "权限测试",
                    messages: [],
                    run: run({ id: "run-permission", conversationId: "conversation-1", status: "running", permissionMode: "read_only" }),
                    permissionMode: "read_only",
                    createdAt: "2026-11-23T00:00:00Z",
                    updatedAt: "2026-11-23T00:00:01Z",
                },
            ],
        };
        const memory = memoryStore(seed);
        const calls: Array<{ id: string; mode: string }> = [];
        const client = fakeClient({
            async listConversationRuns() {
                return { items: [run({ id: "run-permission", conversationId: "conversation-1", status: "running", permissionMode: "read_only" })] };
            },
            async getRun() {
                return { run: run({ id: "run-permission", conversationId: "conversation-1", status: "running", permissionMode: "read_only" }) };
            },
            async changePermission(id: string, input: { permissionMode: AgentRunSnapshot["permissionMode"] }) {
                calls.push({ id, mode: input.permissionMode });
                return { permissionMode: input.permissionMode, changed: true };
            },
        });
        const controller = new AgentConversationController({ identity, client, store: memory.store });
        await controller.hydrate();
        expect(await controller.changePermission("auto")).toBe(true);
        expect(calls).toEqual([{ id: "run-permission", mode: "auto" }]);
        expect(controller.getSnapshot().run?.permissionMode).toBe("auto");
        controller.dispose();
    });

    test("persists the original request before transport and replays the same key after response loss", async () => {
        const memory = memoryStore();
        let calls = 0;
        let storedBeforeTransport = false;
        const client = fakeClient({
            async startRun(request: { prompt: string; idempotencyKey: string }) {
                calls += 1;
                const activeId = [...memory.documents.values()][0]?.activeId || "";
                const document = activeId ? memory.documents.get(memory.scopeKey) : undefined;
                storedBeforeTransport =
                    document?.conversations.some((item) => item.id === activeId && item.messages.some((message) => message.text === request.prompt)) === true &&
                    memory.pending.get(memory.conversationKey(activeId))?.request?.idempotencyKey === request.idempotencyKey;
                if (calls === 1) throw new Error("network response lost");
                return { run: run({ id: "run-retried", conversationId: "conversation-retried" }) };
            },
        });
        const controller = new AgentConversationController({ identity, client, store: memory.store, createId: () => "local-conversation" });
        await controller.hydrate();
        const request = {
            scope: identity.scope,
            canvasId: "" as const,
            prompt: "介绍当前能力",
            permissionMode: "read_only" as const,
            idempotencyKey: "stable-platform-key",
        };

        expect(await controller.submit({ request })).toBe(false);
        expect(storedBeforeTransport).toBe(true);
        expect(controller.getSnapshot().pending?.request?.idempotencyKey).toBe("stable-platform-key");
        expect(memory.pending.get(memory.conversationKey("local-conversation"))?.key).toBe("stable-platform-key");

        expect(await controller.retryPending()).toBe(true);
        expect(calls).toBe(2);
        expect(memory.pending.size).toBe(0);
        expect(controller.getSnapshot().run?.id).toBe("run-retried");
        expect(controller.getSnapshot().messages.filter((message) => message.role === "user")).toHaveLength(1);
        controller.dispose();
    });

    test("starts each Run event stream at its own cursor and applies duplicate seq once", async () => {
        const seed: AgentConversationDocument = {
            version: 2,
            activeId: "local-1",
            conversations: [
                {
                    id: "local-1",
                    serverConversationId: "conversation-1",
                    lastEventSeq: 99,
                    eventCursors: { "old-run": 99 },
                    title: "旧会话",
                    messages: [],
                    run: null,
                    permissionMode: "read_only",
                    createdAt: "2026-11-23T00:00:00Z",
                    updatedAt: "2026-11-23T00:00:00Z",
                },
            ],
        };
        const memory = memoryStore(seed);
        let after = -1;
        const client = fakeClient({
            async listConversationRuns() {
                return { items: [run({ status: "running", eventCount: 0 })] };
            },
            async getRun() {
                return { run: run({ status: "running", eventCount: 0 }) };
            },
            async readEvents(_id: string, onEvent: (item: AgentEvent) => void, options: { after?: number; onSnapshot?: (snapshot: AgentRunSnapshot) => void }) {
                after = options.after || 0;
                const duplicate = event(1);
                onEvent(duplicate);
                onEvent(duplicate);
                options.onSnapshot?.(run({ status: "completed", latestSeq: 1 }));
            },
        });
        const controller = new AgentConversationController({ identity, client, store: memory.store });
        await controller.hydrate();
        await new Promise((resolve) => setTimeout(resolve, 0));

        expect(after).toBe(0);
        expect(controller.getSnapshot().messages.filter((message) => message.role === "assistant")).toHaveLength(1);
        expect(controller.getSnapshot().conversations[0].eventCursors?.["run-1"]).toBe(1);
        controller.dispose();
    });

    test("persists model, reasoning, skills, send mode and context usage across hydration", async () => {
        const seed: AgentConversationDocument = {
            version: 2,
            activeId: "local-1",
            conversations: [
                {
                    id: "local-1",
                    serverConversationId: "conversation-1",
                    title: "状态",
                    messages: [],
                    run: null,
                    permissionMode: "read_only",
                    createdAt: "2026-11-23T00:00:00Z",
                    updatedAt: "2026-11-23T00:00:00Z",
                },
            ],
        };
        const memory = memoryStore(seed);
        const controller = new AgentConversationController({ identity, client: fakeClient(), store: memory.store });
        await controller.hydrate();
        controller.setModel("deepseek-v3");
        controller.setReasoningMode("high");
        controller.setSkillIds(["skill-a", "skill-b", "skill-a"]);
        controller.setSendMode("interject");
        await new Promise((resolve) => setTimeout(resolve, 220));
        controller.dispose();

        const restored = new AgentConversationController({ identity, client: fakeClient(), store: memory.store });
        await restored.hydrate();
        expect(restored.getSnapshot().model).toBe("deepseek-v3");
        expect(restored.getSnapshot().reasoningMode).toBe("high");
        expect(restored.getSnapshot().skillIds).toEqual(["skill-a", "skill-b"]);
        expect(restored.getSnapshot().sendMode).toBe("interject");
        restored.dispose();
    });

    test("projects context pressure into the shared snapshot and emits raw events once", async () => {
        const memory = memoryStore({
            version: 2,
            activeId: "local-1",
            conversations: [
                {
                    id: "local-1",
                    serverConversationId: "conversation-1",
                    title: "上下文",
                    messages: [],
                    run: run({ status: "running" }),
                    permissionMode: "read_only",
                    createdAt: "2026-11-23T00:00:00Z",
                    updatedAt: "2026-11-23T00:00:00Z",
                },
            ],
        });
        const seen: string[] = [];
        const client = fakeClient({
            async listConversationRuns() {
                return { items: [run({ status: "running", eventCount: 2 })] };
            },
            async getRun() {
                return { run: run({ status: "running", eventCount: 2 }) };
            },
            async readEvents(_id: string, onEvent: (item: AgentEvent) => void, options: { onSnapshot?: (snapshot: AgentRunSnapshot) => void }) {
                onEvent(event(1, { type: "context_pressure", payload: { modelLimitConfigured: true, inputBudgetTokens: 1000, estimatedInputTokens: 250 } }));
                onEvent(event(2, { type: "context_compacted", payload: { sourceBytes: 100 } }));
                options.onSnapshot?.(run({ status: "running", latestSeq: 2 }));
            },
        });
        const controller = new AgentConversationController({ identity, client, store: memory.store });
        controller.subscribeEvent((item) => seen.push(item.type));
        await controller.hydrate();
        await new Promise((resolve) => setTimeout(resolve, 0));
        expect(seen).toEqual(["context_pressure", "context_compacted"]);
        expect(controller.getSnapshot().contextUsage.reading).toMatchObject({ inputBudgetTokens: 1000 });
        expect(controller.getSnapshot().contextUsage.readingStale).toBe(true);
        controller.dispose();
    });

    test("applies queue revisions for edit, interject, cancel and pause", async () => {
        const seed: AgentConversationDocument = {
            version: 2,
            activeId: "local-1",
            conversations: [
                {
                    id: "local-1",
                    serverConversationId: "conversation-1",
                    title: "队列",
                    messages: [],
                    run: run(),
                    permissionMode: "read_only",
                    createdAt: "2026-11-23T00:00:00Z",
                    updatedAt: "2026-11-23T00:00:00Z",
                },
            ],
        };
        const memory = memoryStore(seed);
        const updates: Array<{ id: string; input: unknown }> = [];
        const queueActions: unknown[] = [];
        const client = fakeClient({
            async listConversationRuns() {
                return { items: [run()] };
            },
            async getConversationQueue() {
                return {
                    conversationId: "conversation-1",
                    latestRunId: "run-1",
                    revision: 7,
                    paused: false,
                    items: [{ id: "queued-1", conversationId: "conversation-1", mode: "queue", sequence: 1, revision: 3, prompt: "旧内容", status: "queued", createdAt: "", updatedAt: "" }],
                };
            },
            async updateQueuedMessage(_conversationId: string, messageId: string, input: unknown) {
                updates.push({ id: messageId, input });
                return { conversationId: "conversation-1", messageId, delivery: "queued" as const };
            },
            async updateConversationQueue(_id: string, input: unknown) {
                queueActions.push(input);
                return { conversationId: "conversation-1", latestRunId: "run-1", revision: 8, paused: true, items: [] };
            },
        });
        const controller = new AgentConversationController({ identity, client, store: memory.store });
        await controller.hydrate();
        const queued = controller.getSnapshot().queue?.items[0];
        expect(queued).toBeDefined();

        expect(await controller.editQueued(queued!, "新内容")).toBe(true);
        expect(await controller.interjectQueued(queued!)).toBe(true);
        expect(await controller.cancelQueued(queued!)).toBe(true);
        expect(await controller.updateQueue("pause")).toBe(true);
        expect(updates).toEqual([
            { id: "queued-1", input: { revision: 3, prompt: "新内容" } },
            { id: "queued-1", input: { revision: 3, action: "interject" } },
        ]);
        expect(queueActions).toEqual([{ revision: 7, action: "pause" }]);
        controller.dispose();
    });

    test("opens a historical run only inside the controller scope and restores its turns", async () => {
        const memory = memoryStore();
        let historyReads = 0;
        const client = fakeClient({
            async getRun(id: string) {
                return { run: run({ id, conversationId: "conversation-historical", scope: { type: "drama", id: "project-1" }, userPrompt: "历史分析" }) };
            },
            async listConversationRuns() {
                historyReads += 1;
                return { items: [run({ id: "historical-run", conversationId: "conversation-historical", scope: { type: "drama", id: "project-1" }, status: "completed" })] };
            },
        });
        const controller = new AgentConversationController({
            identity: { ...identity, scope: { type: "drama", id: "project-1" }, agentDefinitionId: "drama.analysis" },
            client,
            store: memory.store,
        });
        await controller.hydrate();
        expect(await controller.openRun("historical-run")).toBe(true);
        expect(controller.getSnapshot().activeConversationId).toBe("conversation-historical");
        expect(historyReads).toBe(1);
        controller.dispose();
    });

    test("rejects a historical run from another scope without changing the active conversation", async () => {
        const memory = memoryStore();
        const client = fakeClient({
            async getRun(id: string) {
                return { run: run({ id, scope: { type: "drama", id: "project-2" } }) };
            },
        });
        const controller = new AgentConversationController({
            identity: { ...identity, scope: { type: "drama", id: "project-1" }, agentDefinitionId: "drama.analysis" },
            client,
            store: memory.store,
        });
        await controller.hydrate();
        const before = controller.getSnapshot().activeConversationId;
        await expect(controller.openRun("foreign-run")).rejects.toThrow("该运行不属于当前页面范围");
        expect(controller.getSnapshot().activeConversationId).toBe(before);
        controller.dispose();
    });
});

describe("legacy canvas conversation migration", () => {
    test("preserves a full v1 conversation and nested run cursor", () => {
        const migrated = migrateLegacyCanvasConversationDocument(
            JSON.stringify({
                version: 1,
                activeId: "local-1",
                conversations: [
                    {
                        id: "local-1",
                        serverConversationId: "conversation-1",
                        draft: "未发送草稿",
                        title: "旧画布",
                        permissionMode: "read_only",
                        createdAt: "2026-11-20T00:00:00Z",
                        updatedAt: "2026-11-20T00:00:01Z",
                        messages: [{ id: "message-1", role: "user", text: "旧消息" }],
                        run: run({ id: "legacy-run", canvasId: "canvas-a", latestSeq: 4 }),
                        contextUsage: { runId: "legacy-run", reading: { estimatedInputTokens: 12 }, readingSeq: 3, readingStale: false, compactionPending: null, lastCompaction: null },
                        reasoningMode: "medium",
                        sendMode: "interject",
                        storyboardDisabled: true,
                    },
                ],
            }),
            "canvas-a",
        );
        expect(migrated.version).toBe(2);
        expect(migrated.activeId).toBe("local-1");
        expect(migrated.conversations[0].draft).toBe("未发送草稿");
        expect(migrated.conversations[0].messages[0].text).toBe("旧消息");
        expect(migrated.conversations[0].eventCursors?.["legacy-run"]).toBe(4);
        expect(migrated.conversations[0].contextUsage?.reading?.estimatedInputTokens).toBe(12);
        expect(migrated.conversations[0].reasoningMode).toBe("medium");
        expect(migrated.conversations[0].sendMode).toBe("interject");
        expect(migrated.conversations[0].adapterData).toEqual({ storyboardDisabled: true });
    });

    test("migrates canvas pending identity and rejects another canvas", () => {
        const pending = {
            fingerprint: "fingerprint",
            key: "pending-key",
            request: {
                canvasId: "canvas-a",
                prompt: "原请求",
                permissionMode: "read_only",
                idempotencyKey: "pending-key",
            },
        };
        expect(migrateLegacyCanvasPendingSubmission(JSON.stringify(pending), "canvas-a").request?.prompt).toBe("原请求");
        expect(() => migrateLegacyCanvasPendingSubmission(JSON.stringify(pending), "canvas-b")).toThrow();
    });
});
