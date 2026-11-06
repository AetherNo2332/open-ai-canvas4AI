import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

import { createAgentClient, AgentRequestError } from "../src/sdk/agent/client";
import { createSessionController } from "../src/sdk/agent/controller";
import { agentStorageKey, createMemoryStorage } from "../src/sdk/agent/storage";
import type { SessionMessage, SessionView } from "../src/sdk/agent/types";
import type { Transport, TransportRequest } from "../src/sdk/creation-platform/transport";

const sdkDir = join(import.meta.dir, "../src/sdk/agent");
const ok = <T>(data: T) => ({ status: 200, body: { code: 0, data, msg: "ok" } });

function session(id: string, application = "drama", spaceId = "p1"): SessionView {
    return {
        id,
        definitionId: application + ".agent",
        definitionVersion: "1",
        role: "business",
        scope: { application, spaceType: application === "drama" ? "drama_project" : "workbench_batch", spaceId },
        title: id,
        status: "active",
        revision: 1,
        createdAt: "2026-11-06T00:00:00Z",
        updatedAt: "2026-11-06T00:00:00Z",
    };
}

function message(sessionId: string, sequence: number, text: string): SessionMessage {
    return { sessionId, messageId: `${sessionId}-${sequence}`, sequence, kind: "message", actor: "user", role: "user", text, createdAt: "2026-11-06T00:00:00Z" };
}

type Route = (call: TransportRequest) => unknown | Promise<unknown>;

function fakeTransport(routes: Record<string, Route>) {
    const calls: TransportRequest[] = [];
    const transport: Transport = {
        async request(call) {
            calls.push(call);
            const key = `${call.method} ${call.path.split("?")[0]}`;
            const route = routes[key];
            if (!route) throw new Error(`unexpected ${key}`);
            return (await route(call)) as never;
        },
    };
    return { transport, calls };
}

describe("agent SDK", () => {
    test("agent-sdk-no-ui-dependencies: core modules import no browser, React or app modules", () => {
        const forbidden = [/from ["']react/, /from ["']axios/, /from ["']antd/, /zustand/, /@\/stores\//, /@\/services\//, /@\/components\//, /\bwindow\./, /\bdocument\./, /localStorage/, /localforage/, /import\.meta\.env/];
        const files = readdirSync(sdkDir).filter((name) => name.endsWith(".ts"));
        expect(files.sort()).toEqual(["client.ts", "controller.ts", "events.ts", "index.ts", "storage.ts", "types.ts"]);
        for (const name of files) {
            const source = readFileSync(join(sdkDir, name), "utf8");
            for (const pattern of forbidden) expect({ name, match: pattern.test(source) }).toEqual({ name, match: false });
        }
    });

    test("client sends the message ID and never an actor kind; errors keep their code", async () => {
        const { transport, calls } = fakeTransport({
            "POST /agent/v2/sessions/s1/messages": () => ok({ accepted: true, workItemId: "w1", workVersion: 2 }),
            "GET /agent/v2/sessions/s2": () => ({ status: 404, body: { code: 404, data: null, msg: "会话不存在", reason: "not_found", details: { retryable: false } } }),
        });
        const client = createAgentClient({ transport, newMessageId: () => "m-fixed" });
        const receipt = await client.sendMessage("s1", { text: "镜头再暗一点" });
        expect(receipt).toEqual({ accepted: true, workItemId: "w1", workVersion: 2 });
        expect(calls[0]?.body).toEqual({ messageId: "m-fixed", text: "镜头再暗一点" });
        expect(JSON.stringify(calls[0]?.body)).not.toContain("actor");
        const error = await client.getSession("s2").catch((caught: unknown) => caught);
        expect(error).toBeInstanceOf(AgentRequestError);
        expect((error as AgentRequestError).error.code).toBe("not_found");
    });

    test("agent-controller-scope-switch-race: late events update only their own Session", async () => {
        const pending: Record<string, (value: unknown) => void> = {};
        const { transport } = fakeTransport({
            "GET /agent/v2/sessions/s1/messages": () => new Promise((resolve) => (pending.s1 = resolve)),
            "GET /agent/v2/sessions/s2/messages": () => new Promise((resolve) => (pending.s2 = resolve)),
            "GET /agent/v2/sessions/s3/messages": () => ok({ messages: [message("s3", 1, "ecommerce")], nextAfter: 1 }),
        });
        const controller = createSessionController({ client: createAgentClient({ transport }), userId: "u1", storage: createMemoryStorage() });
        controller.open(session("supervisor-like-s1"));
        const a = session("s1"),
            b = session("s2", "drama", "p2"),
            c = session("s3", "ecommerce", "batch_1");
        const loadA = controller.open(a);
        const loadB = controller.open(b);
        await controller.open(c);
        expect(controller.active()?.id).toBe("s3");
        // The responses for A and B arrive after the user moved on to C.
        pending.s2?.(ok({ messages: [message("s2", 1, "B 的回复")], nextAfter: 1 }));
        pending.s1?.(ok({ messages: [message("s1", 1, "A 的回复")], nextAfter: 1 }));
        await Promise.all([loadA, loadB]);
        expect(controller.state("s1").messages.map((m) => m.text)).toEqual(["A 的回复"]);
        expect(controller.state("s2").messages.map((m) => m.text)).toEqual(["B 的回复"]);
        expect(controller.state("s3").messages.map((m) => m.text)).toEqual(["ecommerce"]);
        expect(controller.active()?.id).toBe("s3");
    });

    test("drafts and cursors are partitioned by user + scope + Session", () => {
        const storage = createMemoryStorage();
        const a = session("s1");
        const sameIdOtherScope = { ...session("s1"), scope: { application: "ecommerce", spaceType: "workbench_batch", spaceId: "p1" } };
        expect(agentStorageKey("u1", a, "draft")).not.toBe(agentStorageKey("u2", a, "draft"));
        expect(agentStorageKey("u1", a, "draft")).not.toBe(agentStorageKey("u1", sameIdOtherScope, "draft"));
        const { transport } = fakeTransport({});
        const forU1 = createSessionController({ client: createAgentClient({ transport }), userId: "u1", storage });
        forU1.setDraft(a, "未发送的草稿");
        const forU2 = createSessionController({ client: createAgentClient({ transport }), userId: "u2", storage });
        expect(forU2.draft(a)).toBe("");
        expect(forU1.draft(a)).toBe("未发送的草稿");
        expect(forU1.draft(sameIdOtherScope)).toBe("");
    });

    test("an open Session subscription incrementally reads replies and stops with the panel", async () => {
        let page = 0;
        let poll: (() => Promise<void>) | undefined;
        let stopped = false;
        let interval = 0;
        const { transport } = fakeTransport({
            "GET /agent/v2/sessions/s1/messages": () => {
                page++;
                return page === 1 ? ok({ messages: [message("s1", 1, "用户消息")], nextAfter: 1 }) : ok({ messages: [{ ...message("s1", 2, "Agent 回复"), actor: "executor", role: "assistant" }], nextAfter: 2 });
            },
        });
        const controller = createSessionController({
            client: createAgentClient({ transport }),
            userId: "u1",
            storage: createMemoryStorage(),
            schedulePoll: (next, intervalMs) => {
                poll = next;
                interval = intervalMs;
                return () => {
                    stopped = true;
                };
            },
        });
        await controller.open(session("s1"));
        const unsubscribe = controller.subscribe("s1", 1_250);
        expect(interval).toBe(1_250);
        await poll?.();
        expect(controller.state("s1").messages.map((item) => item.text)).toEqual(["用户消息", "Agent 回复"]);
        unsubscribe();
        expect(stopped).toBe(true);
    });

    test("agent-controller-cursor-recovery: a reloaded page shows the full history, then resumes by sequence without duplicates", async () => {
        const storage = createMemoryStorage();
        const afters: string[] = [];
        const history = [message("s1", 1, "一"), message("s1", 2, "二"), message("s1", 3, "三")];
        const { transport } = fakeTransport({
            "GET /agent/v2/sessions/s1/messages": (call) => {
                const after = Number(new URL("http://x" + call.path).searchParams.get("after") ?? "0");
                afters.push(String(after));
                const page = history.filter((m) => m.sequence > after);
                return ok({ messages: page, nextAfter: page.at(-1)?.sequence ?? after });
            },
        });
        const first = createSessionController({ client: createAgentClient({ transport }), userId: "u1", storage });
        await first.open(session("s1"));
        expect(first.state("s1").messages.map((m) => m.text)).toEqual(["一", "二", "三"]);
        history.push(message("s1", 4, "四"));
        // A page reload: a new controller holds no messages, so it must read the
        // whole history instead of resuming after a cursor left by the old page.
        const second = createSessionController({ client: createAgentClient({ transport }), userId: "u1", storage });
        await second.open(session("s1"));
        expect(second.state("s1").messages.map((m) => m.text)).toEqual(["一", "二", "三", "四"]);
        // Later reads resume after the last sequence it holds; redelivery never duplicates.
        await second.refresh("s1");
        await second.refresh("s1");
        expect(afters).toEqual(["0", "0", "4", "4"]);
        expect(second.state("s1").messages.map((m) => m.sequence)).toEqual([1, 2, 3, 4]);
    });

    test("agent-cost-and-approval-shared-view: one approval ID across views, deciding twice sends one decision per view", async () => {
        const decisions: unknown[] = [];
        const approval = { id: "ap-1", workItemId: "child", sessionId: "s1", summary: "生成 4 张分镜图", quoteMicrocredits: 100_000, authorizationVersion: 3, status: "pending" };
        const { transport } = fakeTransport({
            "GET /agent/v2/work/root/approvals": () => ok({ approvals: [approval] }),
            "GET /agent/v2/work/child/approvals": () => ok({ approvals: [approval] }),
            "POST /agent/v2/approvals/ap-1/decision": (call) => {
                decisions.push(call.body);
                return ok({ ...approval, status: "approved" });
            },
            "GET /platform/v1/budget-grants/g1": () =>
                ok({
                    id: "g1",
                    rootId: "g0",
                    limitMicrocredits: 600_000,
                    availableMicrocredits: 350_000,
                    usage: { chargedMicrocredits: 150_000, inFlightMicrocredits: 50_000, pendingReviewMicrocredits: 50_000, refundedMicrocredits: 20_000 },
                    status: "open",
                    version: 1,
                }),
        });
        const client = createAgentClient({ transport });
        const [supervisorView, businessView] = await Promise.all([client.workApprovals("root"), client.workApprovals("child")]);
        expect(supervisorView[0]?.id).toBe(businessView[0]?.id);
        await client.decideApproval("ap-1", "approved", 3);
        await client.decideApproval("ap-1", "approved", 3);
        expect(decisions).toEqual([
            { decision: "approved", expectedAuthorizationVersion: 3 },
            { decision: "approved", expectedAuthorizationVersion: 3 },
        ]);
        const cost = await client.budget("g1");
        expect(cost).toEqual({ limit: 600_000, available: 350_000, charged: 150_000, inFlight: 50_000, pendingReview: 50_000, refunded: 20_000, status: "open" });
    });
});
