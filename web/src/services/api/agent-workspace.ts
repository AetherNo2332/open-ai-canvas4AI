import { http } from "@/services/api/request";

export type AgentWorkspaceView = { workspaceId: string; canvasId: string; revision: number; agentsMd: string; agentsMdHash: string };

const path = (canvasId: string) => `/agent/workspaces/${encodeURIComponent(canvasId)}`;
export function getAgentWorkspace(canvasId: string, signal?: AbortSignal) {
    return http.get<AgentWorkspaceView>(path(canvasId), { signal });
}
export function updateAgentWorkspace(canvasId: string, revision: number, agentsMd: string) {
    return http.patch<AgentWorkspaceView>(path(canvasId), { revision, agentsMd });
}
