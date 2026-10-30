import { expect, test } from "bun:test";
test("admin Agent entry owns config and defaults; memory documents have their own page", async () => {
    const shell = await Bun.file(new URL("../src/pages/admin/components/admin-shell.tsx", import.meta.url)).text();
    const page = await Bun.file(new URL("../src/pages/admin/settings/agent-settings-page.tsx", import.meta.url)).text();
    expect(shell).toContain('label: "Agent（beta）"');
    expect(page).toContain('title="Agent（beta）"');
    expect(page).not.toContain("AgentLessonsPanel");
    expect(page).toContain('to="/admin/agent-lessons"');
    expect(page).not.toContain("listAgentSchedulerStatus");
    expect(page).not.toContain("#runtime");
    expect(page).not.toContain("AgentRuntimeStatusView");
    expect(page).toContain("getAgentSchedulerSetting");
    expect(page).toContain("updateAgentSchedulerSetting");
});

test("healthy runtime policy page includes the Agent management link", async () => {
    const source = await Bun.file(new URL("../src/pages/admin/settings/runtime-policy-settings-page.tsx", import.meta.url)).text();
    const normal = source.slice(source.indexOf("const activeTaskLimit ="));
    expect(normal).toContain('to="/admin/settings/agent"');
});
