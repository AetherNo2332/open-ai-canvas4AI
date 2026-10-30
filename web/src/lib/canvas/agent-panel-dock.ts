/**
 * Agent 面板“停靠在画布右侧”的本机偏好。停靠时面板与画布并排：工作区让出面板宽度，
 * 画布不被遮挡；浮动窗口的位置和大小另存（见 agent-panel-layout.ts），两者互不覆盖。
 */

export type AgentPanelDockPreference = { docked: boolean; width: number };

export const AGENT_PANEL_DOCK_KEY = "canvas:agent-panel-dock:v1";
/** 视口窄于这个宽度时并排会把画布挤得太小，自动回到浮动窗口。 */
export const AGENT_DOCK_MIN_VIEWPORT = 960;
export const AGENT_DOCK_DEFAULT_WIDTH = 420;
const AGENT_DOCK_MIN_WIDTH = 360;
const AGENT_DOCK_MAX_WIDTH = 720;

/** 面板最宽占视口一半，保证画布至少留一半。 */
export function agentDockWidthBounds(viewportWidth: number) {
    return { min: AGENT_DOCK_MIN_WIDTH, max: Math.max(AGENT_DOCK_MIN_WIDTH, Math.min(AGENT_DOCK_MAX_WIDTH, Math.floor(viewportWidth / 2))) };
}

export function clampAgentDockWidth(width: number, viewportWidth: number) {
    const { min, max } = agentDockWidthBounds(viewportWidth);
    const value = Number.isFinite(width) ? width : AGENT_DOCK_DEFAULT_WIDTH;
    return Math.round(Math.min(Math.max(value, min), max));
}

export function restoreAgentPanelDock(raw: string | null): AgentPanelDockPreference {
    const fallback = { docked: false, width: AGENT_DOCK_DEFAULT_WIDTH };
    if (!raw) return fallback;
    try {
        const parsed: unknown = JSON.parse(raw);
        if (parsed && typeof parsed === "object") {
            const { docked, width } = parsed as Record<string, unknown>;
            if (typeof docked === "boolean" && typeof width === "number" && Number.isFinite(width)) return { docked, width };
        }
    } catch {
        // UI 偏好损坏不影响对话，回到默认浮动窗口。
    }
    return fallback;
}

/** fork 旧版侧栏宽度偏好；只在首次读取停靠偏好时迁移一次，之后由 AGENT_PANEL_DOCK_KEY 接管。 */
export const LEGACY_AGENT_PANEL_WIDTH_KEY = "canvas-agent-panel-width";

/**
 * 读取停靠偏好：已有新偏好时原样使用；没有时与上游一致默认浮动，
 * 并把旧版保存的宽度作为初始停靠宽度。宽度在使用时再按视口夹取。
 */
export function migrateAgentPanelDock(raw: string | null, legacyWidth: string | null): { preference: AgentPanelDockPreference; migrated: boolean } {
    if (raw !== null) {
        const restored = restoreAgentPanelDock(raw);
        // 损坏的新偏好不回退到旧键：旧键在首次迁移后已被清理。
        return { preference: restored, migrated: false };
    }
    const width = legacyWidth === null ? Number.NaN : Number(legacyWidth);
    return { preference: { docked: false, width: Number.isFinite(width) && width > 0 ? width : AGENT_DOCK_DEFAULT_WIDTH }, migrated: true };
}
