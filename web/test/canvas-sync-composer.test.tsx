import { expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { CanvasSyncStatus } from "../src/pages/canvas/canvas-sync-status";
import { useSyncProgressStore } from "../src/stores/use-sync-progress-store";

test("real sync status has no recovery card, buttons or alternate-canvas actions", () => {
    useSyncProgressStore.getState().setProjectProgress("status-test", { phase: "pending", errorKind: "network", message: "画布同步失败：连接中断" });
    try {
        const markup = renderToStaticMarkup(<CanvasSyncStatus projectId="status-test" />);
        expect(markup).toContain('role="status"');
        expect(markup).not.toContain("<button");
        expect(markup).not.toContain("采用云端");
        expect(markup).not.toContain("另存");
        expect(markup).not.toContain("导出");
    } finally {
        useSyncProgressStore.getState().setProjectProgress("status-test", null);
    }
});
