import { expect, test } from "bun:test";
import { ApiError } from "../src/services/api/request";
import { agentSubmissionErrorTitle } from "../src/lib/canvas/agent-error-presentation";

test("skill library removes count limits and shows global provenance", async () => {
    const modal = await Bun.file(new URL("../src/components/canvas/canvas-agent-skill-library-modal.tsx", import.meta.url)).text();
    expect(modal).not.toContain("/ 8");
    expect(modal).not.toContain("最多启用 8 个");
    expect(modal).not.toContain("selectedCount < 8");
    expect(modal).toContain("全局默认");
    expect(modal).toContain("globalDefaultSkillIds");
    const panel = await Bun.file(new URL("../src/components/canvas/canvas-cloud-agent-panel.tsx", import.meta.url)).text();
    expect(panel).toContain("globalDefaultSkillIds={globalDefaultSkillIds}");
});

test("budget refusal presents the backend message", () => {
    const message = "上下文预算超限：上限 512 KiB，实际 600 KiB";
    expect(agentSubmissionErrorTitle(new ApiError(message, { reason: "agent_skill_budget_exceeded", status: 400 }), false)).toBe(message);
    expect(agentSubmissionErrorTitle(new ApiError(message, { reason: "agent_skill_budget_exceeded", status: 400 }), true)).toBe("运行已接收，但本地提交记录清理失败");
});
