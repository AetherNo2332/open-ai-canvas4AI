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
