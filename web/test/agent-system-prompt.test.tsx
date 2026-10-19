import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentSystemPromptForm } from "@/pages/settings/agent-system-prompt-pane";

test("account system prompt form renders saved content and an explicit save action", () => {
    const html = renderToStaticMarkup(<AgentSystemPromptForm settings={{ systemPrompt: "用中文回答，保留角色名称。", revision: 3 }} onSave={async () => ({ systemPrompt: "", revision: 4 })} onReload={() => {}} />);
    expect(html).toContain("用中文回答，保留角色名称。");
    expect(html).toContain("自定义系统提示词");
    expect(html).toContain("保存提示词");
    expect(html).toContain("清空");
    expect(html).toContain("12000");
    expect(html).toContain('id="agent-system-prompt"');
    expect(html).toContain('disabled=""');
});

test("empty settings provide an editable input and explain when changes take effect", () => {
    const html = renderToStaticMarkup(<AgentSystemPromptForm settings={{ systemPrompt: "", revision: 0 }} onSave={async () => ({ systemPrompt: "", revision: 1 })} onReload={() => {}} />);
    expect(html).toContain("后续新建的运行");
    expect(html).toContain("留空");
    expect(html).toContain("textarea");
});
