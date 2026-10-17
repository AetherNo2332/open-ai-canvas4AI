import { describe, expect, test } from "bun:test";
import { healthPresentation, startHealthPolling, diagnosticExport, runDrainOperation, snapshotListQuery, snapshotSearchParams } from "../src/pages/admin/components/tool-snapshot-state";
import { readFileSync } from "node:fs";

const health: any = { generatedAt: "now", backend: { id: "backend" }, workers: [{ instanceId: "w", status: "matched", online: true }], alerts: [], affectedRuns: 0 };
describe("tool snapshot current health", () => {
    test("partial impact checks and changed disk metadata cannot appear healthy", () => {
        expect(healthPresentation({ ...health, truncated: true }, "").type).toBe("warning");
        expect(healthPresentation({ ...health, workers: [{ ...health.workers[0], artifactFileState: "changed" }] }, "").type).toBe("warning");
    });
    test("metadata pagination preserves deep links and filter changes reset to first page", () => {
        const params = new URLSearchParams("snapshotPage=8&snapshotTool=drawing&snapshotSource=task&snapshotVersion=abc&userId=admin");
        expect(snapshotListQuery(params)).toEqual({ page: 8, pageSize: 20, source: "task", tool: "drawing", version: "abc" });
        const filtered = snapshotSearchParams(params, { snapshotTool: "media" });
        expect(filtered.get("snapshotPage")).toBe("1");
        expect(filtered.get("userId")).toBe("admin");
        expect(snapshotSearchParams(filtered, { snapshotPage: "9" }).get("snapshotPage")).toBe("9");
        expect(snapshotListQuery(new URLSearchParams("snapshotPage=-2")).page).toBe(1);
    });
    test("tool-name filtering never fans out full definition requests", () => {
        const view = readFileSync(new URL("../src/pages/admin/components/tool-snapshots-panel.tsx", import.meta.url), "utf8");
        expect(view).not.toContain("snapshots.map((s) => getToolSnapshot");
        expect(view).toContain("listToolSnapshots(listQuery, controller.signal)");
        expect(view).toContain("current: page");
    });
    test("legacy revision zero and oversized drain reasons are rejected before writes", async () => {
        await expect(
            runDrainOperation(
                { ...health.workers[0], revision: 0 },
                true,
                "upgrade",
                async () => {},
                async () => {},
            ),
        ).rejects.toThrow();
        await expect(
            runDrainOperation(
                { ...health.workers[0], revision: 4 },
                true,
                "x".repeat(501),
                async () => {},
                async () => {},
            ),
        ).rejects.toThrow();
    });
    test("current health mounts before analytics filters and uses its independent endpoint", () => {
        const overview = readFileSync(new URL("../src/pages/admin/components/analytics-panel.tsx", import.meta.url), "utf8");
        const api = readFileSync(new URL("../src/services/api/tool-snapshots.ts", import.meta.url), "utf8");
        expect(overview.indexOf("<ToolSnapshotsPanel />")).toBeLessThan(overview.indexOf("<ListToolbar"));
        expect(api).toContain("/tool-snapshot-health");
        expect(api).not.toContain("AnalyticsFilters");
    });
    test("missing, offline, failed and description drift never appear healthy", () => {
        expect(healthPresentation(null, "").type).toBe("warning");
        expect(healthPresentation(health, "network").type).toBe("warning");
        expect(healthPresentation({ ...health, workers: [] }, "").type).toBe("warning");
        expect(healthPresentation({ ...health, workers: [{ online: false, status: "offline" }] }, "").type).toBe("warning");
        expect(healthPresentation({ ...health, workers: [{ online: true, status: "mismatch", differences: [{ path: "/description" }] }] }, "").type).toBe("warning");
        expect(healthPresentation(health, "").type).toBe("success");
    });
    test("polling pauses hidden, resumes immediately and aborts superseded requests", async () => {
        let visible = true;
        let tick = () => {};
        let changed = () => {};
        const signals: AbortSignal[] = [];
        const stop = startHealthPolling({
            visible: () => visible,
            listen: (fn) => {
                changed = fn;
                return () => {};
            },
            interval: (fn, ms) => {
                expect(ms).toBe(15000);
                tick = fn;
                return () => {};
            },
            load: async (signal) => {
                signals.push(signal);
                return health;
            },
            update: () => {},
        });
        expect(signals).toHaveLength(1);
        tick();
        expect(signals[0].aborted).toBe(true);
        visible = false;
        changed();
        tick();
        expect(signals).toHaveLength(2);
        visible = true;
        changed();
        expect(signals).toHaveLength(3);
        stop();
        expect(signals[2].aborted).toBe(true);
    });
    test("diagnostics whitelist excludes environment, tokens and run contents", () => {
        const exported = JSON.stringify(diagnosticExport({ ...health, token: "secret", environment: { SECRET: "secret" }, workers: [{ ...health.workers[0], userContent: "secret" }] }, []));
        expect(exported).not.toContain("secret");
        expect(exported).toContain("backend");
    });
    test("failed reads clear old health and late aborted reads cannot overwrite", async () => {
        let tick = () => {};
        let resolveOld: (value: any) => void = () => {};
        const updates: any[] = [];
        let calls = 0;
        const stop = startHealthPolling({
            visible: () => true,
            listen: () => () => {},
            interval: (fn) => {
                tick = fn;
                return () => {};
            },
            load: () =>
                ++calls === 1
                    ? new Promise((resolve) => {
                          resolveOld = resolve;
                      })
                    : Promise.reject(new Error("unavailable")),
            update: (value, error) => updates.push([value, error]),
        });
        tick();
        await Promise.resolve();
        expect(updates).toEqual([[null, "unavailable"]]);
        resolveOld(health);
        await Promise.resolve();
        expect(updates).toHaveLength(1);
        stop();
    });
    test("drain requires reason, sends revision and refetches both success and conflict", async () => {
        let refreshes = 0;
        let payload: any;
        await expect(
            runDrainOperation(
                health.workers[0],
                true,
                " ",
                async () => {},
                async () => {
                    refreshes++;
                },
            ),
        ).rejects.toThrow();
        await runDrainOperation(
            { ...health.workers[0], revision: 4 },
            true,
            "upgrade",
            async (_id, body) => {
                payload = body;
            },
            async () => {
                refreshes++;
            },
        );
        expect(payload).toEqual({ expectedRevision: 4, draining: true, reason: "upgrade" });
        expect(refreshes).toBe(1);
        await expect(
            runDrainOperation(
                { ...health.workers[0], revision: 4 },
                false,
                "resume",
                async () => {
                    throw new Error("conflict");
                },
                async () => {
                    refreshes++;
                },
            ),
        ).rejects.toThrow("conflict");
        expect(refreshes).toBe(2);
    });
});
