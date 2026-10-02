import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { AgentRuntimeStatusView } from "../src/pages/admin/settings/components/agent-runtime-status";
import type { AgentRuntimeStatus } from "../src/services/api/admin-agent-settings";

const online: AgentRuntimeStatus = {
    instanceId: "online-worker", resident: 2, capacity: 64, claimReservations: 1,
    dispatchActive: 1, readyQueued: 3, draining: false, appliedConfigRevision: 4,
    lastHeartbeatAt: "2026-10-02T10:00:00Z", online: true,
};
test("runtime summary excludes stale offline capacity and reservations", () => {
    const html = renderToStaticMarkup(<AgentRuntimeStatusView revision={5} instances={[
        { ...online, instanceId: "offline-worker", resident: 40, claimReservations: 20, readyQueued: 10, online: false }, online,
    ]} />);
    expect(html).toContain("2 / 64");
    expect(html).toContain("1 个领取预留");
    expect(html).toContain("待应用");
    expect(html).toContain("配置修订号");
    expect(html).toContain("online-worker");
    expect(html).not.toContain("offline-worker");
    expect(html).toContain('aria-pressed="false"');
});
test("runtime view explains empty and offline-only states", () => {
    expect(renderToStaticMarkup(<AgentRuntimeStatusView instances={[]} />)).toContain("暂无执行器心跳记录");
    expect(renderToStaticMarkup(<AgentRuntimeStatusView instances={[{ ...online, online: false }]} />)).toContain("暂无在线执行器");
});
