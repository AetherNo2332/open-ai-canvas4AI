import { expect, test } from "bun:test";
import { agentToolCategory, friendlyAgentToolSummary } from "@/lib/canvas/agent-tool-presentation";

test("director tools have distinct Agent presentation and preserve read/write semantics", () => {
    expect(agentToolCategory("director_check_continuity")).toBe("read");
    expect(agentToolCategory("director_handoff_shot")).toBe("read");
    expect(agentToolCategory("director_block_scene")).toBe("create");
    expect(agentToolCategory("director_design_shot")).toBe("operate");
    expect(agentToolCategory("director_preview_shot")).toBe("operate");
    expect(friendlyAgentToolSummary("director_draw_action", "", { eventType: "tool_completed" })).toBe("动作路径已保存");
});
