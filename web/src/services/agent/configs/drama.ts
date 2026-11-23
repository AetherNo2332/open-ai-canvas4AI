import type { AgentPanelConfig } from "../panel-config";

export const dramaAgentPanelConfig: AgentPanelConfig = {
    id: "drama",
    businessType: "drama",
    agentDefinitionId: "drama.analysis",
    title: "短剧 Agent",
    placeholder: "讨论剧情、人物与镜头；正式拆分仍在工作台触发",
    welcome: "先讨论本集内容。每轮发送时会冻结当前剧集作为分析目标。",
    modelSource: "project-text",
    controls: { model: true, reasoning: true, skills: true, contextUsage: true, sendMode: true },
    extensions: ["episode-target"],
    docking: "floating",
};
