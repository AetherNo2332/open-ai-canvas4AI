import { describe, expect, test } from "bun:test";

import { createAgentClient, createMemoryStorage, createSessionController } from "../src/sdk/agent";
import { createFetchTransport } from "../src/sdk/creation-platform";

// 只在 backend/internal/handler 的 TestAgentSDKAgainstRealBackend 启动真实后端时运行；平时 `bun test` 跳过。
const baseUrl = process.env.CANVAS_AGENT_E2E_URL;
const cookieA = process.env.CANVAS_AGENT_E2E_COOKIE_A;
const cookieB = process.env.CANVAS_AGENT_E2E_COOKIE_B;

const describeE2E = baseUrl ? describe : describe.skip;

describeE2E("agent SDK against a real backend", () => {
    const clientFor = (cookie: string) => createAgentClient({ transport: createFetchTransport({ baseUrl: baseUrl!, headers: () => ({ Cookie: cookie }) }) });

    test("work, supervisor Session, messages, budget and stop", async () => {
        const a = clientFor(cookieA!);
        const definitions = await a.definitions();
        expect(definitions.map((d) => d.id)).toContain("supervisor.agent");
        const drama = await a.createSession({ definitionId: "drama.agent", scope: { application: "drama", spaceType: "drama_project", spaceId: "drama-1" }, idempotencyKey: "e2e-drama" });
        const again = await a.createSession({ definitionId: "drama.agent", scope: { application: "drama", spaceType: "drama_project", spaceId: "drama-1" }, idempotencyKey: "e2e-drama" });
        expect(again.id).toBe(drama.id);

        const controller = createSessionController({ client: a, userId: "e2e-a", storage: createMemoryStorage() });
        await controller.open(drama);
        await controller.send(drama.id, "第一集要更紧凑");
        expect(controller.state(drama.id).messages.map((m) => [m.actor, m.text])).toEqual([["user", "第一集要更紧凑"]]);

        const listed = await a.listSessions({ scope: drama.scope });
        expect(listed.sessions.map((s) => s.id)).toEqual([drama.id]);

        // Another user sees nothing and can touch nothing.
        const b = clientFor(cookieB!);
        expect((await b.listSessions()).sessions).toEqual([]);
        await expect(b.getSession(drama.id)).rejects.toMatchObject({ error: { code: "not_found" } });
        await expect(b.sendMessage(drama.id, { text: "越权" })).rejects.toMatchObject({ error: { code: "not_found" } });

        const archived = await a.archiveSession(drama.id, (await a.getSession(drama.id)).revision);
        expect(archived.status).toBe("archived");
    });
});
