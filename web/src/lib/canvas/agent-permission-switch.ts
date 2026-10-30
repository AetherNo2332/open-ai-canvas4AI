import type { AgentPermissionMode, AgentRun } from "@/services/api/agent";

const ACTIVE = new Set<AgentRun["status"]>(["queued", "running", "waiting_approval"]);

/** 在当前画布上有进行中的运行时，切换权限要同步到这一轮；否则只影响下一轮。 */
export function agentPermissionTargetRun(run: Pick<AgentRun, "id" | "status"> | null | undefined, onCanvas: boolean): string | null {
    if (!onCanvas || !run?.id || !ACTIVE.has(run.status)) return null;
    return run.id;
}

export type AgentPermissionSwitchDeps = {
    change: (runId: string, input: { permissionMode: AgentPermissionMode; requestId: string }) => Promise<{ permissionMode: AgentPermissionMode; changed: boolean }>;
    setMode: (mode: AgentPermissionMode) => void;
    requestId: () => string;
    onApplied: () => void;
    onFailed: (cause: unknown) => void;
};

/**
 * 切换执行权限。控件先乐观切换；有进行中的运行时调用接口，成功提示「已在本轮生效」，
 * 失败把控件退回原值并交由调用方按 ApiError.reason 提示。没有进行中的运行时不发请求。
 */
export async function switchAgentPermission(next: AgentPermissionMode, previous: AgentPermissionMode, run: Pick<AgentRun, "id" | "status"> | null | undefined, onCanvas: boolean, deps: AgentPermissionSwitchDeps): Promise<void> {
    deps.setMode(next);
    const runId = agentPermissionTargetRun(run, onCanvas);
    if (!runId || next === previous) return;
    try {
        const result = await deps.change(runId, { permissionMode: next, requestId: deps.requestId() });
        deps.setMode(result.permissionMode);
        deps.onApplied();
    } catch (cause) {
        deps.setMode(previous);
        deps.onFailed(cause);
    }
}

const LABELS: Record<AgentPermissionMode, string> = { read_only: "只读", request_approval: "请求审批", auto: "自动执行" };

/** permission_changed 事件在时间线上的说明行。 */
export function agentPermissionChangedText(from: unknown, to: unknown): string {
    const label = (value: unknown) => LABELS[value as AgentPermissionMode] || String(value || "未知");
    return `执行权限已从「${label(from)}」改为「${label(to)}」，从下一次工具调用起生效`;
}

export function agentPermissionModeValue(value: unknown): AgentPermissionMode | null {
    return value === "read_only" || value === "request_approval" || value === "auto" ? value : null;
}
