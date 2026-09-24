import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentOperationFeed, type CloudAgentChatMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";
import { canvasThemes } from "@/lib/canvas-theme";

const step = (id: string, title: string, text: string, detail: unknown = { eventType: "tool_completed" }): CloudAgentChatMessage => ({ id, role: "tool", title, text, detail });

const steps: CloudAgentChatMessage[] = [step("t1", "canvas_get_state", "工具执行成功"), step("t2", "model_list", "工具执行成功")];
const viewed = (id: string, title: string): CloudAgentChatMessage => step(id, "canvas_inspect_image", "工具执行成功", { eventType: "tool_completed", result: { nodeId: id, title } });

test("operations fold into one line that reports only the latest action", () => {
    for (const theme of [canvasThemes.light, canvasThemes.dark]) {
        const html = renderToStaticMarkup(<AgentOperationFeed items={steps} theme={theme} />);
        expect(html).toContain("agent-operation-feed");
        expect(html).toContain('aria-expanded="false"');
        expect(html).toContain("已获取可用模型");
        expect(html).not.toContain("已读取画布清单");
        expect(html).not.toContain("agent-operation-list");
        expect(html).toContain("2 步");
        expect(html).toContain("读取信息");
    }
});

test("the folded line names the tier it is reporting", () => {
    const read = renderToStaticMarkup(<AgentOperationFeed items={[steps[0]]} theme={canvasThemes.light} />);
    expect(read).toContain("读取清单");
    expect(read).toContain('data-agent-category="read"');
    expect(read).not.toContain("操作画布");
    expect(read).toContain('aria-label="展开 1 步读取清单记录，最新一步：已读取画布清单（未查看画面）"');
    // 只有一步时不加计数徽标
    expect(read).not.toContain('class="agent-operation-count"');

    const vision = renderToStaticMarkup(<AgentOperationFeed items={[viewed("n1", "剧照1.png"), viewed("n2", "封面.png")]} theme={canvasThemes.light} />);
    expect(vision).toContain("查看画面");
    expect(vision).toContain('data-agent-category="vision"');
    expect(vision).toContain("查看了 2 张画面 · 最新《封面.png》");
    expect(vision).not.toContain("操作已完成");

    const operate = renderToStaticMarkup(<AgentOperationFeed items={[step("t9", "canvas_apply_ops", "工具执行成功", { eventType: "canvas_updated" })]} theme={canvasThemes.light} />);
    expect(operate).toContain("修改画布");
    expect(operate).toContain('data-agent-category="operate"');
});

test("a failed step is marked and expanded instead of hidden behind the folded line", () => {
    const failed = [...steps, step("t3", "canvas_apply_ops", "", { eventType: "tool_failed", result: { taskId: "task-1" } })];
    const html = renderToStaticMarkup(<AgentOperationFeed items={failed} theme={canvasThemes.light} />);
    expect(html).toContain("is-failed");
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain("更新画布内容失败");
    expect(html).toContain("agent-operation-list");
    // 展开的完整记录仍然是原样的工具卡（含任务 ID 与历史步骤），失败那步的文字是红的
    expect(html).toContain("已读取画布清单（未查看画面）");
    expect(html).toContain("任务 ID：task-1");
    expect(html).toContain("#dc2626");
});

test("the shimmer only runs while the panel says the segment is live", () => {
    const idle = renderToStaticMarkup(<AgentOperationFeed items={steps} theme={canvasThemes.light} />);
    const running = renderToStaticMarkup(<AgentOperationFeed items={steps} theme={canvasThemes.light} live />);
    expect(idle).not.toContain("is-live");
    expect(running).toContain("is-live");
    // 失败的那一段即使是末尾也不流光：红色静态文字承担语义
    const failedLive = renderToStaticMarkup(<AgentOperationFeed items={[step("t3", "canvas_apply_ops", "", { eventType: "tool_failed" })]} theme={canvasThemes.light} live />);
    expect(failedLive).not.toContain("is-live");
});

test("style contract: no per-step status ticks, vision tint and shimmer stay token-driven", async () => {
    const css = await Bun.file(new URL("../src/components/canvas/canvas-cloud-agent.css", import.meta.url)).text();
    // 展开后的每一步不再渲染状态勾/叉（一列绿勾会把记录读成成绩单）
    expect(css).toContain(".agent-operation-list .agent-tool-status {");
    // 流光动画只挂在 .is-live 上：任务完成即停
    expect(css).toContain(".agent-operation-feed.is-live .agent-operation-latest {");
    // 类别取色走面板注入的主题 token，不写字面值
    expect(css).toContain("--agent-tool-accent: var(--agent-accent, var(--foreground));");
});

test("style contract: 正文 / 工具调用 / 模型思考 三档靠公共轴与明度分层", async () => {
    const css = await Bun.file(new URL("../src/components/canvas/canvas-cloud-agent.css", import.meta.url)).text();
    // 同一选择器可能在容器查询里被覆盖，这里把所有命中块拼起来看整体契约。
    const block = (selector: string) => [...css.matchAll(new RegExp(`${selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")} \\{([^}]*)\\}`, "g"))].map((match) => match[1]).join("\n");

    // ① 星标后面不再有圆底：只留图标本身
    const icon = block(".agent-reasoning-icon");
    expect(icon).toContain("background: transparent");
    expect(icon).not.toContain("border-radius: 50%");

    // ② 公共轴：正文就从这条线开始（列表左内边距 = 轴）
    const axis = block(".agent-conversation-messages");
    expect(axis).toContain("--agent-axis: 22px");
    expect(axis).toContain("padding-left: var(--agent-axis)");

    // ③ 过程标记挂在轴上：星标向左挂半格 + 摘要内边距，中心正好落在轴
    const reasoning = block(".agent-reasoning");
    expect(reasoning).toContain("margin-left: calc(-1 * (var(--agent-thought-icon, 16px) / 2 + var(--agent-summary-inset, 4px)))");
    expect(reasoning).not.toContain("border-left");
    expect(block(".agent-reasoning-summary")).toContain("padding: 6px var(--agent-summary-inset");

    // ④ 工具左轨也压在轴上（1px 线居中），内容落在轨右侧
    const feed = block(".agent-operation-feed");
    expect(feed).toContain("margin-left: -0.5px");
    expect(feed).toContain("border-left: 1px solid");
    expect(feed).toContain("padding-left: var(--agent-activity-inset");

    // ⑤ 时间线标记同样挂半格，中心与星标/左轨同轴
    expect(block(".agent-timeline-marker")).toContain("width: var(--agent-timeline-marker, 24px)");
    expect(block(".agent-conversation-messages .agent-timeline-marker")).toContain("margin-left: calc(-1 * (var(--agent-timeline-marker, 24px) / 2))");

    // ⑥ 窄面板只把轴收细，不让三处错位
    expect(css).toContain("--agent-axis: 16px");
});
