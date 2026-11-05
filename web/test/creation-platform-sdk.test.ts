import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { createCreationPlatformClient, PlatformRequestError } from "../src/sdk/creation-platform/client";
import type { GenerationRequest, TaskEvent, TaskSnapshot } from "../src/sdk/creation-platform/contract";
import type { Transport, TransportRequest } from "../src/sdk/creation-platform/transport";

const sdkDir = join(import.meta.dir, "../src/sdk/creation-platform");

const request: GenerationRequest = {
    contractVersion: 1,
    capability: "image",
    model: { channelId: "ch", modelKey: "m" },
    prompt: "白底陶瓷杯",
    business: { application: "ecommerce", objectType: "batch_item", objectId: "item-1" },
    idempotencyKey: "",
};

function snapshot(partial: Partial<TaskSnapshot> = {}): TaskSnapshot {
    return {
        contractVersion: 1,
        taskId: "task-1",
        revision: 1,
        capability: "image",
        execution: "queued",
        stage: "",
        progress: 0,
        resources: "none",
        billing: { status: "reserved", reservedMicrocredits: 100, refundedMicrocredits: 0 },
        actions: ["cancel"],
        business: request.business,
        createdAt: "2026-11-06T00:00:00Z",
        updatedAt: "2026-11-06T00:00:00Z",
        ...partial,
    };
}

type Route = (call: TransportRequest) => unknown;

// 可编程传输：按 "METHOD path" 返回信封或抛出传输失败，记录每次调用。
function fakeTransport(routes: Record<string, Route>) {
    const calls: TransportRequest[] = [];
    const transport: Transport = {
        async request(call) {
            calls.push(call);
            const key = `${call.method} ${call.path.split("?")[0]}`;
            const route = routes[key];
            if (!route) throw new Error(`unexpected ${key}`);
            return route(call);
        },
    };
    return { transport, calls };
}

const ok = <T>(data: T) => ({ status: 200, body: { code: 0, data, msg: "ok" } });

describe("creation platform SDK", () => {
    test("core modules have no browser, React, store or axios dependencies", () => {
        const forbidden = [/from ["']react/, /from ["']axios/, /from ["']antd/, /zustand/, /@\/stores\//, /@\/services\//, /@\/components\//, /\bwindow\./, /\bdocument\./, /localStorage/, /localforage/, /import\.meta\.env/];
        const core = readdirSync(sdkDir).filter((name) => name.endsWith(".ts") && name !== "http-transport.ts");
        expect(core.sort()).toEqual(["client.ts", "contract.ts", "index.ts", "transport.ts"]);
        for (const name of core) {
            const source = readFileSync(join(sdkDir, name), "utf8");
            for (const pattern of forbidden) expect({ name, match: pattern.test(source) }).toEqual({ name, match: false });
        }
    });

    test("submit replays the same idempotency key after a lost response", async () => {
        let attempts = 0;
        const { transport, calls } = fakeTransport({
            "POST /platform/v1/generations": () => {
                attempts += 1;
                if (attempts === 1) throw new TypeError("network connection lost");
                return ok({ snapshot: snapshot(), replayed: true });
            },
        });
        const client = createCreationPlatformClient({ transport, newIdempotencyKey: () => "generated-key", retryDelayMs: () => 0 });
        const receipt = await client.submit(request);
        expect(receipt.snapshot.taskId).toBe("task-1");
        expect(calls).toHaveLength(2);
        const keys = calls.map((call) => (call.body as GenerationRequest).idempotencyKey);
        expect(keys).toEqual(["generated-key", "generated-key"]);
    });

    test("caller-provided idempotency key is kept and invalid requests never reach the transport", async () => {
        const { transport, calls } = fakeTransport({ "POST /platform/v1/generations": () => ok({ snapshot: snapshot(), replayed: false }) });
        const client = createCreationPlatformClient({ transport, newIdempotencyKey: () => "unused" });
        await client.submit({ ...request, idempotencyKey: "batch-7:item-2" });
        expect((calls[0].body as GenerationRequest).idempotencyKey).toBe("batch-7:item-2");
        await expect(client.submit({ ...request, model: { logicalModelId: "x", channelId: "y", modelKey: "z" } as never })).rejects.toMatchObject({ error: { code: "invalid_request" } });
        expect(calls).toHaveLength(1);
    });

    test("HTTP and envelope errors become PlatformError with machine-readable codes", async () => {
        const { transport } = fakeTransport({
            "POST /platform/v1/generations": () => ({ status: 409, body: { code: 409, data: null, msg: "同一幂等键已用于不同内容的请求", reason: "idempotency_conflict", details: { retryable: false, requestId: "req-1" } } }),
            "GET /platform/v1/tasks/task-1": () => ({ status: 200, body: { code: 500, data: null, msg: "boom", reason: "internal" } }),
        });
        const client = createCreationPlatformClient({ transport, retryDelayMs: () => 0 });
        const conflict = await client.submit({ ...request, idempotencyKey: "k" }).catch((error: unknown) => error);
        expect(conflict).toBeInstanceOf(PlatformRequestError);
        expect((conflict as PlatformRequestError).error).toMatchObject({ code: "idempotency_conflict", retryable: false, requestId: "req-1", httpStatus: 409 });
        const internal = await client.task("task-1").catch((error: unknown) => error);
        expect((internal as PlatformRequestError).error.code).toBe("internal");
    });

    test("unknown reason and unknown execution state never look like success", async () => {
        const { transport } = fakeTransport({
            "GET /platform/v1/tasks/task-1": () => ok(snapshot({ execution: "exploded" as never })),
            "POST /platform/v1/quotes": () => ({ status: 400, body: { code: 400, data: null, msg: "?", reason: "something_new" } }),
        });
        const client = createCreationPlatformClient({ transport });
        const unknownState = await client.task("task-1").catch((error: unknown) => error);
        expect((unknownState as PlatformRequestError).error.code).toBe("internal");
        const unknownReason = await client.quote(request).catch((error: unknown) => error);
        expect((unknownReason as PlatformRequestError).error.code).toBe("internal");
    });

    test("actions follow server-allowed actions and do not call the server otherwise", async () => {
        const { transport, calls } = fakeTransport({
            "GET /platform/v1/tasks/task-1": () => ok(snapshot({ execution: "running", actions: [] })),
            "POST /platform/v1/tasks/task-1/cancel": () => ok(snapshot({ execution: "cancelled" })),
        });
        const client = createCreationPlatformClient({ transport });
        const refused = await client.cancel("task-1").catch((error: unknown) => error);
        expect((refused as PlatformRequestError).error.code).toBe("action_not_allowed");
        expect(calls.map((call) => call.path)).toEqual(["/platform/v1/tasks/task-1"]);
    });

    test("waitForTask resumes from the snapshot revision and ignores stale events", async () => {
        let polls = 0;
        const { transport, calls } = fakeTransport({
            "GET /platform/v1/tasks/task-1": () =>
                ok(
                    polls < 1
                        ? snapshot({ revision: 5, execution: "running" })
                        : polls < 2
                          ? snapshot({ revision: 9, execution: "succeeded", resources: "ready", actions: [], billing: { status: "running", reservedMicrocredits: 100, refundedMicrocredits: 0 } })
                          : snapshot({
                                revision: 10,
                                execution: "succeeded",
                                resources: "ready",
                                actions: [],
                                billing: { status: "settled", reservedMicrocredits: 100, chargedMicrocredits: 100, refundedMicrocredits: 0 },
                                result: { resources: [{ resourceId: "res-1", kind: "image" }] },
                            }),
                ),
            "GET /platform/v1/events": (call) => {
                polls += 1;
                const after = new URLSearchParams(call.path.split("?")[1]).get("after");
                const events: TaskEvent[] = after === "5" ? [event(4, "running"), event(9, "succeeded")] : after === "9" ? [event(10, "succeeded")] : [];
                return ok({ events, nextCursor: events.length ? String(events[events.length - 1].id) : (after ?? "0") });
            },
        });
        const seen: number[] = [];
        const client = createCreationPlatformClient({ transport, pollIntervalMs: 0 });
        const done = await client.waitForTask("task-1", { onSnapshot: (s) => seen.push(s.revision) });
        expect(done.execution).toBe("succeeded");
        expect(done.result?.resources?.[0]?.resourceId).toBe("res-1");
        // succeeded 但费用仍在结算时继续等待，直到结算落定。
        expect(seen).toEqual([5, 9, 10]);
        expect(done.billing.status).toBe("settled");
        const eventCall = calls.find((call) => call.path.startsWith("/platform/v1/events"));
        expect(new URLSearchParams(eventCall!.path.split("?")[1]).get("after")).toBe("5");
    });

    test("expired cursor triggers a snapshot resync instead of losing state", async () => {
        let resynced = false;
        const { transport } = fakeTransport({
            "GET /platform/v1/tasks/task-1": () => ok(resynced ? snapshot({ revision: 20, execution: "failed", actions: ["retry"] }) : snapshot({ revision: 3, execution: "running" })),
            "GET /platform/v1/events": () => {
                if (!resynced) {
                    resynced = true;
                    return ok({ events: [], nextCursor: "20", resyncRequired: true });
                }
                return ok({ events: [], nextCursor: "20" });
            },
        });
        const client = createCreationPlatformClient({ transport, pollIntervalMs: 0 });
        const done = await client.waitForTask("task-1");
        expect(done.execution).toBe("failed");
        expect(done.revision).toBe(20);
    });

    test("aborting the wait only stops local waiting and never cancels the server task", async () => {
        const { transport, calls } = fakeTransport({
            "GET /platform/v1/tasks/task-1": () => ok(snapshot({ revision: 1, execution: "running" })),
            "GET /platform/v1/events": () => ok({ events: [], nextCursor: "1" }),
        });
        const controller = new AbortController();
        const client = createCreationPlatformClient({ transport, pollIntervalMs: 5 });
        const waiting = client.waitForTask("task-1", { signal: controller.signal });
        setTimeout(() => controller.abort(), 20);
        const outcome = await waiting.catch((error: unknown) => error);
        expect((outcome as DOMException).name).toBe("AbortError");
        expect(calls.some((call) => call.method === "POST")).toBe(false);
        // 刷新后按任务 ID 仍能找回服务端任务。
        const again = await client.task("task-1");
        expect(again.taskId).toBe("task-1");
    });

    test("pending billing keeps charged unknown and media recovery uses the server action", async () => {
        const { transport, calls } = fakeTransport({
            "GET /platform/v1/tasks/task-1": () => ok(snapshot({ execution: "succeeded", resources: "failed", actions: ["recover_media"], billing: { status: "pending_review", reservedMicrocredits: 100, refundedMicrocredits: 0 } })),
            "POST /platform/v1/tasks/task-1/recover-media": () => ok(snapshot({ execution: "succeeded", resources: "delivering", actions: [] })),
        });
        const client = createCreationPlatformClient({ transport });
        const current = await client.task("task-1");
        expect(current.billing.chargedMicrocredits).toBeUndefined();
        const recovering = await client.recoverMedia("task-1");
        expect(recovering.resources).toBe("delivering");
        expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual(["GET /platform/v1/tasks/task-1", "GET /platform/v1/tasks/task-1", "POST /platform/v1/tasks/task-1/recover-media"]);
    });

    test("resources upload and access go through the injected transport", async () => {
        const { transport, calls } = fakeTransport({
            "POST /resources": (call) => {
                expect(call.body).toBeInstanceOf(FormData);
                return ok({ resource: { id: "res-up", kind: "image", status: "ready", mimeType: "image/png", size: 3, width: 2, height: 3 } });
            },
            "POST /resources/access": () => ok({ items: [{ resourceId: "res-up", access: { url: "/api/resources/res-up/file?sig=x", expiresAt: null } }] }),
        });
        const client = createCreationPlatformClient({ transport });
        const uploaded = await client.uploadResource(new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" }), { kind: "image", fileName: "a.png", idempotencyKey: "up-1" });
        expect(uploaded).toEqual({ resourceId: "res-up", kind: "image", mimeType: "image/png", bytes: 3, width: 2, height: 3 });
        expect(calls[0].headers?.["X-Idempotency-Key"]).toBe("up-1");
        const access = await client.resourceAccess("res-up", "display");
        expect(access.url).toContain("res-up");
    });
});

function event(id: number, execution: TaskEvent["execution"]): TaskEvent {
    return { id, taskId: "task-1", revision: id, type: "task.state_changed", execution, resources: execution === "succeeded" ? "ready" : "none", billing: "running", occurredAt: "2026-11-06T00:00:00Z" };
}
