import { expect, test } from "bun:test";
import { agentToolCategory, friendlyAgentToolSummary } from "../src/lib/canvas/agent-tool-presentation";

test("web search is shown as reading information with a useful receipt", () => {
    expect(agentToolCategory("web_search", { eventType: "tool_completed" })).toBe("read");
    expect(friendlyAgentToolSummary("web_search", "工具执行成功", { eventType: "tool_completed" })).toContain("联网搜索");
});
