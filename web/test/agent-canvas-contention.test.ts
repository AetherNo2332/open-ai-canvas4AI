import { expect, test } from "bun:test";
import type { AgentEvent } from "@/services/api/agent";
import { applyAgentEvent } from "@/lib/canvas/cloud-agent-events";
import type { CloudAgentChatMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";

function collect(events: AgentEvent[]) {
    let messages: CloudAgentChatMessage[] = [];
    const setMessages = (next: CloudAgentChatMessage[] | ((current: CloudAgentChatMessage[]) => CloudAgentChatMessage[])) => {
        messages = typeof next === "function" ? next(messages) : next;
    };
    const noop = () => {};
    for (const event of events) applyAgentEvent(event, setMessages as never, noop as never, noop as never);
    return messages;
}

test("canvas_contention 显示为系统消息，不出错误卡，重放不重复", () => {
    const event: AgentEvent = { eventId: "run:9", runId: "run", seq: 9, type: "canvas_contention", payload: { toolName: "canvas_apply_ops", attempts: 5 }, createdAt: "" };
    const messages = collect([event, event]);
    expect(messages).toHaveLength(1);
    expect(messages[0].role).toBe("system");
    expect(messages[0].text).toBe("画布一直在变化，Agent 已暂停等你");
});
