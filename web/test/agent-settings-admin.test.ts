import { expect, test } from "bun:test";
import { agentMemoryRedirect } from "../src/lib/agent-admin-route";

test("old memory links preserve queries and target the memory section", () => {
    expect(agentMemoryRedirect("?userId=abc&status=approved")).toBe("/admin/settings/agent?userId=abc&status=approved#memory");
});
test("admin Agent entry owns config, defaults and the existing memory panel", async () => {
    const shell = await Bun.file(new URL("../src/pages/admin/components/admin-shell.tsx", import.meta.url)).text();
    const page = await Bun.file(new URL("../src/pages/admin/settings/agent-settings-page.tsx", import.meta.url)).text();
    expect(shell).toContain('label: "Agent（beta）"');
    expect(page).toContain('title="Agent（beta）"');
    expect(page).toContain("AgentLessonsPanel");
    expect(page).not.toContain("listAgentSchedulerStatus");
    expect(page).not.toContain("#runtime");
    expect(page).not.toContain("AgentRuntimeStatusView");
    expect(page).toContain("getAgentSchedulerSetting");
    expect(page).toContain("#memory");
    expect(page).toContain("updateAgentSchedulerSetting");
});

test("healthy runtime policy page includes the Agent management link", async () => {
    const source = await Bun.file(new URL("../src/pages/admin/settings/runtime-policy-settings-page.tsx", import.meta.url)).text();
    const normal = source.slice(source.indexOf("const activeTaskLimit ="));
    expect(normal).toContain('to="/admin/settings/agent"');
});
