import { http } from "@/services/api/request";

export type WorkspaceSkillSelection = { skillId: string; skillVersionId: string; position: number; enabled: boolean };
export type WorkspaceSkillView = WorkspaceSkillSelection & { skillName: string; contentHash: string; fileCount: number; totalBytes: number; source: "workspace" };
export type AgentWorkspaceView = { workspaceId: string; canvasId: string; revision: number; agentsMd: string; agentsMdHash: string; skills: WorkspaceSkillView[] };

const path = (canvasId: string) => `/agent/workspaces/${encodeURIComponent(canvasId)}`;
export function getAgentWorkspace(canvasId: string, signal?: AbortSignal) {
    return http.get<AgentWorkspaceView>(path(canvasId), { signal });
}
export function updateAgentWorkspace(canvasId: string, revision: number, agentsMd: string) {
    return http.patch<AgentWorkspaceView>(path(canvasId), { revision, agentsMd });
}
export function listWorkspaceSkills(canvasId: string, signal?: AbortSignal) {
    return http.get<WorkspaceSkillView[]>(`${path(canvasId)}/skills`, { signal });
}
export function replaceWorkspaceSkills(canvasId: string, revision: number, skills: WorkspaceSkillSelection[]) {
    return http.put<AgentWorkspaceView>(`${path(canvasId)}/skills`, { revision, skills });
}
