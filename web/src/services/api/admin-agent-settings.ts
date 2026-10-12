import { http } from "./request";

export interface AgentSchedulerSetting {
    dispatchConcurrency: number;
    maxResidentSessions: number;
    maxResidentPerCanvas: number;
    revision: number;
    updatedAt: string;
}
export type AgentSchedulerDraft = Pick<AgentSchedulerSetting, "dispatchConcurrency" | "maxResidentSessions" | "maxResidentPerCanvas">;
export const getAgentSchedulerSetting = () => http.get<{ setting: AgentSchedulerSetting }>("/admin/settings/agent-scheduler");
export const updateAgentSchedulerSetting = (input: AgentSchedulerDraft & { expectedRevision: number }) => http.put<{ setting: AgentSchedulerSetting }>("/admin/settings/agent-scheduler", input);

export interface AgentWebSearchSetting {
    enabled: boolean;
    hasApiKey: boolean;
    revision: number;
    updatedAt?: string;
}
export interface AgentWebSearchDraft {
    enabled: boolean;
    apiKey: string;
    clearApiKey: boolean;
    expectedRevision: number;
}
export const getAgentWebSearchSetting = () => http.get<{ setting: AgentWebSearchSetting }>("/admin/settings/agent-web-search");
export const updateAgentWebSearchSetting = (input: AgentWebSearchDraft) => http.put<{ setting: AgentWebSearchSetting }>("/admin/settings/agent-web-search", input);
