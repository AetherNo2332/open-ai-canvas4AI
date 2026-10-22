import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentWelcome } from "@/components/canvas/canvas-agent-welcome";
import { AgentChatComposer } from "@/components/canvas/canvas-cloud-agent-chat-ui";
import { AgentHeader } from "@/components/canvas/canvas-cloud-agent-panel";
import { agentPermissionVisual } from "@/components/canvas/canvas-cloud-agent-settings";
import { canvasThemes } from "@/lib/canvas-theme";
import { DEFAULT_CANVAS_APPEARANCE, agentCopy } from "@/lib/canvas/agent-appearance";

const noop = () => {};

test("Skills is accessible in the Agent header with its selected count and dialog state", () => {
    const html = renderToStaticMarkup(<AgentHeader theme={canvasThemes.dark} hasMessages statusLabel="运行中" statusColor="green" nodeCount={12}
        onNew={noop} onHistory={noop} onSettings={noop} onCollapse={noop} onExport={noop} exporting={false}
        skillsOpen selectedSkillCount={3} onSkills={noop} />);
    expect(html).toContain("Skills(3)");
    expect(html).toContain('aria-label="打开 Skills 技能库，已启用 3 个"');
    expect(html).toContain('aria-expanded="true"');
    expect(html).toContain('aria-haspopup="dialog"');
    expect(html.indexOf("Skills(3)")).toBeLessThan(html.indexOf("导出 Agent 调试记录"));
});

test("welcome uses the independent assistant name and escapes it as text", () => {
    const html = renderToStaticMarkup(<AgentWelcome appearance={{ ...DEFAULT_CANVAS_APPEARANCE, agentName: "我的<工作室>" }} nodeCount={2} onChooseSkill={noop} onDraftPrompt={noop} />);
    expect(html).toContain("在这里，和我的&lt;工作室&gt;让灵感，慢慢成形");
    expect(html).toContain("2 个节点");
    expect(html.match(/type="button"/g)).toHaveLength(3);
    expect(html).not.toContain('disabled=""');
});

test("empty canvas disables only the canvas-analysis shortcut", () => {
    const html = renderToStaticMarkup(<AgentWelcome appearance={DEFAULT_CANVAS_APPEARANCE} nodeCount={0} onChooseSkill={noop} onDraftPrompt={noop} />);
    expect(html.match(/disabled=""/g)).toHaveLength(1);
    expect(html).toContain("添加节点后，一起梳理创作思路");
    expect(html).not.toContain("生成前由你确认");
});

test("assistant template replacement treats names literally", () => {
    expect(agentCopy("{agentName} / {agentName}", "$&小鱼")).toBe("$&小鱼 / $&小鱼");
});

test("permission modes have distinct icons, not just distinct colors", () => {
    const icons = (["read_only", "request_approval", "auto"] as const).map((mode) => {
        const Icon = agentPermissionVisual(mode).icon;
        return renderToStaticMarkup(<Icon />);
    });
    expect(new Set(icons).size).toBe(3);
    expect(icons[0]).toContain("lucide-lock-keyhole");
    expect(icons[1]).toContain("lucide-shield-check");
    expect(icons[2]).toContain("lucide-sparkles");
});

test("cloud Agent starts in automatic approval mode", async () => {
    const source = await Bun.file(new URL("../src/components/canvas/canvas-cloud-agent-panel.tsx", import.meta.url)).text();
    expect(source).toContain('useState<AgentPermissionMode>("auto")');
});

test("compact send button preserves empty-draft and sending guards", () => {
    for (const [prompt, sending, disabled] of [
        ["", false, true],
        ["故事灵感", false, false],
        ["故事灵感", true, true],
    ] as const) {
        const html = renderToStaticMarkup(<AgentChatComposer prompt={prompt} sending={sending} placeholder="聊聊想法" theme={canvasThemes.dark} onPromptChange={noop} onSubmit={noop} />);
        const button = html.match(/<button\b[^>]*class="agent-composer-send [^"]*"[^>]*>/)?.[0];
        expect(button).toBeDefined();
        expect(button).toContain("size-7");
        expect(button?.includes('disabled=""')).toBe(disabled);
        expect(button).not.toContain("box-shadow:");
    }
});
