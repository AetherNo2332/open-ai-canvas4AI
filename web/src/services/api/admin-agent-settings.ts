import { http } from "./request";

export interface AgentSchedulerSetting {
    dispatchConcurrency: number;
    maxResidentSessions: number;
    maxResidentPerCanvas: number;
    revision: number;
    updatedAt: string;
}
export interface AgentRuntimeStatus {
    instanceId: string;
    resident: number;
    capacity: number;
    claimReservations: number;
    dispatchActive: number;
    readyQueued: number;
    draining: boolean;
    appliedConfigRevision: number;
    lastHeartbeatAt: string;
    online: boolean;
}
export type AgentSchedulerDraft = Pick<AgentSchedulerSetting, "dispatchConcurrency" | "maxResidentSessions" | "maxResidentPerCanvas">;
export const getAgentSchedulerSetting = () => http.get<{ setting: AgentSchedulerSetting }>("/admin/settings/agent-scheduler");
export const updateAgentSchedulerSetting = (input: AgentSchedulerDraft & { expectedRevision: number }) => http.put<{ setting: AgentSchedulerSetting }>("/admin/settings/agent-scheduler", input);
export const listAgentSchedulerStatus = () => http.get<{ instances: AgentRuntimeStatus[] }>("/admin/settings/agent-scheduler/status");
