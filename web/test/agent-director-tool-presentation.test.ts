import { expect, test } from "bun:test";
import { AGENT_TOOL_NAMES, agentToolCategory, agentToolCategoryLabel, friendlyAgentToolSummary } from "@/lib/canvas/agent-tool-presentation";

const call = (action: string, eventType = "tool_completed") => ({ eventType, arguments: JSON.stringify({ action }) });

test("the single director tool is categorized by its action", () => {
    expect(AGENT_TOOL_NAMES).toContain("director");
    expect(AGENT_TOOL_NAMES.some((name) => name.startsWith("director_") || name.startsWith("previs_"))).toBe(false);
    expect(agentToolCategory("director", call("read"))).toBe("read");
    expect(agentToolCategoryLabel("director", "read")).toBe("读取场景");
    expect(agentToolCategory("director", call("create_scene"))).toBe("create");
    expect(agentToolCategory("director", call("apply_patch"))).toBe("operate");
    expect(agentToolCategory("director", call("preview"))).toBe("operate");
    // 参数缺失或无法解析时不能落进只读档。
    expect(agentToolCategory("director", { eventType: "tool_completed" })).toBe("operate");
});

test("director summaries name the action in Chinese", () => {
    expect(friendlyAgentToolSummary("director", "", call("read"))).toBe("已读取导演台场景");
    expect(friendlyAgentToolSummary("director", "", call("create_scene"))).toBe("导演场景已创建，导演台已打开");
    expect(friendlyAgentToolSummary("director", "", call("apply_patch"), true)).toBe("准备修改导演场景");
    expect(friendlyAgentToolSummary("director", "", call("preview"))).toBe("导演台预演已录制并回传");
    expect(friendlyAgentToolSummary("director", "", call("preview", "tool_failed"))).toBe("导演台预演未完成");
});
