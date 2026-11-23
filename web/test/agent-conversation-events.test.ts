import { describe, expect, test } from "bun:test";
import { applyAgentConversationEvent, pendingAgentQuestion } from "@/lib/agent/conversation-events";
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

    test("only the queue's current question is actionable", () => {
        const messages = [
            { id: "run-1:44", role: "assistant" as const, text: "", question: { id: "run-1:44", question: "旧问题", options: [{ label: "是" }, { label: "否" }] } },
            { id: "run-2:8", role: "assistant" as const, text: "", question: { id: "run-2:8", question: "新问题", options: [{ label: "演员" }, { label: "摄影机" }] } },
        ];
        expect(pendingAgentQuestion(messages, "run-2:8")?.question).toBe("新问题");
        expect(pendingAgentQuestion(messages, "")).toBeUndefined();
    });
});
