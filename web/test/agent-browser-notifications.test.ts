import { expect, test } from "bun:test";

import { notifyAgentApproval } from "../src/lib/canvas/agent-browser-notifications";

test("approval notification is emitted when the page is hidden and permission is granted", async () => {
    const calls: Array<{ title: string; options?: NotificationOptions }> = [];
    class GrantedNotification {
        static permission = "granted" as NotificationPermission;
        constructor(title: string, options?: NotificationOptions) {
            calls.push({ title, options });
        }
    }

    const notified = await notifyAgentApproval({
        approvalId: "approval-1",
        summary: "准备生成角色图",
    }, { NotificationCtor: GrantedNotification as unknown as typeof Notification, visibilityState: "hidden" });

    expect(notified).toBe(true);
    expect(calls).toEqual([{ title: "ArrowFX 需要审批", options: { body: "准备生成角色图", icon: "/agent-avatar.png", tag: "agent-approval-approval-1" } }]);
});

test("approval notification does not interrupt a visible page or denied permission", async () => {
    class DeniedNotification {
        static permission = "denied" as NotificationPermission;
    }

    expect(await notifyAgentApproval({ approvalId: "approval-2" }, { NotificationCtor: DeniedNotification as unknown as typeof Notification, visibilityState: "visible" })).toBe(false);
});
