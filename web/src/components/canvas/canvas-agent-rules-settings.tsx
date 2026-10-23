import { Tabs } from "antd";
import { AgentSystemPromptPane } from "@/components/agent/agent-system-prompt-settings";
import { CanvasAgentWorkspaceSettings } from "./canvas-agent-workspace-settings";

export const agentRulesTabs = [
    { key: "global", label: "全局" },
    { key: "canvas", label: "当前画布" },
] as const;

export function CanvasAgentRulesSettings({ canvasId }: { canvasId: string }) {
    return (
        <div className="canvas-agent-settings-scroll thin-scrollbar min-h-0 flex-1 overflow-y-auto p-4">
            <Tabs
                defaultActiveKey="global"
                items={agentRulesTabs.map((tab) => ({
                    ...tab,
                    children: tab.key === "global" ? <AgentSystemPromptPane /> : <CanvasAgentWorkspaceSettings canvasId={canvasId} />,
                }))}
            />
        </div>
    );
}
