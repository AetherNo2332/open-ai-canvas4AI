import { http } from "@/services/api/request";

export type AgentSkillDefaultItemInput = { skillId: string; skillVersionId: string; position: number; enabled: number };
export type AgentSkillDefaultsViewItem = AgentSkillDefaultItemInput & { skillName: string; versionLabel: string; status: number; fileCount: number; totalBytes: number };
export type AgentSkillDefaultsView = { revision: number; items: AgentSkillDefaultsViewItem[]; totalFiles: number; totalBytes: number; contextEstimateBytes: number };

export function listAdminAgentSkillDefaults() {
    return http.get<AgentSkillDefaultsView>("/admin/agent/skill-defaults");
}

export function updateAdminAgentSkillDefaults(input: { revision: number; items: AgentSkillDefaultItemInput[] }) {
    return http.put<{ revision: number }>("/admin/agent/skill-defaults", input);
}
