import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentChatMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";
import { agentRunErrorMessage } from "@/lib/canvas/agent-error-presentation";
import { canvasThemes } from "@/lib/canvas-theme";
import type { AgentEvent } from "@/services/api/agent";

const event = (type: string, payload: Record<string, unknown>, runId = "ag-timeout-123"): AgentEvent => ({
    type, payload, runId, seq: 1, eventId: `${runId}:1`, createdAt: "2026-10-06T15:00:00Z",
});

test("timeout failure events and restored snapshots retain the same run identity", () => {
    const text = "Agent 超时失败：单步模型调用超过执行时限；本轮已停止。";
    const live = agentRunErrorMessage(event("run_failed", { text, reason: "model_step_timeout" }));
    const restored = agentRunErrorMessage(event("run_status", { status: "failed", failureMessage: text }));
    expect(live).toMatchObject({ role: "error", title: "Agent 超时失败", text, runId: "ag-timeout-123" });
    expect(restored).toEqual(live);
    expect(agentRunErrorMessage(event("run_failed", { text }, "ag-timeout-456")).id).not.toBe(live.id);
});

test("failed run UI shows the run ID and a copy control in both themes", () => {
    const item = { id: "terminal-ag-timeout-123", role: "error" as const, title: "Agent 超时失败", text: "单步模型调用超过执行时限", runId: "ag-timeout-123" };
    for (const theme of [canvasThemes.light, canvasThemes.dark]) {
        const html = renderToStaticMarkup(<AgentChatMessage item={item} theme={theme} />);
        expect(html).toContain("Agent 超时失败");
        expect(html).toContain("Run ID");
        expect(html).toContain("ag-timeout-123");
        expect(html).toContain('aria-label="复制 Run ID"');
    }
});

test("other run failures retain their reason and run ID", () => {
    const item = agentRunErrorMessage(event("run_failed", { text: "工具准入失败", reason: "tool_admission_failed" }));
    expect(item).toMatchObject({ title: "Agent 执行失败", text: "工具准入失败", runId: "ag-timeout-123" });
});
