import { expect, test } from "bun:test";

const apiUrl = new URL("../src/services/api/system-performance.ts", import.meta.url);
const pageUrl = new URL("../src/pages/admin/settings/system-performance-page.tsx", import.meta.url);

test("system performance API no longer exposes the removed agent-limit endpoint", async () => {
    const api = await import("../src/services/api/system-performance");
    const source = await Bun.file(apiUrl).text();
    expect("updateSystemPerformanceAgentLimit" in api).toBe(false);
    expect("updateAgentSessionLimit" in api).toBe(false);
    expect(source).not.toContain("agent-limit");
});

test("system performance page links to agent scheduling settings instead of editing limits", async () => {
    const source = await Bun.file(pageUrl).text();
    expect(source).not.toContain("agent-limit");
    expect(source).not.toContain("data.agents");
    expect(source).toMatch(/<Link[^>]*to="\/admin\/settings\/agent"[^>]*>\s*Agent 调度设置\s*<\/Link>/);
});
