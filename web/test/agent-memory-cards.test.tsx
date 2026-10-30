import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { App } from "antd";
import type { AgentEvent } from "@/services/api/agent";
import { applyAgentEvent } from "@/lib/canvas/cloud-agent-events";
import { AgentChatMessage, type CloudAgentChatMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";
import { AGENT_TOOL_NAMES, friendlyAgentToolSummary } from "@/lib/canvas/agent-tool-presentation";
import { canvasThemes } from "@/lib/canvas-theme";

function collect(events: AgentEvent[]) {
    let messages: CloudAgentChatMessage[] = [];
    const setMessages = (next: CloudAgentChatMessage[] | ((current: CloudAgentChatMessage[]) => CloudAgentChatMessage[])) => {
        messages = typeof next === "function" ? next(messages) : next;
    };
    const noop = () => {};
    for (const event of events) applyAgentEvent(event, setMessages as never, noop as never, noop as never);
    return messages;
}

const applied: AgentEvent = {
    eventId: "run:7",
    runId: "run",
    seq: 7,
    type: "memory_updated",
    payload: { status: "applied", revisionId: "rev-1", summary: "新增 1 条：用户说以后都用竖屏", changes: [{ kind: "add", id: "u1", section: "user", after: "偏好竖屏 9:16" }], text: "长期记忆已更新" },
    createdAt: "",
};

test("memory_updated 渲染为记忆卡，重放不重复，成功的工具行不再重复展示", () => {
    const toolDone: AgentEvent = { eventId: "run:8", runId: "run", seq: 8, type: "tool_completed", payload: { toolName: "memory_update", callId: "c1", result: { status: "applied" } }, createdAt: "" };
    const messages = collect([applied, applied, toolDone]);
    expect(messages).toHaveLength(1);
    expect(messages[0].memoryUpdate).toEqual({ revisionId: "rev-1", status: "applied", summary: "新增 1 条：用户说以后都用竖屏", changes: [{ kind: "add", section: "user", before: undefined, after: "偏好竖屏 9:16" }] });
    const html = renderToStaticMarkup(
        <App>
            <AgentChatMessage item={messages[0]} theme={canvasThemes.light} />
        </App>,
    );
    expect(html).toContain("长期记忆已更新");
    expect(html).toContain("新增 · 关于用户");
    expect(html).toContain("偏好竖屏 9:16");
    expect(html).toContain('aria-label="撤销这次记忆修改"');
});

test("待确认的记忆修改不提供撤销，提示去设置里确认", () => {
    const pending: AgentEvent = { ...applied, eventId: "run:9", seq: 9, payload: { ...applied.payload, status: "pending_review", revisionId: "rev-2" } };
    const [message] = collect([pending]);
    expect(message.memoryUpdate?.status).toBe("pending");
    const html = renderToStaticMarkup(
        <App>
            <AgentChatMessage item={message} theme={canvasThemes.dark} />
        </App>,
    );
    expect(html).toContain("Agent 想修改长期记忆，等你确认");
    expect(html).not.toContain("撤销这次记忆修改");
});

test("失败的 memory_update 仍按工具行展示；旧经验工具已退役", () => {
    const failed: AgentEvent = { eventId: "run:10", runId: "run", seq: 10, type: "tool_failed", payload: { toolName: "memory_update", error: "只读模式不能修改长期记忆" }, createdAt: "" };
    expect(collect([failed])).toHaveLength(1);
    expect(friendlyAgentToolSummary("memory_update", "", { eventType: "tool_completed", result: { status: "pending_review" } })).toBe("记忆修改已提交，等你确认");
    expect(friendlyAgentToolSummary("memory_update", "", { eventType: "tool_completed", result: { status: "unchanged" } })).toBe("记忆里已有这些内容，未改动");
    expect(AGENT_TOOL_NAMES).toContain("memory_update");
    expect(AGENT_TOOL_NAMES as readonly string[]).not.toContain("recall_lessons");
    expect(AGENT_TOOL_NAMES as readonly string[]).not.toContain("remember_lesson");
});
