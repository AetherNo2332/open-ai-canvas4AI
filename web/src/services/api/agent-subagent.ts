import { http } from "@/services/api/request";

export type AgentSubagentPolicy = { canvasId: string; enabled: boolean; revision: number };
export type AgentSubagentMessage = { id: string; direction: string; kind: string; text: string; sequence: number };
export type AgentSubagent = { id: string; parentRunId: string; childRunId: string; displayName: string; roleLabel: string; objective: string; depth: number; status: string; failureMessage?: string; messages: AgentSubagentMessage[] };

export const getAgentSubagentPolicy = (canvasId: string, signal?: AbortSignal) => http.get<AgentSubagentPolicy>(`/agent/subagent-policy?canvasId=${encodeURIComponent(canvasId)}`, { signal });

export const updateAgentSubagentPolicy = (input: { canvasId: string; enabled: boolean; expectedRevision: number }) => http.put<AgentSubagentPolicy>("/agent/subagent-policy", input);

export const listAgentSubagents = (runId: string, signal?: AbortSignal) => http.get<AgentSubagent[]>(`/agent/runs/${encodeURIComponent(runId)}/subagents`, { signal });
