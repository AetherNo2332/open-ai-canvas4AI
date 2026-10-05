import type { AgentRunSkill, AgentSkillDefaultsSummary } from "@/services/api/agent";

export function effectiveAgentDefaultSkills(
    run: { skills?: Pick<AgentRunSkill, "id" | "name" | "source">[] } | null,
    summary: AgentSkillDefaultsSummary,
): AgentSkillDefaultsSummary["skills"] {
    if (!run) return summary.skills;
    return (run.skills || []).filter(skill => skill.source === "global")
        .map(skill => ({ skillId: skill.id, skillName: skill.name }));
}
