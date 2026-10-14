export type AgentApprovalNotification = {
    approvalId: string;
    summary?: string;
};

type NotificationOptionsWithRuntime = {
    NotificationCtor?: typeof Notification;
    visibilityState?: DocumentVisibilityState;
};

/** Notify only when an approval arrives while the canvas is out of view. */
export async function notifyAgentApproval(
    approval: AgentApprovalNotification,
    runtime: NotificationOptionsWithRuntime = {},
): Promise<boolean> {
    const NotificationCtor = runtime.NotificationCtor ?? (typeof Notification === "undefined" ? undefined : Notification);
    const visibilityState = runtime.visibilityState ?? (typeof document === "undefined" ? "visible" : document.visibilityState);
    if (!NotificationCtor || visibilityState !== "hidden" || NotificationCtor.permission === "denied") return false;

    let permission: NotificationPermission = NotificationCtor.permission;
    if (permission === "default") permission = await NotificationCtor.requestPermission();
    if (permission !== "granted") return false;

    new NotificationCtor("ArrowFX 需要审批", {
        body: approval.summary?.trim() || "Agent 等待你的审批决定",
        icon: "/agent-avatar.png",
        tag: `agent-approval-${approval.approvalId}`,
    });
    return true;
}
