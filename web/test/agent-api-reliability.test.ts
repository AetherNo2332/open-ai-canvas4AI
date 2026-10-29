import { afterAll, afterEach, beforeEach, describe, expect, it } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import type { AgentEvent, CreateAgentRunInput } from "../src/services/api/agent";

// Exercise the production protocol, isolating only transport/storage dependencies.
// Temporary modules avoid mock.module leaking into unrelated Bun test suites.
const dir = mkdtempSync(join(tmpdir(), "agent-reliability-"));
const root = new URL("../src/", import.meta.url);
const requestPath = join(dir, "request.ts");
writeFileSync(requestPath, 'export const apiBaseURL = "https://agent.invalid/api"; export const compactApiParams = (p) => Object.fromEntries(Object.entries(p).filter(([,v]) => v !== undefined)); export const http = { post: async () => { throw new Error("unexpected POST"); } };');
writeFileSync(join(dir, "agent.ts"), readFileSync(new URL("services/api/agent.ts", root), "utf8")
    .replace('"@/services/api/request"', JSON.stringify(requestPath))
    .replace('"@/services/api/task-text-stream"', JSON.stringify(fileURLToPath(new URL("services/api/task-text-stream.ts", root)))));
const api: typeof import("../src/services/api/agent") = await import(join(dir, "agent.ts"));
const transport = await import(requestPath);
const storagePath = join(dir, "storage.ts");
writeFileSync(storagePath, 'export const data = new Map(); export const localForageStorageForScope = (scope) => ({ getItem: async (k) => data.get(scope + ":" + k) ?? null, setItem: async (k,v) => {data.set(scope + ":" + k,v);}, removeItem: async (k) => {data.delete(scope + ":" + k);} });');
writeFileSync(join(dir, "scope.ts"), 'export const getActiveUserScope = () => "test-user";');
writeFileSync(join(dir, "conversations.ts"), readFileSync(new URL("services/cloud-agent-conversations.ts", root), "utf8")
    .replace('"@/lib/localforage-storage"', JSON.stringify(storagePath))
    .replace('"@/lib/user-scope"', JSON.stringify(join(dir, "scope.ts")))
    .replace('"@/lib/markdown-plain-text"', JSON.stringify(fileURLToPath(new URL("lib/markdown-plain-text.ts", root)))));
const conversations: typeof import("../src/services/cloud-agent-conversations") = await import(join(dir, "conversations.ts"));
const storage = await import(storagePath);
const nativeFetch = globalThis.fetch;
const nativeTimer = globalThis.setTimeout;
let delays: number[] = [];
let stops: Array<() => void> = [];
beforeEach(() => {
    delays = [];
    storage.data.clear();
    // Retain watchdog semantics; accelerate only retry waits, recording real delays.
    globalThis.setTimeout = ((fn: TimerHandler, ms?: number, ...args: unknown[]) => {
        delays.push(ms ?? 0);
        return nativeTimer(fn, ms === 45_000 ? ms : Math.min(ms ?? 0, 1), ...args);
    }) as typeof setTimeout;
});
afterEach(() => {
    stops.forEach((stop) => stop());
    stops = [];
    globalThis.fetch = nativeFetch;
    globalThis.setTimeout = nativeTimer;
});
afterAll(() => rmSync(dir, { recursive: true, force: true }));
const frame = (event: string, data: unknown) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`;
const snapshot = (status: string, extra = {}) => frame("run_snapshot", { id: "run", status, ...extra });
const event = (seq: number) => frame("agent_event", { runId: "run", eventId: `run:${seq}`, seq, type: "tool_completed", payload: { nodeId: "node" } });
const stream = (body: string) => new Response(body, { headers: { "content-type": "text/event-stream" } });
function observe(options: { timeoutMs?: number; after?: number } = {}) {
    const events: AgentEvent[] = [];
    const statuses: string[] = [];
    let finish!: (error?: unknown) => void;
    const done = new Promise<unknown>((resolve, reject) => {
        const deadline = nativeTimer(() => reject(new Error("subscription did not settle")), 3000);
        finish = (error) => { clearTimeout(deadline); resolve(error); };
    });
    const stop = api.subscribeAgentEvents("run", (e) => {
        events.push(e);
        if (e.type === "run_status" && !e.payload.cleanupPending && ["completed", "failed", "cancelled"].includes(String(e.payload.status))) finish();
    }, { ...options, onError: finish, onConnectionChange: (s) => statuses.push(s) });
    stops.push(stop);
    return { events, statuses, done, stop, finish };
}

describe("Agent SSE recovery", () => {
    it("sends the user's final image settings with the approval and keeps cancellation", async () => {
        const requests: unknown[][] = [];
        transport.http.post = async (...args: unknown[]) => { requests.push(args); return { accepted: true }; };
        const signal = new AbortController().signal;
        const mediaSettings = { logicalModelId: "chosen-image", size: "1536x1024", quality: "high" };
        await api.decideAgentApproval("run/id", "approval/id", "approve", "", signal, mediaSettings);
        expect(requests).toEqual([["/agent/runs/run%2Fid/approvals/approval%2Fid/decision", { decision: "approve", reason: undefined, mediaSettings }, { signal }]]);
    });
    it("posts a director preview result to the run's director-preview endpoint", async () => {
        const requests: unknown[][] = [];
        transport.http.post = async (...args: unknown[]) => { requests.push(args); return { accepted: true }; };
        await api.submitDirectorPreviewResult("run/id", "dpv_1", { videoResourceId: "res-1" });
        await api.submitDirectorPreviewResult("run/id", "dpv_2", { error: "录制失败" });
        expect(requests).toEqual([
            ["/agent/runs/run%2Fid/director-preview", { requestId: "dpv_1", videoResourceId: "res-1" }, { timeout: 20_000 }],
            ["/agent/runs/run%2Fid/director-preview", { requestId: "dpv_2", error: "录制失败" }, { timeout: 20_000 }],
        ]);
    });
    it.each([401, 403, 404, 422])("stops immediately on definitive %s", async (status) => {
        let requests = 0;
        globalThis.fetch = (async () => { requests++; return new Response("", { status }); }) as typeof fetch;
        const result = observe();
        expect(await result.done).toMatchObject({ status });
        expect(requests).toBe(1);
        expect(result.statuses.at(-1)).toBe("disconnected");
    });

    it.each(["", snapshot("running")])("bounds clean EOF retries even with repeated snapshots", async (body) => {
        let requests = 0;
        globalThis.fetch = (async () => { requests++; return stream(body); }) as typeof fetch;
        const result = observe();
        expect(await result.done).toBeInstanceOf(Error);
        expect(requests).toBe(5);
        expect(delays.filter((ms) => ms !== 45_000)).toHaveLength(4);
    });

    it("resumes the persisted cursor and deduplicates replayed events", async () => {
        const cursors: string[] = [];
        globalThis.fetch = (async (url, init) => {
            const cursor = new URL(String(url)).searchParams.get("after")!;
            cursors.push(cursor);
            expect((init?.headers as Record<string, string>)["Last-Event-ID"]).toBe(cursor);
            if (cursors.length === 2) throw new TypeError("connection reset");
            return stream(cursors.length === 1 ? event(1) + snapshot("running") : event(1) + event(2) + snapshot("completed"));
        }) as typeof fetch;
        const result = observe();
        expect(await result.done).toBeUndefined();
        expect(cursors).toEqual(["0", "1", "1"]);
        expect(result.events.filter((e) => e.seq > 0).map((e) => e.seq)).toEqual([1, 2]);
    });

    it("marks a replayed streaming draft as a non-final assistant message", async () => {
        // 断线/刷新后由 run_snapshot.activeMessage 补出来的那条正文是**还在流式生成**的草稿，
        // 不是本轮最终答复。少了 final=false，前端会按"缺省即结论"把它显示成最终回复
        // （工作项 A：收尾闸门要求界面上把过程说明与结论分开）。
        globalThis.fetch = (async () => stream(snapshot("running", { activeMessage: { messageId: "task-1", text: "我先读一下画布。" } }))) as typeof fetch;
        const result = observe();
        await result.done;
        const drafts = result.events.filter((e) => e.type === "assistant_message");
        expect(drafts).toHaveLength(1);
        expect(drafts[0].payload).toMatchObject({ messageId: "task-1", text: "我先读一下画布。", final: false });
    });

    it("waits for durable cleanup after a terminal status", async () => {
        let requests = 0;
        globalThis.fetch = (async () => stream(snapshot("cancelled", { cleanupPending: ++requests === 1 }))) as typeof fetch;
        const result = observe();
        expect(await result.done).toBeUndefined();
        expect(requests).toBe(2);
        expect(result.events.filter((e) => e.type === "run_status").map((e) => e.payload.cleanupPending)).toEqual([true, false]);
    });

    it("forwards compaction changes in snapshots even while status remains running", async () => {
        const compaction = { status: "running", turnCount: 4, sourceBytes: 10000, resume: true };
        globalThis.fetch = (async () => stream(snapshot("running", { contextCompaction: compaction }) + snapshot("running") + snapshot("completed"))) as typeof fetch;
        const result = observe();
        expect(await result.done).toBeUndefined();
        const statuses = result.events.filter((e) => e.type === "run_status");
        expect(statuses.map((e) => e.payload.contextCompaction)).toEqual([compaction, undefined, undefined]);
        expect(statuses.every((e) => Object.hasOwn(e.payload, "contextCompaction"))).toBe(true);
        expect(statuses.every((e) => e.seq === 0)).toBe(true);
    });

    it("honors Retry-After rather than immediately retrying 429", async () => {
        let requests = 0;
        globalThis.fetch = (async () => ++requests === 1 ? new Response("", { status: 429, headers: { "retry-after": "60" } }) : stream(snapshot("completed"))) as typeof fetch;
        const result = observe();
        await result.done;
        expect(requests).toBe(2);
        expect(delays).toContain(60_000);
    });

    it("returns long Retry-After waits to the user without retrying early", async () => {
        let requests = 0;
        globalThis.fetch = (async () => { requests++; return new Response("", { status: 429, headers: { "retry-after": "600" } }); }) as typeof fetch;
        const result = observe();
        expect(await result.done).toMatchObject({ status: 429 });
        expect(requests).toBe(1);
    });

    it("times out a stalled connection with a bounded retry budget", async () => {
        let requests = 0;
        globalThis.fetch = (async (_url, init) => {
            requests++;
            return new Promise<Response>((_resolve, reject) => init?.signal?.addEventListener("abort", () => reject(new Error("timeout")), { once: true }));
        }) as typeof fetch;
        expect(await observe({ timeoutMs: 10 }).done).toBeInstanceOf(Error);
        expect(requests).toBe(5);
    });

    it("unsubscribe aborts the live reader without reporting a run failure", async () => {
        let cancelled = false;
        globalThis.fetch = (async () => new Response(new ReadableStream({ cancel() { cancelled = true; } }), { headers: { "content-type": "text/event-stream" } })) as typeof fetch;
        const result = observe();
        await new Promise((resolve) => nativeTimer(resolve, 5));
        result.stop();
        await new Promise((resolve) => nativeTimer(resolve, 5));
        expect(cancelled).toBe(true);
        expect(result.statuses).not.toContain("disconnected");
        result.finish();
        await result.done;
    });
});

describe("Agent admission replay and durable pending records", () => {
    const input: CreateAgentRunInput = { canvasId: "canvas", prompt: "generate", model: "original", idempotencyKey: "stable-key-123" };
    it("requests scoped history pages with cancellation and starts event hydration at seq one", async () => {
        const requests: Array<{ path: string; options: any }> = [];
        transport.http.get = async (path: string, options: any) => { requests.push({ path, options }); return { items: [] }; };
        const controller = new AbortController();
        await api.listAgentConversations({ canvasId: "canvas", limit: 100, signal: controller.signal });
        await api.listAgentConversationRuns("a/b", { after: "cursor", signal: controller.signal });
        await api.getAgentRun("run", controller.signal, { fromStart: true, eventLimit: 500 });
        expect(requests[0]).toEqual({ path: "/agent/conversations", options: { params: { canvasId: "canvas", limit: 100 }, signal: controller.signal } });
        expect(requests[1]).toEqual({ path: "/agent/conversations/a%2Fb/runs", options: { params: { after: "cursor" }, signal: controller.signal } });
        expect(requests[2]).toEqual({ path: "/agent/runs/run?eventLimit=500&fromStart=true", options: { signal: controller.signal } });
    });
    it("aborts a switched account's pending admission before another retry", async () => {
        const controller = new AbortController();
        let attempts = 0;
        transport.http.post = async (_path: string, _body: unknown, options: { signal?: AbortSignal }) => {
            attempts++;
            expect(options.signal).toBe(controller.signal);
            controller.abort();
            throw { retryable: true };
        };
        await expect(api.createAgentRun(input, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
        expect(attempts).toBe(1);
    });
    it("clears the captured account recovery identity without touching the new account", async () => {
        const first = { fingerprint: "first", key: input.idempotencyKey, request: input };
        const second = { fingerprint: "second", key: "second-stable-key", request: { ...input, idempotencyKey: "second-stable-key" } };
        await conversations.saveCloudAgentPendingSubmission("canvas", "same-chat", first, "first-account");
        await conversations.saveCloudAgentPendingSubmission("canvas", "same-chat", second, "second-account");
        await conversations.clearCloudAgentPendingSubmission("canvas", "same-chat", "first-account");
        expect(await conversations.loadCloudAgentPendingSubmission("canvas", "same-chat", "first-account")).toBeNull();
        expect(await conversations.loadCloudAgentPendingSubmission("canvas", "same-chat", "second-account")).toEqual(second);
    });
    it("creates and continues Cloud Agent runs with only Skill IDs", async () => {
        const bodies: unknown[] = [];
        transport.http.post = async (_path: string, body: unknown) => { bodies.push(body); return { run: { id: "run" } }; };
        const request = { ...input, skillIds: ["skill-one", "skill-two"] };
        await api.createAgentRun(request);
        await api.sendAgentMessage("run", request);
        expect(bodies).toEqual([request, request]);
        for (const body of bodies) {
            expect(Object.keys(body as object).filter((key) => /skill/i.test(key))).toEqual(["skillIds"]);
        }
    });
    it("retries with a frozen original body/key and a bounded HTTP timeout", async () => {
        const bodies: unknown[] = [];
        const mutable = { ...input };
        transport.http.post = async (_path: string, body: unknown, options: unknown) => {
            bodies.push(structuredClone(body));
            expect(options).toEqual({ timeout: 15_000 });
            mutable.model = "changed";
            if (bodies.length < 3) throw { retryable: true };
            return { run: { id: "accepted" } };
        };
        expect(await api.createAgentRun(mutable)).toMatchObject({ run: { id: "accepted" } });
        expect(bodies).toEqual([input, input, input]);
    });

    it("limits transport failures to three attempts, but never retries admission rejection", async () => {
        let attempts = 0;
        transport.http.post = async () => { attempts++; throw { retryable: true }; };
        await expect(api.sendAgentMessage("parent", input)).rejects.toMatchObject({ retryable: true });
        expect(attempts).toBe(3);
        attempts = 0;
        transport.http.post = async () => { attempts++; throw { status: 400, retryable: false }; };
        await expect(api.createAgentRun(input)).rejects.toMatchObject({ status: 400 });
        expect(attempts).toBe(1);
    });

    it("restores the full original request and message identity, isolated by conversation", async () => {
        const pending = { fingerprint: "fingerprint", key: input.idempotencyKey, request: input, parentRunId: "parent", messageId: "bubble", composerDraft: "分镜请求之外的 Agent 草稿" };
        await conversations.saveCloudAgentPendingSubmission("canvas", "conversation", pending);
        expect(await conversations.loadCloudAgentPendingSubmission("canvas", "conversation")).toEqual(pending);
        expect(await conversations.loadCloudAgentPendingSubmission("canvas", "other")).toBeNull();
        await conversations.clearCloudAgentPendingSubmission("canvas", "conversation");
        expect(await conversations.loadCloudAgentPendingSubmission("canvas", "conversation")).toBeNull();
    });

    it("freezes queued message delivery and question association across retries", async () => {
        const saved = { fingerprint: "queue", key: input.idempotencyKey, request: input, parentRunId: "parent", conversationMessage: { conversationId: "server", mode: "queue" as const, expectedRunId: "parent" } };
        await conversations.saveCloudAgentPendingSubmission("canvas", "conversation", saved);
        expect(await conversations.loadCloudAgentPendingSubmission("canvas", "conversation")).toEqual(saved);
        const bodies: unknown[] = [];
        const request = { mode: "queue" as const, expectedRunId: "parent", request: { ...input } };
        transport.http.post = async (path: string, body: unknown) => {
            expect(path).toBe("/agent/conversations/server/messages");
            bodies.push(structuredClone(body));
            request.request.prompt = "changed after sending";
            if (bodies.length < 2) throw { retryable: true };
            return { delivery: "queued", messageId: "message", queueRevision: 2 };
        };
        expect(await api.sendAgentConversationMessage("server", request)).toMatchObject({ delivery: "queued" });
        expect(bodies[0]).toEqual(bodies[1]);
    });

    it("fails closed when pending delivery mode is invalid", async () => {
        await conversations.saveCloudAgentPendingSubmission("canvas", "conversation", { fingerprint: "queue", key: input.idempotencyKey, request: input, parentRunId: "parent" });
        const key = [...storage.data.keys()][0];
        const raw = JSON.stringify({ fingerprint: "queue", key: input.idempotencyKey, request: input, parentRunId: "parent", conversationMessage: { conversationId: "server", mode: "unknown", expectedRunId: "parent" } });
        storage.data.set(key, raw);
        await expect(conversations.loadCloudAgentPendingSubmission("canvas", "conversation")).rejects.toThrow("已暂停本对话发送");
        expect(storage.data.get(key)).toBe(raw);
    });

    it.each(["{broken", "null", "{}", JSON.stringify({ fingerprint: "f", key: input.idempotencyKey, request: { ...input, idempotencyKey: "wrong-key" } })])("fails closed on corrupt pending recovery data", async (raw) => {
        await conversations.saveCloudAgentPendingSubmission("canvas", "conversation", { fingerprint: "f", key: input.idempotencyKey });
        const key = [...storage.data.keys()][0];
        storage.data.set(key, raw);
        await expect(conversations.loadCloudAgentPendingSubmission("canvas", "conversation")).rejects.toThrow("已暂停本对话发送");
        expect(storage.data.get(key)).toBe(raw);
    });
});
