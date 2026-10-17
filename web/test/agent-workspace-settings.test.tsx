import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { WorkspaceSettingsForm, workspaceDocumentBytes } from "../src/components/canvas/canvas-agent-workspace-settings";
import type { AgentWorkspaceView } from "../src/services/api/agent-workspace";

const view: AgentWorkspaceView = {
    workspaceId: "workspace",
    canvasId: "canvas",
    revision: 7,
    agentsMd: "项目规则",
    agentsMdHash: "abc123",
    skills: [{ skillId: "story", skillVersionId: "v2", skillName: "分镜", contentHash: "def456", fileCount: 1, totalBytes: 10, source: "workspace", position: 0, enabled: true }],
};
test("Workspace editor exposes frozen scope, current revision and versioned skill sources", () => {
    const html = renderToStaticMarkup(
        <WorkspaceSettingsForm view={view} defaultSkills={[{ skillId: "global", skillName: "导演" }]} availableSkills={[]} saving={false} onSaveDocument={async () => view} onSaveSkills={async () => view} onReload={async () => {}} />,
    );
    expect(html).toContain('aria-label="Workspace Agents.md"');
    expect(html).toContain("项目规则");
    expect(html).toContain("abc123");
    expect(html).toContain("7");
    expect(html).toContain("仅影响新 Run");
    expect(html).toContain("全局默认");
    expect(html).toContain("Workspace");
    expect(html).toContain("v2");
    expect(html).toContain("导演");
    expect(html).toContain("分镜");
});
test("Workspace document admission counts UTF-8 bytes including Chinese and emoji", () => {
    expect(workspaceDocumentBytes("界🙂")).toBe(7);
    const over = { ...view, agentsMd: "界".repeat(22000) };
    const html = renderToStaticMarkup(<WorkspaceSettingsForm view={over} defaultSkills={[]} availableSkills={[]} saving={false} onSaveDocument={async () => view} onSaveSkills={async () => view} onReload={async () => {}} />);
    expect(html).toContain("66000 / 65536");
    expect(html).toContain("超过 64KB");
    const saveButton = html.match(/<button[^>]*><span>保存文档<\/span><\/button>/)?.[0];
    expect(saveButton).toContain('disabled=""');
});
