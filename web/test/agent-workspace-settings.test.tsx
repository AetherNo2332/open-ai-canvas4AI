import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { CanvasAgentRulesSettings } from "../src/components/canvas/canvas-agent-rules-settings";
import { WorkspaceSettingsForm, workspaceDocumentBytes } from "../src/components/canvas/canvas-agent-workspace-settings";
import type { AgentWorkspaceView } from "../src/services/api/agent-workspace";

test("rules menu groups account defaults and current canvas without preference or skill configuration", () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const html = renderToStaticMarkup(<QueryClientProvider client={client}><CanvasAgentRulesSettings canvasId="canvas" /></QueryClientProvider>);
    expect(html).toContain("全局");
    expect(html).toContain("当前画布");
    expect(html).not.toContain("长期偏好");
    expect(html).not.toContain("技能来源与版本");
    client.clear();
});

const view: AgentWorkspaceView = {
    workspaceId: "workspace",
    canvasId: "canvas",
    revision: 7,
    agentsMd: "项目规则",
    agentsMdHash: "abc123",
};
test("Workspace editor exposes canvas rules without a second skill configuration", () => {
    const html = renderToStaticMarkup(
        <WorkspaceSettingsForm view={view} saving={false} onSaveDocument={async () => view} onReload={async () => {}} />,
    );
    expect(html).toContain('aria-label="Workspace Agents.md"');
    expect(html).toContain("项目规则");
    expect(html).toContain("仅用于当前画布的新运行");
    expect(html).toContain("与全局自定义系统提示词冲突时");
    expect(html).toContain("Workspace");
    expect(html).not.toContain("技能来源与版本");
    expect(html).not.toContain("保存技能");
    expect(html).not.toContain("追加已安装的公开技能");
});
test("Workspace document admission counts UTF-8 bytes including Chinese and emoji", () => {
    expect(workspaceDocumentBytes("界🙂")).toBe(7);
    const over = { ...view, agentsMd: "界".repeat(22000) };
    const html = renderToStaticMarkup(<WorkspaceSettingsForm view={over} saving={false} onSaveDocument={async () => view} onReload={async () => {}} />);
    expect(html).toContain("66000 / 65536");
    expect(html).toContain("超过 64KB");
    const saveButton = html.match(/<button[^>]*><span>保存文档<\/span><\/button>/)?.[0];
    expect(saveButton).toContain('disabled=""');
});
