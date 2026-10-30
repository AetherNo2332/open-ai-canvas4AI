import { create } from "zustand";
import { AGENT_PANEL_DOCK_KEY, LEGACY_AGENT_PANEL_WIDTH_KEY, migrateAgentPanelDock, restoreAgentPanelDock, type AgentPanelDockPreference } from "@/lib/canvas/agent-panel-dock";
import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import type { CanvasNodeData } from "@/types/canvas";

/** 画布页交给全局面板的一次输入：autoSubmit 时按 fork 的外部发送语义直接提交，不改动用户草稿。 */
export type AgentPrefillRequest = {
    id: number;
    /** 只投递给发起请求的画布；面板当前画布不同就丢弃。 */
    canvasId: string;
    /** 发起时的账号 scope；账号切换后不再投递。 */
    userScope: string;
    text: string;
    autoSubmit?: boolean;
    /** 本轮必须在支持图片输入的平台模型上运行（图片复现预演台）。 */
    requiresVision?: boolean;
    focusNodeIds?: string[];
};
export type AgentPrefillInput = Omit<AgentPrefillRequest, "id">;
/** sent/queued 与原 sendPrompt 一致；prefilled 表示只写入了输入框；false 表示未投递或被拒绝。 */
export type AgentPrefillOutcome = "sent" | "queued" | "prefilled" | false;

/**
 * 当前页面提供给平台级 Agent 的画布上下文。只有画布编辑器会发布它；
 * 离开画布时清空，此后面板不再以该画布发送请求。
 */
export type AgentCanvasContext = {
    canvasId: string;
    nodeCount: number;
    selectedNodeIds: string[];
    references: CanvasResourceReference[];
    onFocusNode?: (nodeId: string) => void;
    /** 以下三项只为与上游字段名保持一致；fork 面板不读取，画布页也不发布，免得每次节点变化都重渲染面板。 */
    domainProjectId?: string;
    canvasNodes?: readonly CanvasNodeData[];
    runningNodeId?: string | null;
};

type GlobalAgentStore = {
    /** 平台唯一 Agent 面板的开合状态，跨路由保持。 */
    open: boolean;
    canvas: AgentCanvasContext | null;
    /** 当前路由是画布编辑器（由 user-layout 发布）。 */
    canvasRoute: boolean;
    /** 进入画布路由后发布过画布上下文。切换或加载画布时上下文会短暂为空，停靠判断沿用这一位，不闪成浮动窗口。 */
    canvasHeld: boolean;
    /** 面板代码加载失败：之后的投递直接按 false 结束，不在 store 里留下无人消费的请求。 */
    panelUnavailable: boolean;
    /** 待面板消费的一次性输入；同一 id 只消费一次。 */
    prefillRequest: AgentPrefillRequest | null;
    /** 本机 UI 偏好：在画布里是否停靠到右侧，以及停靠宽度。账号切换不影响。 */
    dock: AgentPanelDockPreference;
    openAgent: () => void;
    closeAgent: () => void;
    setCanvas: (canvas: AgentCanvasContext | null) => void;
    setCanvasRoute: (canvasRoute: boolean) => void;
    setDocked: (docked: boolean) => void;
    setDockWidth: (width: number) => void;
    reset: () => void;
};

export function readAgentPanelDockPreference(): AgentPanelDockPreference {
    try {
        if (typeof localStorage === "undefined") return restoreAgentPanelDock(null);
        const { preference, migrated } = migrateAgentPanelDock(localStorage.getItem(AGENT_PANEL_DOCK_KEY), localStorage.getItem(LEGACY_AGENT_PANEL_WIDTH_KEY));
        if (migrated) {
            localStorage.setItem(AGENT_PANEL_DOCK_KEY, JSON.stringify(preference));
            localStorage.removeItem(LEGACY_AGENT_PANEL_WIDTH_KEY);
        }
        return preference;
    } catch (error) {
        console.warn("Agent 停靠偏好无法读取", error);
        return restoreAgentPanelDock(null);
    }
}

function sameRecord(left: object, right: object) {
    if (left === right) return true;
    const leftKeys = Object.keys(left);
    if (leftKeys.length !== Object.keys(right).length) return false;
    return leftKeys.every((key) => Object.is((left as Record<string, unknown>)[key], (right as Record<string, unknown>)[key]));
}

/** 内容没变就沿用旧数组；变了也尽量沿用没变的元素，订阅方可以按引用判断变化。 */
function reuseEqualItems<T>(previous: readonly T[] | undefined, next: readonly T[] | undefined, equal: (left: T, right: T) => boolean): readonly T[] | undefined {
    if (!previous || !next || previous === next) return next;
    let changed = previous.length !== next.length;
    const merged = next.map((item, index) => {
        const old = previous[index];
        if (index < previous.length && equal(old, item)) return old;
        changed = true;
        return item;
    });
    return changed ? merged : previous;
}

/**
 * 画布每次选中、拖动结束或编辑都会重新发布上下文，其中大部分字段并没有变。
 * 这里把内容相同的数组和整份上下文换回旧引用，面板只在真正用到的字段变化时才重渲染。
 */
export function stabilizeCanvasContext(previous: AgentCanvasContext | null, next: AgentCanvasContext | null): AgentCanvasContext | null {
    if (!previous || !next || previous.canvasId !== next.canvasId) return next;
    const stable: AgentCanvasContext = {
        ...next,
        references: reuseEqualItems(previous.references, next.references, sameRecord) as CanvasResourceReference[],
        selectedNodeIds: reuseEqualItems(previous.selectedNodeIds, next.selectedNodeIds, Object.is) as string[],
    };
    return sameRecord(previous, stable) ? previous : stable;
}

let persistDockTimer: ReturnType<typeof setTimeout> | undefined;

// 拖动调宽时每帧都会更新，合并后再写本地存储。
function persistDockPreference(dock: AgentPanelDockPreference) {
    if (typeof localStorage === "undefined") return;
    clearTimeout(persistDockTimer);
    persistDockTimer = setTimeout(() => {
        try {
            localStorage.setItem(AGENT_PANEL_DOCK_KEY, JSON.stringify(dock));
        } catch (error) {
            console.warn("Agent 停靠偏好无法保存", error);
        }
    }, 150);
}

// id 跨画布、跨面板实例单调递增：新画布页或面板重挂载都不会把旧请求当成新请求。
let prefillSequence = 0;
const prefillWaiters = new Map<number, (outcome: AgentPrefillOutcome) => void>();

function settleWaiter(id: number, outcome: AgentPrefillOutcome) {
    const resolve = prefillWaiters.get(id);
    prefillWaiters.delete(id);
    resolve?.(outcome);
}

export const useGlobalAgentStore = create<GlobalAgentStore>()((set, get) => ({
    open: false,
    canvas: null,
    canvasRoute: false,
    canvasHeld: false,
    panelUnavailable: false,
    prefillRequest: null,
    dock: readAgentPanelDockPreference(),
    openAgent: () => set({ open: true }),
    closeAgent: () => set({ open: false }),
    setCanvas: (canvas) =>
        set((state) => {
            const next = stabilizeCanvasContext(state.canvas, canvas);
            if (next === state.canvas) return state;
            // 离开或切换画布时，发给旧画布的待消费输入作废。
            const stalePrefill = state.prefillRequest && state.prefillRequest.canvasId !== next?.canvasId ? state.prefillRequest : null;
            if (stalePrefill) settleWaiter(stalePrefill.id, false);
            const held = { canvas: next, canvasHeld: next !== null || state.canvasHeld };
            return stalePrefill ? { ...held, prefillRequest: null } : held;
        }),
    setCanvasRoute: (canvasRoute) => set((state) => (state.canvasRoute === canvasRoute ? state : { canvasRoute, canvasHeld: canvasRoute && state.canvasHeld })),
    setDocked: (docked) => {
        const dock = { ...get().dock, docked };
        set({ dock });
        persistDockPreference(dock);
    },
    setDockWidth: (width) => {
        if (get().dock.width === width) return;
        const dock = { ...get().dock, width };
        set({ dock });
        persistDockPreference(dock);
    },
    reset: () => {
        const pending = get().prefillRequest;
        if (pending) settleWaiter(pending.id, false);
        // 新账号重新挂载面板；若代码块仍然失败，错误边界会再次标记。
        set({ open: false, canvas: null, canvasHeld: false, panelUnavailable: false, prefillRequest: null });
    },
}));

/** 停靠判断用的“在画布里”：有画布上下文，或仍在画布路由且之前发布过（加载 / 切换画布的空档）。 */
export function selectAgentOnCanvas(state: Pick<GlobalAgentStore, "canvas" | "canvasRoute" | "canvasHeld">) {
    return state.canvas !== null || (state.canvasRoute && state.canvasHeld);
}

/**
 * 面板代码块加载失败时由布局层的错误边界调用：结束正在等待的投递，并让之后的投递立即返回 false，
 * 调用方不会因为一条无人消费的请求而永远被“已有请求”拒绝。
 */
export function markAgentPanelUnavailable() {
    const pending = useGlobalAgentStore.getState().prefillRequest;
    if (pending) settleWaiter(pending.id, false);
    useGlobalAgentStore.setState({ panelUnavailable: true, prefillRequest: null });
}

/**
 * 画布页向全局面板投递一次输入（替代旧的 ref.sendPrompt）。返回面板处理结果。
 * 与旧 sendPrompt 一样先到先得：已有请求尚未被面板取走时，新请求直接按 false 结束，重复点击不会重复提交。
 */
export function requestAgentPrefill(input: AgentPrefillInput): Promise<AgentPrefillOutcome> {
    const state = useGlobalAgentStore.getState();
    if (state.panelUnavailable || state.prefillRequest) return Promise.resolve(false);
    const request: AgentPrefillRequest = { ...input, id: ++prefillSequence };
    const outcome = new Promise<AgentPrefillOutcome>((resolve) => prefillWaiters.set(request.id, resolve));
    useGlobalAgentStore.setState({ prefillRequest: request });
    return outcome;
}

/** 面板取走请求：从 store 移除，之后同一 id 不会再被看到。 */
export function takeAgentPrefill(id: number): AgentPrefillRequest | null {
    const current = useGlobalAgentStore.getState().prefillRequest;
    if (!current || current.id !== id) return null;
    useGlobalAgentStore.setState({ prefillRequest: null });
    return current;
}

export function settleAgentPrefill(id: number, outcome: AgentPrefillOutcome) {
    settleWaiter(id, outcome);
}
