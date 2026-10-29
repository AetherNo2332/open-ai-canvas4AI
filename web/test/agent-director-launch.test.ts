import { describe, expect, test } from "bun:test";

import { DirectorPreviewAbandonedError, directorOpenTargetReady, directorPreviewErrorMessage, directorPreviewVideoResourceId, isDirectorOpenStale, directorToolAction, handleDirectorOpen, parseDirectorOpenEvent, runDirectorPreview, waitForDirectorViewport } from "@/lib/canvas/agent-director-launch";
import { ApiError } from "@/services/api/request";

const openEvent = { canvasId: "canvas-1", nodeId: "previs-scene-1", sceneId: "scene-1", shotId: "shot-1", action: "create_scene" };

describe("handleDirectorOpen", () => {
    test("opens the director for the current canvas", () => {
        const opened: string[] = [];
        expect(handleDirectorOpen(openEvent, { currentCanvasId: "canvas-1", openPrevisWorkbench: (id) => opened.push(id) })).toBe("opened");
        expect(opened).toEqual(["previs-scene-1"]);
    });

    test("ignores events for another canvas", () => {
        const opened: string[] = [];
        expect(handleDirectorOpen(openEvent, { currentCanvasId: "canvas-2", openPrevisWorkbench: (id) => opened.push(id) })).toBe("ignored");
        expect(opened).toEqual([]);
    });

    test("does not reopen a node that is already open", () => {
        const opened: string[] = [];
        let openNodeId: string | null = null;
        const deps = {
            currentCanvasId: "canvas-1",
            get openNodeId() {
                return openNodeId;
            },
            openPrevisWorkbench: (id: string) => {
                opened.push(id);
                openNodeId = id;
            },
        };
        expect(handleDirectorOpen(openEvent, deps)).toBe("opened");
        expect(handleDirectorOpen({ ...openEvent, action: "preview", requestId: "dpv_1" }, deps)).toBe("ignored");
        expect(opened).toEqual(["previs-scene-1"]);
    });

    test("ignores events without a target node", () => {
        expect(handleDirectorOpen({ ...openEvent, nodeId: "" }, { currentCanvasId: "canvas-1", openPrevisWorkbench: () => {} })).toBe("ignored");
    });
});

describe("director_open parsing and readiness", () => {
    test("parses a director_open run event and rejects other events", () => {
        expect(parseDirectorOpenEvent({ type: "director_open", runId: "run-1", payload: { ...openEvent, action: "preview", requestId: "dpv_1" } })).toEqual({ runId: "run-1", ...openEvent, action: "preview", requestId: "dpv_1" });
        expect(parseDirectorOpenEvent({ type: "tool_completed", runId: "run-1", payload: openEvent })).toBeNull();
        expect(parseDirectorOpenEvent({ type: "director_open", runId: "run-1", payload: { ...openEvent, sceneId: 3 } })).toBeNull();
    });

    test("waits until the bound node and scene have synced", () => {
        const node = { id: "previs-scene-1", metadata: { previsSceneId: "scene-1" } };
        expect(directorOpenTargetReady(openEvent, [], [{ id: "scene-1" }])).toBe(false);
        expect(directorOpenTargetReady(openEvent, [node], [])).toBe(false);
        expect(directorOpenTargetReady(openEvent, [{ ...node, metadata: { previsSceneId: "other" } }], [{ id: "scene-1" }])).toBe(false);
        expect(directorOpenTargetReady(openEvent, [node], [{ id: "scene-1" }])).toBe(true);
    });

    test("reads the director action from tool arguments", () => {
        expect(directorToolAction({ arguments: JSON.stringify({ action: "apply_patch" }) })).toBe("apply_patch");
        expect(directorToolAction({ arguments: { action: "read" } })).toBe("read");
        expect(directorToolAction({ arguments: "{bad" })).toBe("");
    });
});

describe("runDirectorPreview", () => {
    test("submits the recorded video resource exactly once", async () => {
        const calls: unknown[] = [];
        const outcome = await runDirectorPreview({
            runId: "run-1",
            requestId: "dpv_1",
            record: async () => "res-video-1",
            submit: async (...args) => {
                calls.push(args);
            },
        });
        expect(outcome).toBe("submitted");
        expect(calls).toEqual([["run-1", "dpv_1", { videoResourceId: "res-video-1" }]]);
    });

    test("submits { error } when recording fails", async () => {
        const calls: unknown[] = [];
        await runDirectorPreview({
            runId: "run-1",
            requestId: "dpv_1",
            record: async () => {
                throw new Error("录制已中止：请保持预演台标签页可见\n第二行");
            },
            submit: async (...args) => {
                calls.push(args);
            },
        });
        expect(calls).toEqual([["run-1", "dpv_1", { error: "录制已中止：请保持预演台标签页可见 第二行" }]]);
    });

    test("treats an expired request as settled instead of failing", async () => {
        const outcome = await runDirectorPreview({
            runId: "run-1",
            requestId: "dpv_1",
            record: async () => "res-video-1",
            submit: async () => {
                throw new ApiError("导演台预演请求不存在或已过期", { status: 404, code: 404, reason: "director_preview_not_found" });
            },
        });
        expect(outcome).toBe("expired");
    });

    test("propagates other submit failures", async () => {
        await expect(
            runDirectorPreview({
                runId: "run-1",
                requestId: "dpv_1",
                record: async () => "res-video-1",
                submit: async () => {
                    throw new ApiError("服务不可用", { status: 503, code: 503 });
                },
            }),
        ).rejects.toThrow("服务不可用");
    });

    test("does not submit when the director was closed before recording", async () => {
        const calls: unknown[] = [];
        const outcome = await runDirectorPreview({
            runId: "run-1",
            requestId: "dpv_1",
            record: async () => {
                throw new DirectorPreviewAbandonedError();
            },
            submit: async (...args) => {
                calls.push(args);
            },
        });
        expect(outcome).toBe("abandoned");
        expect(calls).toEqual([]);
    });

    test("only accepts a server resource as the preview video", () => {
        expect(directorPreviewVideoResourceId("resource:res-1")).toBe("res-1");
        expect(() => directorPreviewVideoResourceId("previs-clay:user:abc")).toThrow("预演视频未上传到服务端");
        expect(() => directorPreviewVideoResourceId(undefined)).toThrow("预演视频未上传到服务端");
    });

    test("ignores replayed open events older than the preview timeout", () => {
        const now = Date.parse("2026-10-29T00:10:00Z");
        expect(isDirectorOpenStale("2026-10-29T00:09:30Z", now)).toBe(false);
        expect(isDirectorOpenStale("2026-10-29T00:00:00Z", now)).toBe(true);
        expect(isDirectorOpenStale(undefined, now)).toBe(false);
    });

    test("limits error text to the backend contract", () => {
        expect(directorPreviewErrorMessage(new Error("x".repeat(600)))).toHaveLength(500);
        expect(directorPreviewErrorMessage("bad\u0000value")).toBe("bad value");
        expect(directorPreviewErrorMessage(undefined)).toBe("导演台预演录制失败");
    });
});

describe("waitForDirectorViewport", () => {
    test("resolves once the viewport reports ready", async () => {
        let checks = 0;
        await waitForDirectorViewport(() => ++checks >= 3, { timeoutMs: 1000, intervalMs: 1 });
        expect(checks).toBe(3);
    });

    test("rejects after the timeout", async () => {
        await expect(waitForDirectorViewport(() => false, { timeoutMs: 5, intervalMs: 1 })).rejects.toThrow("导演台 3D 视口未就绪");
    });
});
