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

    test("keeps the rich canvas event feed in the shared conversation state", () => {
        const base = {
            messages: [],
            run: {
                id: "run-rich",
                canvasId: "canvas-1",
                status: "running" as const,
                permissionMode: "read_only" as const,
                createdAt: "2026-11-24T00:00:00Z",
                updatedAt: "2026-11-24T00:00:00Z",
            },
            approval: null,
        };
        const event = (seq: number, type: string, payload: Record<string, unknown>): AgentEvent => ({
            eventId: `event-${seq}`,
            runId: "run-rich",
            seq,
            type,
            payload,
            createdAt: "2026-11-24T00:00:00Z",
        });

        let view = applyAgentConversationEvent(base, event(1, "tool_pending", { callId: "call-1", toolName: "director" }));
        view = applyAgentConversationEvent(view, event(2, "tool_completed", { callId: "call-1", toolName: "director", text: "已读取世界模型" }));
        expect(view.messages.filter((item) => item.id === "tool-run-rich-call-1")).toHaveLength(1);
        expect(view.messages[0]?.text).toContain("已读取世界模型");

        view = applyAgentConversationEvent(view, event(3, "media_batch_progress", { batchId: "batch-1", phase: "running", total: 2, items: [{ index: 0, nodeId: "image-1", status: "submitted" }] }));
        view = applyAgentConversationEvent(view, event(4, "media_batch_progress", { batchId: "batch-1", phase: "running", delta: true, items: [{ index: 1, nodeId: "image-2", status: "succeeded" }] }));
        expect(view.messages.find((item) => item.id === "media-batch-run-rich-batch-1")?.mediaBatch?.items).toHaveLength(2);

        view = applyAgentConversationEvent(view, event(5, "context_compaction_requested", { turnCount: 3, pressureRatio: 0.91 }));
        expect(view.messages.some((item) => item.id === "run-rich:context-compaction:3" && item.streaming === true)).toBe(true);
        view = applyAgentConversationEvent(view, event(6, "context_compacted", { compactedTurnCount: 3, historyMessages: 8 }));
        expect(view.messages.find((item) => item.id === "run-rich:context-compaction:3")?.streaming).toBe(false);

        view = applyAgentConversationEvent(view, event(7, "permission_changed", { from: "read_only", to: "auto" }));
        expect(view.messages.some((item) => item.text.includes("执行权限已从"))).toBe(true);
        expect(view.run?.permissionMode).toBe("auto");

        view = applyAgentConversationEvent(view, event(8, "memory_updated", { revisionId: "rev-1", status: "pending_review", summary: "记住用户偏好", changes: [{ kind: "add", section: "user", after: "偏好冷色" }] }));
        expect(view.messages.find((item) => item.id === "memory-rev-1")?.memoryUpdate?.status).toBe("pending");
    });
});
