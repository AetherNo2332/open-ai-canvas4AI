import { http } from "./request";

export interface AgentSchedulerSetting {
    dispatchConcurrency: number;
    maxResidentSessions: number;
    maxResidentPerUser: number;
    maxResidentPerCanvas: number;
    revision: number;
    updatedAt: string;
}
export type AgentSchedulerDraft = Pick<AgentSchedulerSetting, "dispatchConcurrency" | "maxResidentSessions" | "maxResidentPerUser" | "maxResidentPerCanvas">;
export const getAgentSchedulerSetting = () => http.get<{ setting: AgentSchedulerSetting; toolCount: number }>("/admin/settings/agent-scheduler");
export const updateAgentSchedulerSetting = (input: AgentSchedulerDraft & { expectedRevision: number }) => http.put<{ setting: AgentSchedulerSetting }>("/admin/settings/agent-scheduler", input);
export interface AgentRuntimeStatus {
    instanceId: string;
    online: boolean;
    readiness: "starting" | "ready" | "degraded" | "draining";
    readinessReason?: string;
    resident: number;
    capacity: number;
    lastHeartbeatAt: string;
}
export const getAgentSchedulerStatus = () => http.get<{ instances: AgentRuntimeStatus[] }>("/admin/settings/agent-scheduler/status");

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
