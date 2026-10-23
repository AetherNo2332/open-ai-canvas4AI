import { afterEach, expect, test } from "bun:test";
import { apiClient, ApiError } from "@/services/api/request";
import { createCanvasEditLeaseAPI, type CanvasEditLease, type CanvasEditLeaseAPI } from "@/services/api/canvas-edit-leases";
import { cancelCanvasEditLeases, captureCanvasEditProofProvider, createCanvasEditLeaseController } from "@/services/canvas-edit-leases";
import { createCanvasCommandTransport } from "@/services/api/canvas-commands";
import type { Command } from "@/lib/canvas/command-contract.generated";

const previousAdapter = apiClient.defaults.adapter;
const controllers: Array<ReturnType<typeof createCanvasEditLeaseController>> = [];
afterEach(() => {
    for (const controller of controllers.splice(0)) controller.close();
    cancelCanvasEditLeases();
    apiClient.defaults.adapter = previousAdapter;
});
const command: Command = { protocolVersion: 1, canvasId: "c", documentEpoch: "e", clientId: "client", operationId: "op", expectedEntityVersions: { "node:n": 1 }, operations: [{ type: "update_node", id: "n", patch: { content: "draft" } }] };
function controller(api: CanvasEditLeaseAPI, extras: Partial<Parameters<typeof createCanvasEditLeaseController>[0]> = {}) {
    const value = createCanvasEditLeaseController({ scope: "owner", canvasId: "c", api, current: () => true, ...extras });
    controllers.push(value);
    return value;
}

test("actual lease API denies another tab, renews one owner, sends only header proof and releases after durable ACK", async () => {
    let now = Date.now(),
        remote: CanvasEditLease | undefined,
        releaseCount = 0,
        commandBody = "",
        header = "";
    let ack!: () => void;
    const durable = new Promise<void>((resolve) => {
        ack = resolve;
    });
    apiClient.defaults.adapter = async (config) => {
        const body = config.data ? JSON.parse(config.data) : undefined;
        let data: unknown;
        if (config.url?.endsWith("/acquire")) {
            if (remote && remote.sessionId !== body.sessionId) throw new ApiError("occupied", { status: 409, reason: "node_locked" });
            remote = { canvasId: "c", ...body, expiresAt: new Date(now + 30000).toISOString() };
            data = remote;
        } else if (config.url?.endsWith("/renew")) {
            remote = { ...remote!, expiresAt: new Date(now + 30000).toISOString() };
            data = remote;
        } else if (config.url?.endsWith("/release")) {
            releaseCount++;
            remote = undefined;
            data = { released: true };
        } else if (config.url?.endsWith("/commands")) {
            commandBody = config.data;
            header = String(config.headers.get("X-Canvas-Edit-Proofs"));
            await durable;
            data = {};
        } else throw new Error(`unexpected ${config.url}`);
        return { data: { code: 0, data, msg: "ok" }, status: 200, statusText: "OK", headers: {}, config };
    };
    const scheduled: Array<() => void> = [];
    const one = controller(createCanvasEditLeaseAPI(), {
        now: () => now,
        schedule: (fn) => {
            scheduled.push(fn);
            return () => {};
        },
    });
    const two = controller(createCanvasEditLeaseAPI(), { now: () => now });
    const detach = one.retain("n");
    await one.begin("n");
    expect(remote?.token).toHaveLength(48);
    await expect(two.begin("n")).rejects.toMatchObject({ reason: "node_locked" });
    expect(two.node("n").phase).toBe("denied");
    now += 10000;
    scheduled[0]();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(Date.parse(remote!.expiresAt)).toBe(now + 30000);
    const transport = createCanvasCommandTransport(undefined, (wire) => one.proofHeaders(wire));
    const save = one.save("n", async () => {
        await transport.execute(command);
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(one.node("n").phase).toBe("saving");
    expect(releaseCount).toBe(0);
    expect(JSON.parse(header)[0]).toMatchObject({ canvasId: "c", nodeId: "n" });
    expect(commandBody).not.toContain(remote!.token);
    ack();
    await save;
    expect(releaseCount).toBe(1);
    expect(one.node("n").phase).toBe("saved");
    detach();
});

test("lost acquire ACK retries original token; expired retry refreshes proof without changing command wire", async () => {
    let now = 100000,
        first = true;
    const tokens: string[] = [],
        headers: string[] = [];
    const api = {
        acquire: async (proof) => {
            tokens.push(proof.token);
            if (first) {
                first = false;
                throw new Error("lost ACK");
            }
            return { ...proof, expiresAt: new Date(now + 30000).toISOString() };
        },
        renew: async (proof) => ({ ...proof, expiresAt: new Date(now + 30000).toISOString() }),
        release: async () => ({ released: true }),
        status: async () => ({ revision: 1, documentEpoch: "e", eventSeq: 1, leases: [], activeAgents: [] }),
    } satisfies CanvasEditLeaseAPI;
    const own = controller(api, { now: () => now });
    await expect(own.begin("n")).rejects.toThrow("lost ACK");
    await own.begin("n");
    expect(tokens[0]).toBe(tokens[1]);
    const body = JSON.stringify(command);
    headers.push((await own.proofHeaders(command))["X-Canvas-Edit-Proofs"]!);
    now += 31000;
    headers.push((await own.proofHeaders(command))["X-Canvas-Edit-Proofs"]!);
    expect(tokens[2]).not.toBe(tokens[1]);
    expect(headers[1]).not.toBe(headers[0]);
    expect(JSON.stringify(command)).toBe(body);
    expect(JSON.stringify(own.getState())).not.toContain(tokens[2]);
});

test("one canvas status poll preserves multiple agents and canceled scope cannot supply proofs", async () => {
    let reads = 0;
    const scheduled: Array<() => void> = [];
    const own = controller(
        {
            acquire: async (proof) => ({ ...proof, expiresAt: new Date(Date.now() + 30000).toISOString() }),
            renew: async (proof) => ({ ...proof, expiresAt: new Date(Date.now() + 30000).toISOString() }),
            release: async () => ({ released: true }),
            status: async () => {
                reads++;
                return { revision: 1, documentEpoch: "e", eventSeq: 1, leases: [], activeAgents: ["a", "b"].map((id) => ({ id, conversationId: id, title: id, status: "running", updatedAt: "now" })) };
            },
        },
        {
            schedule: (fn) => {
                scheduled.push(fn);
                return () => {};
            },
        },
    );
    const unwatch1 = own.watchStatus(),
        unwatch2 = own.watchStatus();
    await own.refreshStatus();
    expect(reads).toBe(1);
    expect(own.getState().activity?.activeAgents).toHaveLength(2);
    unwatch1();
    unwatch2();
    const proof = captureCanvasEditProofProvider();
    cancelCanvasEditLeases();
    await expect(proof(command)).rejects.toMatchObject({ name: "AbortError" });
});

test("late renewal ACK cannot restore a lease or restart renewal after saved release", async () => {
    let finishSave!: () => void,
        finishRenew!: () => void,
        acquired = 0;
    const pendingSave = new Promise<void>((resolve) => {
        finishSave = resolve;
    });
    const pendingRenew = new Promise<void>((resolve) => {
        finishRenew = resolve;
    });
    const scheduled: Array<() => void> = [];
    const own = controller(
        {
            acquire: async (proof) => {
                acquired++;
                return { ...proof, expiresAt: new Date(Date.now() + 30000).toISOString() };
            },
            renew: async (proof) => {
                await pendingRenew;
                return { ...proof, expiresAt: new Date(Date.now() + 30000).toISOString() };
            },
            release: async () => ({ released: true }),
            status: async () => ({ revision: 1, documentEpoch: "e", eventSeq: 1, leases: [], activeAgents: [] }),
        },
        {
            schedule: (fn) => {
                scheduled.push(fn);
                return () => {};
            },
        },
    );
    const detach = own.retain("n");
    await own.begin("n");
    const saved = own.save("n", () => pendingSave);
    await new Promise((resolve) => setTimeout(resolve, 0));
    scheduled[0]();
    await new Promise((resolve) => setTimeout(resolve, 0));
    finishSave();
    await saved;
    const count = scheduled.length;
    finishRenew();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(own.node("n").phase).toBe("saved");
    expect(await own.proofHeaders(command)).toEqual({});
    expect(scheduled).toHaveLength(count);
    expect(acquired).toBe(1);
    detach();
});
