import { describe, expect, test } from "bun:test";
import { applyAgentConversationEvent } from "@/lib/agent/conversation-events";
import type { AgentEvent } from "@/sdk/agent";

describe("Agent conversation event projection", () => {
    test("uses the server queue question identity when rendering a user question", () => {
        const event: AgentEvent = {
            eventId: "run-1:44",
            runId: "run-1",
            seq: 44,
            type: "user_question",
            payload: {
                question: "选哪个对象？",
                options: [{ label: "演员" }, { label: "摄影机" }],
            },
            createdAt: "2026-11-23T00:00:00Z",
        };
        const view = applyAgentConversationEvent({ messages: [], run: null, approval: null }, event);
        expect(view.messages[0]?.question?.id).toBe("run-1:44");
    });
});
