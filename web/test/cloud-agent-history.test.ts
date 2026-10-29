import { describe, expect, test } from "bun:test";
import { loadCloudAgentConversation, mergeCloudAgentConversation, mergeCloudAgentConversations, projectCloudAgentConversation, refreshCloudAgentConversations, type CloudAgentHistoryClient } from "@/services/cloud-agent-history";
import type { AgentEvent, AgentRun, AgentConversationSummary } from "@/services/api/agent";
import type { CloudAgentConversation } from "@/services/cloud-agent-conversations";

const at = "2026-10-23T00:00:00Z";
const conversationId = "conversation-1";
const run = (id: string, overrides: Partial<AgentRun> = {}): AgentRun => ({
    id,
    conversationId,
    canvasId: "canvas-1",
    userPrompt: `提示词-${id}`,
    status: "completed",
    permissionMode: "request_approval",
    createdAt: at,
    updatedAt: at,
    revision: 1,
    ...overrides,
});
const event = (runId: string, seq: number, type: string, payload: AgentEvent["payload"] = {}): AgentEvent => ({ eventId: `${runId}:${seq}`, runId, seq, type, payload, createdAt: at });
const summary = (overrides: Partial<AgentConversationSummary> = {}): AgentConversationSummary => ({
    id: conversationId,
    canvasId: "canvas-1",
    title: "对话",
    latestRunId: "first",
    status: "completed",
    revision: 1,
    createdAt: at,
    updatedAt: at,
    ...overrides,
});
function client(overrides: Partial<CloudAgentHistoryClient> = {}): CloudAgentHistoryClient {
    return {
        listConversations: async () => ({ items: [summary()] }),
        listRuns: async () => ({ items: [run("first")] }),
        getRun: async (id) => ({ run: run(id, { ...(id === "child" ? { parentId: "first" } : {}), eventCount: 0, events: [] }) }),
        ...overrides,
    };
}

describe("Agent cloud conversation history", () => {
    test("remote archive removes cached history without loading its runs", async () => {
        const local = await loadCloudAgentConversation(conversationId, { client: client() });
        const remote = await refreshCloudAgentConversations({
            canvasId: "canvas-1",
            existing: [local],
            client: client({
                listConversations: async () => ({ items: [summary({ archived: true })] }),
                listRuns: async () => {
                    throw new Error("archived history must not reload");
                },
            }),
        });
        expect(remote[0].archived).toBe(true);
        expect(mergeCloudAgentConversations([local], remote)).toEqual([]);
        const legacy = { ...local, serverConversationId: undefined, run: { ...run(conversationId), conversationId: undefined } };
        expect(mergeCloudAgentConversations([legacy], remote)).toEqual([]);
    });
    test("restores every run page and every event page from the first event", async () => {
        const reads: Array<{ id: string; seq: number; fromStart?: boolean }> = [];
        const restored = await loadCloudAgentConversation(conversationId, {
            client: client({
                listRuns: async (_id, options) => {
                    expect(options?.limit).toBe(100);
                    return options?.after ? { items: [run("second", { parentId: "first" })] } : { items: [run("first")], nextCursor: "second-page" };
                },
                getRun: async (id, _signal, options) => {
                    const seq = options?.sinceSeq || 0;
                    reads.push({ id, seq, fromStart: options?.fromStart });
                    if (id === "first") return { run: run(id, { eventCount: 1, events: [event(id, 1, "assistant_message", { messageId: "first-answer", text: "第一轮回答" })] }) };
                    if (!seq)
                        return {
                            run: run(id, {
                                parentId: "first",
                                eventCount: 3,
                                events: [event(id, 1, "assistant_delta", { messageId: "second-answer", text: "第二" }), event(id, 2, "assistant_delta", { messageId: "second-answer", text: "轮" })],
                                eventsTruncated: true,
                            }),
                        };
                    return { run: run(id, { parentId: "first", eventCount: 3, events: [event(id, 3, "assistant_message", { messageId: "second-answer", text: "第二轮回答" })] }) };
                },
            }),
        });
        expect(restored.serverConversationId).toBe(conversationId);
        expect(restored.runs?.map((item) => item.id)).toEqual(["first", "second"]);
        expect(restored.messages.map((item) => item.text)).toEqual(["提示词-first", "第一轮回答", "提示词-second", "第二轮回答"]);
        expect(restored.run?.id).toBe("second");
        expect(restored.lastEventSeq).toBe(3);
        expect(reads).toContainEqual({ id: "second", seq: 2, fromStart: false });
        expect(reads.filter((item) => item.seq === 0).every((item) => item.fromStart)).toBe(true);
    });

    test("rejects incomplete event pages rather than hiding missing history", async () => {
        await expect(loadCloudAgentConversation(conversationId, { client: client({ getRun: async (id) => ({ run: run(id, { eventCount: 2, events: [event(id, 2, "assistant_message", { text: "缺失前缀" })] }) }) }) })).rejects.toThrow("历史事件未完整");
    });

    test("does not trust a tail-only event window even when it is cached on the run summary", async () => {
        const reads: number[] = [];
        const result = await loadCloudAgentConversation(conversationId, {
            client: client({
                listRuns: async () => ({ items: [run("first", { eventCount: 2, events: [event("first", 2, "assistant_message", { messageId: "answer", text: "完整回答" })], eventsTruncated: true })] }),
                getRun: async (id, _signal, options) => {
                    reads.push(options?.sinceSeq || 0);
                    return { run: run(id, { eventCount: 2, events: [event(id, 1, "assistant_delta", { messageId: "answer", text: "完整" }), event(id, 2, "assistant_message", { messageId: "answer", text: "完整回答" })] }) };
                },
            }),
        });
        expect(reads).toEqual([0]);
        expect(result.messages.at(-1)?.text).toBe("完整回答");
    });

    test("replays tools, plans, interjections and failure without browser side effects", () => {
        const first = run("first", {
            events: [
                event("first", 1, "plan_updated", { items: [{ id: "step", title: "生成图片", status: "doing" }] }),
                event("first", 2, "user_interjection", { messageId: "interjection-1", text: "改成清晨" }),
                event("first", 3, "director_preview_requested", { callId: "preview-1", toolName: "director", action: "preview", requestId: "dpv_1", sceneId: "scene-1", status: "awaiting_viewport" }),
                event("first", 4, "tool_completed", { callId: "preview-1", toolName: "director", arguments: JSON.stringify({ action: "preview" }), text: "预演完成", result: { sceneId: "scene-1" } }),
                event("first", 5, "generation_task_created", { taskId: "task-1", nodeId: "node-1", text: "图片生成" }),
            ],
        });
        const failed = run("second", { parentId: first.id, status: "failed", failureMessage: "模型额度不足", events: [] });
        const result = projectCloudAgentConversation(conversationId, [failed, first]);
        expect(result.messages.find((item) => item.id === "plan-first")).toMatchObject({ planTerminal: true });
        expect(result.messages.find((item) => item.id === "interjection-1")).toMatchObject({ role: "user", interjection: "sent" });
        expect(result.messages.find((item) => item.id === "first:3")).toMatchObject({ role: "tool", title: "director", text: "等待导演台录制预演" });
        expect(result.messages.find((item) => item.id === "first:4")?.text).toBe("预演完成");
        expect(result.messages.find((item) => item.title === "generate_media")?.detail).toMatchObject({ taskId: "task-1" });
        expect(result.messages.find((item) => item.id === "terminal-second")?.text).toBe("模型额度不足");
    });

    test("orders parent before child with equal timestamps and deduplicates event identity", () => {
        const answer = event("z-parent", 1, "assistant_message", { messageId: "answer", text: "父轮" });
        const result = projectCloudAgentConversation(conversationId, [run("a-child", { parentId: "z-parent" }), run("z-parent", { events: [answer, answer, event("foreign", 2, "assistant_message", { text: "他人消息" })] })]);
        expect(result.runs?.map((item) => item.id)).toEqual(["z-parent", "a-child"]);
        expect(result.messages.filter((item) => item.text === "父轮")).toHaveLength(1);
        expect(JSON.stringify(result.messages)).not.toContain("他人消息");
    });

    test("rejects foreign conversation/run identity and cyclic parents", async () => {
        await expect(loadCloudAgentConversation(conversationId, { client: client({ listRuns: async () => ({ items: [run("foreign", { conversationId: "other-conversation" })] }) }) })).rejects.toThrow("对话归属不一致");
        await expect(loadCloudAgentConversation(conversationId, { client: client({ getRun: async () => ({ run: run("wrong-run") }) }) })).rejects.toThrow("轮次归属不一致");
        expect(() => projectCloudAgentConversation(conversationId, [run("cycle-a", { parentId: "cycle-b" }), run("cycle-b", { parentId: "cycle-a" })])).toThrow("轮次关系异常");
    });

    test("fails closed on repeated pagination cursors and account changes", async () => {
        await expect(loadCloudAgentConversation(conversationId, { client: client({ listRuns: async () => ({ items: [], nextCursor: "same" }) }) })).rejects.toThrow("分页未前进");
        await expect(loadCloudAgentConversation(conversationId, { scope: "another-account", client: client() })).rejects.toMatchObject({ name: "AbortError" });
        const controller = new AbortController();
        controller.abort();
        await expect(loadCloudAgentConversation(conversationId, { signal: controller.signal, client: client() })).rejects.toMatchObject({ name: "AbortError" });
    });

    test("keeps local IDs, drafts, rendered user text and ambiguous POST rows", () => {
        const remote = projectCloudAgentConversation(conversationId, [run("first", { idempotencyKey: "submitted-key", events: [event("first", 1, "assistant_message", { messageId: "answer", text: "服务器新回复" })] })]);
        const local: CloudAgentConversation = {
            ...remote,
            id: "browser-local-id",
            draft: "未发送草稿",
            messages: [
                { id: "user-submitted-key", role: "user", text: "界面里的简短问题" },
                { id: "answer", role: "assistant", text: "缓存旧回复" },
                { id: "unknown-post", role: "user", text: "提交结果未知" },
            ],
        };
        const merged = mergeCloudAgentConversation(local, remote);
        expect(merged.id).toBe("browser-local-id");
        expect(merged.serverConversationId).toBe(conversationId);
        expect(merged.draft).toBe("未发送草稿");
        expect(merged.messages.map((item) => item.text)).toEqual(["界面里的简短问题", "服务器新回复", "提交结果未知"]);
        expect(merged.messages[0].id).toBe("user-first");
    });

    test("links old cache only by an owned run ID, never by a local ID or matching prompt", () => {
        const remote = projectCloudAgentConversation(conversationId, [run("first")]);
        const old = { ...remote, id: "browser-old-id", serverConversationId: undefined, run: { ...remote.run!, conversationId: undefined } };
        expect(mergeCloudAgentConversations([old], [remote])).toHaveLength(1);
        expect(mergeCloudAgentConversations([old], [remote])[0].id).toBe("browser-old-id");
        const unrelated = { ...old, id: conversationId, run: null, runs: [], draft: "原本地草稿" };
        const merged = mergeCloudAgentConversations([unrelated], [remote]);
        expect(merged).toHaveLength(2);
        expect(merged.find((item) => item.run === null)?.draft).toBe("原本地草稿");
        expect(() => mergeCloudAgentConversation(unrelated, remote)).toThrow("归属不一致");
    });

    test("old snapshots cannot regress a newer observed terminal run, message or cursor", () => {
        const remote = projectCloudAgentConversation(conversationId, [run("first", { status: "running", revision: 1, latestSeq: 2 })]);
        const local = { ...remote, lastEventSeq: 8, messages: [{ id: "live", role: "assistant" as const, text: "新的实时完整回复" }], run: { ...remote.run!, status: "completed" as const, revision: 2 } };
        const merged = mergeCloudAgentConversation(local, remote);
        expect(merged.run?.status).toBe("completed");
        expect(merged.lastEventSeq).toBe(8);
        expect(merged.messages).toEqual(local.messages);
    });

    test("an ancestor snapshot cannot replace a locally accepted descendant regardless of per-run revision", () => {
        const parent = run("parent", { revision: 100 });
        const child = run("child", { parentId: parent.id, revision: 1 });
        const local = projectCloudAgentConversation(conversationId, [parent, child, run("grandchild", { parentId: child.id, revision: 1, status: "running", latestSeq: 4 })]);
        const merged = mergeCloudAgentConversation(local, projectCloudAgentConversation(conversationId, [parent]));
        expect(merged.run?.id).toBe("grandchild");
        expect(merged.lastEventSeq).toBe(4);
        expect(merged.messages).toEqual(local.messages);
        expect(mergeCloudAgentConversation(projectCloudAgentConversation(conversationId, [parent]), local).run?.id).toBe("grandchild");
    });

    test("restores an explicitly declared legacy tail without inventing its missing prefix or blocking other conversations", async () => {
        const result = await refreshCloudAgentConversations({
            canvasId: "canvas-1",
            client: client({
                listConversations: async () => ({ items: [summary(), summary({ id: "new", latestRunId: "new" })] }),
                listRuns: async (id) => ({ items: [run(id, { conversationId: id })] }),
                getRun: async (id) => ({
                    run: run(id, {
                        conversationId: id,
                        eventSeqBase: id === conversationId ? 2 : 0,
                        eventCount: id === conversationId ? 3 : 0,
                        events: id === conversationId ? [event(id, 3, "assistant_message", { messageId: "tail", text: "可用旧记录" })] : [],
                    }),
                }),
            }),
        });
        expect(result).toHaveLength(2);
        const legacy = result.find((item) => item.serverConversationId === conversationId)!;
        expect(legacy.historyState?.status).toBe("partial");
        expect(legacy.historyState?.message).toContain("早期记录");
        expect(legacy.run?.events?.map((item) => item.seq)).toEqual([3]);
        expect(legacy.run?.eventSeqBase).toBe(2);
        expect(legacy.lastEventSeq).toBe(3);
        expect(legacy.messages.at(-1)?.text).toBe("可用旧记录");
        expect(result.find((item) => item.serverConversationId === "new")?.historyState).toBeUndefined();
    });

    test("isolates a corrupt conversation and retries it without losing its cached draft", async () => {
        const cached = { ...projectCloudAgentConversation(conversationId, [run("first")]), draft: "未发送草稿" };
        let broken = true;
        let reads = 0;
        const api = client({
            listConversations: async () => ({ items: [summary({ revision: 2 }), summary({ id: "healthy", latestRunId: "healthy", revision: 2 })] }),
            listRuns: async (id) => {
                reads++;
                return { items: [run(id === conversationId ? "first" : id, { conversationId: id, revision: 2 })] };
            },
            getRun: async (id) => ({
                run: run(id, {
                    conversationId: id === "first" ? conversationId : id,
                    revision: 2,
                    eventSeqBase: 0,
                    eventCount: broken && id === "first" ? 2 : 0,
                    events: broken && id === "first" ? [event(id, 2, "assistant_message", { text: "真实坏页" })] : [],
                }),
            }),
        });
        const restored = await refreshCloudAgentConversations({ canvasId: "canvas-1", existing: [cached], client: api });
        const failed = restored.find((item) => item.serverConversationId === conversationId)!;
        expect(failed.historyState?.status).toBe("unavailable");
        expect(failed.historyState?.message).toContain("历史事件未完整");
        expect(failed.draft).toBe("未发送草稿");
        expect(restored.find((item) => item.serverConversationId === "healthy")?.run?.id).toBe("healthy");
        broken = false;
        const retried = await refreshCloudAgentConversations({ canvasId: "canvas-1", existing: restored, client: api });
        expect(reads).toBe(3);
        expect(retried.find((item) => item.serverConversationId === conversationId)?.historyState).toBeUndefined();
    });

    test("a declared tail still rejects a mismatched base and gaps within or after the initial page", async () => {
        for (const events of [[3], [3, 5]]) {
            await expect(
                loadCloudAgentConversation(conversationId, {
                    client: client({
                        getRun: async (id) => ({
                            run: run(id, {
                                eventSeqBase: events.length === 1 ? 1 : 2,
                                eventCount: 5,
                                events: events.map((seq) => event(id, seq, "assistant_delta")),
                            }),
                        }),
                    }),
                }),
            ).rejects.toThrow("历史事件未完整");
        }
        await expect(
            loadCloudAgentConversation(conversationId, {
                client: client({
                    getRun: async (id, _signal, options) => ({
                        run: run(id, {
                            eventSeqBase: options?.sinceSeq ? 2 : 0,
                            eventCount: 3,
                            events: [event(id, options?.sinceSeq ? 3 : 1, "assistant_delta")],
                        }),
                    }),
                }),
            }),
        ).rejects.toThrow("历史事件未完整");
    });

    test("per-conversation isolation retains pagination errors but does not swallow account cancellation", async () => {
        const api = client({ listRuns: async () => ({ items: [], nextCursor: "stuck" }) });
        const result = await refreshCloudAgentConversations({ canvasId: "canvas-1", client: api });
        expect(result[0].historyState?.status).toBe("unavailable");
        expect(result[0].historyState?.message).toContain("分页未前进");
        const controller = new AbortController();
        await expect(
            refreshCloudAgentConversations({
                canvasId: "canvas-1",
                signal: controller.signal,
                client: client({
                    listRuns: async () => {
                        controller.abort();
                        throw new Error("request aborted");
                    },
                }),
            }),
        ).rejects.toMatchObject({ name: "AbortError" });
    });

    test("directory pagination reads every conversation with public limits and reuses unchanged cache", async () => {
        const cached = projectCloudAgentConversation(conversationId, [run("first")]);
        const seen: Array<string | undefined> = [];
        let reads = 0;
        const result = await refreshCloudAgentConversations({
            canvasId: "canvas-1",
            existing: [cached],
            client: client({
                listConversations: async (options) => {
                    expect(options).toMatchObject({ canvasId: "canvas-1", limit: 100 });
                    seen.push(options.before);
                    return options.before ? { items: [summary({ id: "second-conversation" })] } : { items: [summary()], nextCursor: "next-page" };
                },
                listRuns: async (id) => {
                    reads++;
                    return { items: [run("second", { conversationId: id })] };
                },
                getRun: async (id) => ({ run: run(id, { conversationId: "second-conversation", eventCount: 0, events: [] }) }),
            }),
        });
        expect(seen).toEqual([undefined, "next-page"]);
        expect(result).toHaveLength(2);
        expect(result[0]).toBe(cached);
        expect(reads).toBe(1);
    });

    test("new child identity bypasses unchanged session metadata and selected cross-page history stays observed", async () => {
        const cached = projectCloudAgentConversation(conversationId, [run("first")]);
        const result = await refreshCloudAgentConversations({
            canvasId: "canvas-1",
            existing: [cached],
            client: client({
                listConversations: async () => ({ items: [summary({ latestRunId: "child" })] }),
                listRuns: async () => ({ items: [run("first"), run("child", { parentId: "first" })] }),
            }),
        });
        expect(result[0].run?.id).toBe("child");
        let listing = false;
        const selected = await refreshCloudAgentConversations({
            observedConversationIds: [conversationId],
            client: client({
                listConversations: async () => {
                    listing = true;
                    return { items: [] };
                },
            }),
        });
        expect(listing).toBe(false);
        expect(selected[0].run?.id).toBe("first");
    });
});
