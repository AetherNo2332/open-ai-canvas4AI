import { expect, test } from "bun:test";

test("Agent settings exposes the default skill editor and its CAS save contract", async () => {
    const page = await Bun.file(new URL("../src/pages/admin/settings/agent-settings-page.tsx", import.meta.url)).text();
    expect(page).toContain('<section id="skill-defaults"');
    expect(page).toContain('<a href="#skill-defaults">');
    expect(page).toContain("默认技能");
    const api = await Bun.file(new URL("../src/services/api/admin-skill-defaults.ts", import.meta.url)).text();
    expect(api).toContain("listAdminAgentSkillDefaults");
    expect(api).toContain("updateAdminAgentSkillDefaults");
    expect(api).toContain('"/admin/agent/skill-defaults"');
    expect(api).toContain("revision: number");
    expect(api).toContain("items: AgentSkillDefaultItemInput[]");
    const editor = await Bun.file(new URL("../src/pages/admin/settings/components/agent-skill-defaults-section.tsx", import.meta.url)).text();
    expect(editor).toContain("updateAdminAgentSkillDefaults");
    expect(editor).toContain("agent_skill_defaults_revision_conflict");
    expect(editor).toContain("包体积");
    expect(editor).toContain("正文按需读取");
    expect(editor).toContain("合计");
});
