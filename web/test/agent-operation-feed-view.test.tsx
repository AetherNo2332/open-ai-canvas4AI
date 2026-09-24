import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentOperationFeed, type CloudAgentChatMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";
import { canvasThemes } from "@/lib/canvas-theme";

const step = (id: string, title: string, text: string, detail: unknown = { eventType: "tool_completed" }): CloudAgentChatMessage => ({ id, role: "tool", title, text, detail });

const steps: CloudAgentChatMessage[] = [step("t1", "canvas_get_state", "已读取当前画布"), step("t2", "model_list", "已获取可用模型")];

test("operations fold into one line that reports only the latest action", () => {
    for (const theme of [canvasThemes.light, canvasThemes.dark]) {
        const html = renderToStaticMarkup(<AgentOperationFeed items={steps} theme={theme} />);
        expect(html).toContain("agent-operation-feed");
        expect(html).toContain('aria-expanded="false"');
        expect(html).toContain("已获取可用模型");
        expect(html).not.toContain("已读取当前画布");
        expect(html).not.toContain("agent-operation-list");
        expect(html).toContain("2 步");
    }
});

test("a single step keeps the line clean without a step counter", () => {
    const html = renderToStaticMarkup(<AgentOperationFeed items={[steps[0]]} theme={canvasThemes.light} />);
    expect(html).toContain("已读取当前画布");
    // 只有一步时不加计数徽标；无障碍名称里仍会说明步数与最新一步。
    expect(html).not.toContain('class="agent-operation-count"');
    expect(html).toContain('aria-label="展开 1 步操作记录，最新一步：已读取当前画布"');
});

test("a failed step is marked and expanded instead of hidden behind the folded line", () => {
    const failed = [...steps, step("t3", "canvas_apply_ops", "", { eventType: "tool_failed", result: { taskId: "task-1" } })];
    const html = renderToStaticMarkup(<AgentOperationFeed items={failed} theme={canvasThemes.light} />);
    expect(html).toContain("is-failed");
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain("更新画布内容失败");
    expect(html).toContain("agent-operation-list");
    // 展开的完整记录仍然是原样的工具卡（含任务 ID 与历史步骤）。
    expect(html).toContain("已读取当前画布");
    expect(html).toContain("任务 ID：task-1");
});
