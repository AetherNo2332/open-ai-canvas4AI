import { test, expect } from "bun:test";
import { createAgentClient, AgentRequestError } from "../src/sdk/agent/client";
import type { AgentTransport, AgentTransportRequest } from "../src/sdk/agent/transport";
import { AGENT_CONTRACT_VERSION, type AgentRunRequest } from "../src/sdk/agent/contract";
import fixtures from "../../agent/contracts/agent-sdk-v1.json";

const input = {
    canvasId: "canvas-123",
    scope: { type: "canvas" as const, id: "canvas-123" },
    prompt: "分析剧情",
    permissionMode: "read_only" as const,
    budget: { maxCredits: 20 },
    channelId: "channel",
    channelModelKey: "text-test",
    idempotencyKey: "sdk-message-123",
};

test("SDK retries immutable admission and preserves machine error reasons", async () => {
    const calls: AgentTransportRequest[] = [];
    const transport: AgentTransport = {
        async request(call) {
            calls.push(structuredClone(call));
            if (calls.length === 1) throw new TypeError("connection lost");
            return { status: 200, body: { code: 0, data: { run: { id: "run-1" } } } };
        },
        async openEvents() {
            throw new Error("unused");
        },
    };
    const client = createAgentClient({ transport, retryDelayMs: () => 0 });
    const pending = client.startRun(input);
    input.prompt = "mutated after submission";
    expect((await pending).run.id).toBe("run-1");
    expect(calls[0].body).toEqual(calls[1].body);
    expect((calls[0].body as typeof input).prompt).toBe("分析剧情");
    transport.request = async () => ({ status: 409, body: { code: 409, data: null, reason: "agent_idempotency_conflict", msg: "display only" } });
    try {
        await client.startRun(input);
        throw new Error("expected rejection");
    } catch (error) {
        expect(error).toBeInstanceOf(AgentRequestError);
        expect((error as AgentRequestError).reason).toBe("agent_idempotency_conflict");
        expect((error as AgentRequestError).retryable).toBe(false);
    }
});

test("shared Go/TS fixtures preserve wire keys and server error reasons", async () => {
    expect(fixtures.contractVersion).toBe(AGENT_CONTRACT_VERSION);
    for (const item of fixtures.cases) {
        const request = item.request as AgentRunRequest;
        const client = createAgentClient({
            transport: {
                async request(call) {
                    expect(call.body).toEqual(item.request);
                    return item.accepted ? { status: 200, body: { code: 0, data: { run: { id: item.name, scope: item.scope } } } } : { status: 400, body: { code: 400, data: null, reason: item.reason } };
                },
                async openEvents() {
                    throw new Error("unused");
                },
            },
        });
        if (item.accepted) expect((await client.startRun(request)).run.scope).toEqual(item.scope);
        else await expect(client.startRun(request)).rejects.toMatchObject({ reason: item.reason, retryable: false });
    }
});

test("SDK retains queue envelope, approval payload and long retry-after without retrying", async () => {
    const calls: AgentTransportRequest[] = [];
    const transport: AgentTransport = {
        async request(call) {
            calls.push(call);
            return { status: 200, body: { code: 0, data: {} } };
        },
        async openEvents() {
            throw new Error("unused");
        },
    };
    const client = createAgentClient({ transport, retryDelayMs: () => 0 });
    const message = { mode: "queue" as const, expectedRunId: "run-1", request: input };
    await client.sendMessage("conversation/1", message);
    await client.decideApproval("run-1", "approval/1", { decision: "reject", reason: "用户取消" });
    expect(calls[0]).toMatchObject({ path: "/agent/conversations/conversation%2F1/messages", body: message });
    expect(calls[1]).toMatchObject({ path: "/agent/runs/run-1/approvals/approval%2F1/decision", body: { decision: "reject", reason: "用户取消" } });
    let attempts = 0;
    transport.request = async () => {
        attempts++;
        return { status: 429, body: { code: 429, reason: "rate_limited", data: null }, retryAfterMs: 60_000 };
    };
    await expect(client.startRun(input)).rejects.toMatchObject({ reason: "rate_limited", retryAfterMs: 60_000 });
    expect(attempts).toBe(1);
});

test("SDK terminal snapshots do not advance event cursors or trigger reconnection", async () => {
    let connections = 0;
    const client = createAgentClient({
        transport: {
            async request() {
                throw new Error("unused");
            },
            async openEvents() {
                connections++;
                return new Response('event: run_snapshot\ndata: {"id":"run","status":"completed","latestSeq":99}\n\n', { headers: { "content-type": "text/event-stream" } });
            },
        },
    });
    let status = "";
    await client.readEvents(
        "run",
        () => {
            throw new Error("snapshot emitted as event");
        },
        {
            after: 4,
            onSnapshot: (run) => {
                status = run.status;
            },
        },
    );
    expect(status).toBe("completed");
    expect(connections).toBe(1);
});

test("SDK bounds idle SSE attempts and preserves cancellation", async () => {
    let attempts = 0;
    const client = createAgentClient({
        retryDelayMs: () => 0,
        transport: {
            async request() {
                throw new Error("unused");
            },
            async openEvents() {
                attempts++;
                return new Response(new ReadableStream(), { headers: { "content-type": "text/event-stream" } });
            },
        },
    });
    await expect(client.readEvents("run", () => {}, { idleTimeoutMs: 5 })).rejects.toMatchObject({ reason: "agent_stream_timeout" });
    expect(attempts).toBe(3);
    const controller = new AbortController();
    controller.abort();
    await client.readEvents(
        "run",
        () => {
            throw new Error("unexpected event");
        },
        { signal: controller.signal },
    );
    expect(attempts).toBe(3);
});

test("SDK SSE returns long Retry-After waits without reconnecting early", async () => {
    let attempts = 0;
    const client = createAgentClient({
        retryDelayMs: () => 0,
        transport: {
            async request() {
                throw new Error("unused");
            },
            async openEvents() {
                attempts++;
                return new Response("", { status: 429, headers: { "retry-after": "60" } });
            },
        },
    });
    await expect(client.readEvents("run", () => {})).rejects.toMatchObject({ reason: "http_429", retryAfterMs: 60_000 });
    expect(attempts).toBe(1);
});

test("SDK resumes SSE with durable seq and aborts only its subscription", async () => {
    const cursors: number[] = [];
    const requests: string[] = [];
    const controller = new AbortController();
    const encoder = new TextEncoder();
    const transport: AgentTransport = {
        async request(call) {
            requests.push(call.path);
            return { status: 200, body: { code: 0, data: { accepted: true } } };
        },
        async openEvents(call) {
            cursors.push(Number(call.headers?.["Last-Event-ID"]));
            const seq = cursors.length === 1 ? 4 : 5;
            const event = { eventId: `run:${seq}`, runId: "run/1", seq, type: "reply", payload: {}, createdAt: "" };
            const bytes = encoder.encode(`: heartbeat\r\n\r\nid: ${seq}\r\nevent: agent\r\ndata: ${JSON.stringify(event)}\r\n\r\n`);
            return new Response(
                new ReadableStream({
                    start(stream) {
                        stream.enqueue(bytes.slice(0, 35));
                        stream.enqueue(bytes.slice(35));
                        stream.close();
                    },
                }),
                { headers: { "Content-Type": "text/event-stream" } },
            );
        },
    };
    const client = createAgentClient({ transport, retryDelayMs: () => 0 });
    const seen: number[] = [];
    await client.readEvents(
        "run/1",
        (event) => {
            seen.push(event.seq);
            if (event.seq === 5) controller.abort();
        },
        { after: 3, signal: controller.signal },
    );
    expect(seen).toEqual([4, 5]);
    expect(cursors).toEqual([3, 4]);
    expect(requests).toEqual([]);
    await client.cancelRun("run/1");
    expect(requests).toEqual(["/agent/runs/run%2F1/cancel"]);
});

test("bound SDK scope is frozen into body and query and conflicts fail before transport", async () => {
    const calls: AgentTransportRequest[] = [];
    const transport: AgentTransport = {
        async request(call) {
            calls.push(structuredClone(call));
            return { status: 200, body: { code: 0, data: { run: { id: "run-platform" } } } };
        },
        async openEvents() {
            throw new Error("unused");
        },
    };
    const client = createAgentClient({
        transport,
        scope: { type: "platform", id: "self" },
        agentDefinitionId: "platform.default",
    });
    const explicit = {
        scope: { type: "platform" as const, id: "self" as const },
        prompt: "你好",
        permissionMode: "read_only" as const,
        idempotencyKey: "platform-key-1",
    };
    await client.startRun(explicit);
    expect(calls[0].path).toBe("/agent/runs?scopeType=platform&scopeId=self&agentDefinitionId=platform.default");
    expect(calls[0].body).toMatchObject({ scope: { type: "platform", id: "self" }, agentDefinitionId: "platform.default" });
    await expect(client.startRun({ ...explicit, scope: { type: "drama", id: "p1" } })).rejects.toMatchObject({ reason: "agent_scope_invalid" });
    expect(calls).toHaveLength(1);
});

test("SDK exposes scoped capabilities, complete queue and typed run pagination", async () => {
    const calls: AgentTransportRequest[] = [];
    const client = createAgentClient({
        transport: {
            async request(call) {
                calls.push(call);
                if (call.path.startsWith("/agent/capabilities")) {
                    return { status: 200, body: { code: 0, data: { version: 3, permissionModes: ["read_only"], contextScopes: ["platform"], skills: false, writeTools: false, availability: { configured: true, readyWorkers: 1, queueReason: "", reason: "" } } } };
                }
                if (call.path.includes("/queue/")) return { status: 200, body: { code: 0, data: {} } };
                if (call.path.includes("/queue")) return { status: 200, body: { code: 0, data: { revision: 2, paused: false, items: [] } } };
                if (call.path.startsWith("/agent/ui-settings")) return { status: 200, body: { code: 0, data: { revision: 1, defaultSendMode: "queue" } } };
                return { status: 200, body: { code: 0, data: { run: { id: "run-1" } } } };
            },
            async openEvents() {
                throw new Error("unused");
            },
        },
        scope: { type: "platform", id: "self" },
        agentDefinitionId: "platform.default",
    });
    const capabilities = await client.getCapabilities();
    expect(capabilities.permissionModes).toEqual(["read_only"]);
    await client.updateQueuedMessage("c1", "m1", { revision: 1, prompt: "改" });
    await client.cancelQueuedMessage("c1", "m1", 1);
    await client.updateConversationQueue("c1", { revision: 1, action: "pause" });
    await client.getUISettings();
    await client.updateUISettings({ revision: 1, defaultSendMode: "queue" });
    await client.getRun("r1", undefined, { sinceSeq: 4, eventLimit: 50, fromStart: false });
    expect(calls[0].path).toBe("/agent/capabilities?scopeType=platform&scopeId=self&agentDefinitionId=platform.default");
    expect(calls.at(-1)?.path).toBe("/agent/runs/r1?sinceSeq=4&eventLimit=50&fromStart=false&scopeType=platform&scopeId=self&agentDefinitionId=platform.default");
    expect(calls.some((call) => call.path.includes("/queue/m1?revision=1"))).toBe(true);
});
