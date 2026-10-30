import { expect, test } from "bun:test";
import type { AgentEvent, AgentRun } from "@/services/api/agent";
import { applyAgentEvent } from "@/lib/canvas/cloud-agent-events";
import type { CloudAgentChatMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";

test("SSE permission_changed updates the run mode and adds one timeline row on replay", () => {
    let messages: CloudAgentChatMessage[] = [];
    let run: AgentRun | null = { id: "run", canvasId: "c", status: "running", permissionMode: "read_only" } as AgentRun;
    const setMessages = (next: CloudAgentChatMessage[] | ((current: CloudAgentChatMessage[]) => CloudAgentChatMessage[])) => {
        messages = typeof next === "function" ? next(messages) : next;
    };
    const setRun = (next: AgentRun | null | ((current: AgentRun | null) => AgentRun | null)) => {
        run = typeof next === "function" ? next(run) : next;
    };
    const event: AgentEvent = { eventId: "run:4", runId: "run", seq: 4, type: "permission_changed", payload: { from: "read_only", to: "auto", requestId: "r" }, createdAt: "" };
    for (const item of [event, event]) applyAgentEvent(item, setMessages as never, setRun as never, (() => {}) as never);
    expect(run?.permissionMode).toBe("auto");
    expect(messages).toHaveLength(1);
    expect(messages[0].role).toBe("system");
    expect(messages[0].text).toBe("执行权限已从「只读」改为「自动执行」，从下一次工具调用起生效");
});
