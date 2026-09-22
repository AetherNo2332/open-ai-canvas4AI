import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentProgressChip, agentAssistantFinality, agentControlMessage } from "@/components/canvas/canvas-cloud-agent-chat-ui";
import { friendlyAgentToolSummary } from "@/lib/canvas/agent-tool-presentation";
import { canvasThemes } from "@/lib/canvas-theme";

// 工作项 A：收尾闸门在界面上的语义。
// 验收点：① 被拦下的候选收尾正文不能显示成最终答复；② 运行时控制消息不能显示成真人 user 消息；
// ③ 用户能看到"为什么它又继续跑了"。
test("过程说明与最终答复在界面上分开", () => {
    // 服务端的 final 标记：false = 过程说明（本轮还没收尾，或这次收尾被拦下了）
    expect(agentAssistantFinality({ final: false })).toBe(false);
    expect(agentAssistantFinality({ final: true })).toBe(true);
    // 升级前的后端事件没有这个字段：那时所有正文都按正文展示，缺省必须保持同一口径
    expect(agentAssistantFinality({ text: "旧事件" })).toBe(true);

    const chip = renderToStaticMarkup(<AgentProgressChip theme={canvasThemes.dark} />);
    expect(chip).toContain("过程说明");
});

test("运行时控制消息是 system 行，不是用户气泡", () => {
    const message = agentControlMessage("completion-blocked-ag1:7", "待办清单还有未完成的项（生成镜头1）；已要求 Agent 先对账再收尾。", "第 1/2 次");
    expect(message.role).toBe("system");
    expect(message.text).toContain("生成镜头1");
    expect(message.meta).toBe("第 1/2 次");
    // 时间线里没有第二种"控制行"表示法：必须是 system（user 会被当成真人发言）
    expect(message.role === "user").toBe(false);
});

test("面板把闸门事件接进时间线，并把 final 标记传给渲染层", async () => {
    const panel = await Bun.file(new URL("../src/components/canvas/canvas-cloud-agent-panel.tsx", import.meta.url)).text();

    // 控制事件要有处理分支，且走 agentControlMessage（→ system 行）
    expect(panel).toContain('event.type === "completion_blocked"');
    expect(panel).toContain("agentControlMessage(");
    // 正文的收尾语义来自服务端标记，不在前端猜
    expect(panel).toContain("agentAssistantFinality(payload)");
    // 流式快照先建气泡时 final 还是 undefined：结论事件必须能把标记补上，
    // 所以 upsertTextMessage 的"内容没变"短路要一起比较 final。
    expect(panel).toContain("current[index].final === final");
});

test("聊天渲染层给非最终正文加过程说明标记", async () => {
    const chat = await Bun.file(new URL("../src/components/canvas/canvas-cloud-agent-chat-ui.tsx", import.meta.url)).text();
    expect(chat).toContain("item.final === false ? <AgentProgressChip theme={theme} /> : null");
    // 收尾终止原因（run_failed + completion_blocked）仍走既有的错误行，不另开一套状态展示
    expect(chat).toContain("agentControlMessage");
});

test("finish_run 的工具卡说清这次收尾是否被拦下", () => {
    const blocked = friendlyAgentToolSummary("finish_run", "工具执行成功", { eventType: "tool_completed", result: { completionBlocked: true } });
    expect(blocked).toBe("收尾被拦下，继续处理未完成项");
    const accepted = friendlyAgentToolSummary("finish_run", "工具执行成功", { eventType: "tool_completed", result: { completionBlocked: false } });
    expect(accepted).toBe("已收尾并给出最终答复");
    expect(friendlyAgentToolSummary("finish_run", "", undefined, true)).toBe("准备收尾并给出最终答复");
});
