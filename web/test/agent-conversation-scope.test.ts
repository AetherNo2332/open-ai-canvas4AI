import { expect, test } from "bun:test";
import { createAgentClient } from "../src/sdk/agent/client";
import type { AgentTransportRequest } from "../src/sdk/agent/transport";

test("business client carries the same conversation identity to directory, history, events and mutations", async () => {
    const calls: AgentTransportRequest[] = [];
    const client = createAgentClient({
        scope: { type: "drama", id: "project-A" },
        agentDefinitionId: "drama.analysis",
        transport: {
            async request(call) {
                calls.push(call);
                return { status: 200, body: { code: 0, data: { items: [] } } };
            },
            async openEvents(call) {
                calls.push(call);
                return new Response('event: run_snapshot\ndata: {"id":"run-A","status":"completed"}\n\n', { headers: { "content-type": "text/event-stream" } });
            },
        },
    });
    await client.listConversations({ limit: 2 });
    await client.listConversationRuns("conversation-A");
    await client.getRun("run-A");
    await client.readEvents("run-A", () => {});
    await client.getConversationQueue("conversation-A");
    await client.cancelRun("run-A");
    await client.archiveConversation("conversation-A");
    expect(calls).toHaveLength(7);
    for (const call of calls) {
        const query = new URL(call.path, "http://local.invalid").searchParams;
        expect(query.get("scopeType")).toBe("drama");
        expect(query.get("scopeId")).toBe("project-A");
        expect(query.get("agentDefinitionId")).toBe("drama.analysis");
    }
    expect(calls.at(-1)?.method).toBe("DELETE");
});
