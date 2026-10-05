import { expect, test } from "bun:test";
import { formatBuildVersion } from "../src/lib/app-version";
import { agentMemoryRedirect } from "../src/lib/agent-admin-route";

test("version shows the actual build commit in parentheses", () => {
    expect(formatBuildVersion("v1.5.7.2", "abcdef123456")).toBe("v1.5.7.2 (abcdef1)");
    expect(formatBuildVersion("v1.5.7.1+0af49f0", "unknown")).toBe("v1.5.7.1 (0af49f0)");
    expect(formatBuildVersion("v1.5.7.2", "unknown")).toBe("v1.5.7.2");
});
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
