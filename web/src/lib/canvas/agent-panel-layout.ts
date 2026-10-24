export type AgentPanelViewport = { width: number; height: number };

/** 桌面端右侧平行窗口宽度：留出最小可操作画布区，窄屏退化为全宽。 */
export const AGENT_PANEL_WIDTH = 440;
export const AGENT_PANEL_MIN_CANVAS_WIDTH = 560;
export const AGENT_PANEL_MIN_WIDTH = 360;
export const AGENT_PANEL_MAX_WIDTH = 800;

export function agentPanelIsCompact(viewport: AgentPanelViewport): boolean {
    return viewport.width < AGENT_PANEL_WIDTH + AGENT_PANEL_MIN_CANVAS_WIDTH;
}

export function agentPanelWidth(viewport: AgentPanelViewport, preferred = AGENT_PANEL_WIDTH): number {
    if (agentPanelIsCompact(viewport)) return viewport.width;
    return Math.max(AGENT_PANEL_MIN_WIDTH, Math.min(Number.isFinite(preferred) ? preferred : AGENT_PANEL_WIDTH, AGENT_PANEL_MAX_WIDTH, viewport.width - AGENT_PANEL_MIN_CANVAS_WIDTH));
}
