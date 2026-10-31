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

test("SSE generation_settled adds one timeline row and the delivered notice adds none", () => {
    let messages: CloudAgentChatMessage[] = [];
    let run: AgentRun | null = { id: "run", canvasId: "c", status: "running", permissionMode: "auto" } as AgentRun;
    const setMessages = (next: CloudAgentChatMessage[] | ((current: CloudAgentChatMessage[]) => CloudAgentChatMessage[])) => {
        messages = typeof next === "function" ? next(messages) : next;
    };
    const setRun = (next: AgentRun | null | ((current: AgentRun | null) => AgentRun | null)) => {
        run = typeof next === "function" ? next(run) : next;
    };
    const settled: AgentEvent = { eventId: "run:7", runId: "run", seq: 7, type: "generation_settled", payload: { taskId: "t1", nodeId: "n1", mode: "video", status: "failed", summary: "生成未成功", error: "上游拒绝" }, createdAt: "" };
    const delivered: AgentEvent = { eventId: "run:8", runId: "run", seq: 8, type: "generation_notice_delivered", payload: { messageId: "generation:t1", text: "[系统通知] 生成结果：…" }, createdAt: "" };
    for (const item of [settled, settled, delivered]) applyAgentEvent(item, setMessages as never, setRun as never, (() => {}) as never);
    expect(messages).toHaveLength(1);
    expect(messages[0].role).toBe("system");
    expect(messages[0].text).toBe("生成结果 · 视频 生成未成功：上游拒绝");
    expect(run?.status).toBe("running");
});
