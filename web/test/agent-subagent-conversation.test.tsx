import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentSubagentConversationView, subagentConversationMessages } from "../src/components/canvas/canvas-agent-subagent-conversation";
import { SubagentAuthorization } from "../src/components/canvas/canvas-cloud-agent-settings";
import { canvasThemes } from "../src/lib/canvas-theme";
import type { AgentEvent } from "../src/services/api/agent";
import type { AgentSubagent } from "../src/services/api/agent-subagent";

const agent: AgentSubagent = {
    id: "link",
    parentRunId: "parent",
    childRunId: "child",
    displayName: "编剧<一>",
    roleLabel: "叙事",
    objective: "分析剧情",
    depth: 1,
    status: "completed",
    messages: [{ id: "report", direction: "child_to_parent", kind: "final_result", text: "建议完善角色动机", sequence: 1 }],
};
const event = (seq: number, type: string, text: string, messageId = "answer"): AgentEvent => ({ eventId: `e${seq}`, runId: "child", seq, type, payload: { messageId, text }, createdAt: "" });

test("child transcript deduplicates streamed snapshots and sorts persisted events", () => {
    const messages = subagentConversationMessages([
        event(3, "assistant_snapshot", "完整答复"),
        event(1, "assistant_delta", "完"),
        event(2, "assistant_delta", "整"),
        event(3, "assistant_snapshot", "完整答复"),
        event(4, "assistant_message", "最终答复"),
        event(5, "reasoning_delta", "内部推理"),
    ]);
    expect(messages).toHaveLength(1);
    expect(messages[0].text).toBe("最终答复");
});

test("live draft snapshots replace existing deltas before the next chunk arrives", () => {
    const snapshot = { ...event(0, "assistant_message", "A"), eventId: "local-snapshot", localSeq: 1 };
    expect(subagentConversationMessages([event(1, "assistant_delta", "A"), snapshot, event(2, "assistant_delta", "B")])[0].text).toBe("AB");
});

test("child conversation preserves reports and gives a return path, loading and retry states", () => {
    const html = renderToStaticMarkup(<AgentSubagentConversationView agent={agent} theme={canvasThemes.dark} messages={[]} loading={false} error="读取失败" truncated onBack={() => {}} onRefresh={() => {}} />);
    expect(html).toContain("编剧&lt;一&gt;");
    expect(html).toContain("分析剧情");
    expect(html).toContain("建议完善角色动机");
    expect(html).toContain('aria-label="返回父 Agent 对话"');
    expect(html).toContain("刷新会话");
    expect(html).toContain("读取失败");
    expect(html).toContain("最近");
    expect(html).not.toContain("<textarea");
});

test("canvas subagent switch retains persisted authorization and disables during a run", () => {
    const html = renderToStaticMarkup(<SubagentAuthorization enabled disabled saving={false} error="授权加载失败" theme={canvasThemes.light} onToggle={() => {}} onReload={() => {}} />);
    expect(html).toContain('role="switch"');
    expect(html).toContain('aria-checked="true"');
    expect(html).toContain('disabled=""');
    expect(html).toContain("持续生效");
    expect(html).toContain("授权加载失败");
    expect(html).toContain("重新加载");
});

test("waiting children retain their waiting label in conversation details", () => {
    const html = renderToStaticMarkup(<AgentSubagentConversationView agent={{ ...agent, status: "waiting_approval" }} theme={canvasThemes.light} messages={[]} loading={false} error="" onBack={() => {}} onRefresh={() => {}} />);
    expect(html).toContain("叙事 · 等待中");
    expect(html).not.toContain("运行中");
});
