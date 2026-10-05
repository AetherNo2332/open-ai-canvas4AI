import { expect, test } from "bun:test";
import { ApiError } from "../src/services/api/request";
import { agentErrorPresentation, agentSubmissionErrorTitle } from "../src/lib/canvas/agent-error-presentation";

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

test("real budget envelope details survive chat presentation", () => {
    const error = new ApiError("技能集合超出上下文预算", { status: 400, reason: "agent_skill_budget_exceeded", details: { budget: "files", limit: 1024, actual: 1025 } });
    const title = agentSubmissionErrorTitle(error, false);
    expect(title).toContain("文件数");
    expect(title).toContain("1024");
    expect(title).toContain("1025");
    expect(agentErrorPresentation(error).text).toBe(title);
    for (const [budget, label] of [
        ["total_bytes", "包体积"],
        ["context_bytes", "上下文估算"],
    ]) {
        const bytesError = new ApiError(error.message, { reason: error.reason, details: { budget, limit: 1024, actual: 1025 } });
        expect(agentErrorPresentation(bytesError).text).toContain(label);
    }
});
