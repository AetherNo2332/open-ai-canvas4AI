import { http } from "@/services/api/request";

export type AgentSubagentPolicy = { canvasId: string; enabled: boolean; revision: number };

export const getAgentSubagentPolicy = (canvasId: string, signal?: AbortSignal) => http.get<AgentSubagentPolicy>(`/agent/subagent-policy?canvasId=${encodeURIComponent(canvasId)}`, { signal });

export const updateAgentSubagentPolicy = (input: { canvasId: string; enabled: boolean; expectedRevision: number }) => http.put<AgentSubagentPolicy>("/agent/subagent-policy", input);
