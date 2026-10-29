import { describe, expect, test } from "bun:test";

import { directorPreviewHandledStore, directorPreviewJobPending, directorPreviewOutcomeNotice, directorPreviewPending, directorPreviewRunQuery, parseDirectorOpenEvent, handleDirectorOpen, isDirectorOpenStale, runDirectorPreview, withDirectorPreviewLock } from "@/lib/canvas/agent-director-launch";
import type { AgentRun } from "@/services/api/agent";

const openEvent = { canvasId: "canvas-1", nodeId: "previs-scene-1", sceneId: "scene-1", shotId: "shot-1", action: "create_scene" };
const base = { runId: "run-1", requestId: "dpv_1" };
const memoryHandled = () => {
    const set = new Set<string>();
    return { handled: { isHandled: (id: string) => set.has(id), markHandled: (id: string) => void set.add(id) }, set };
};
const freeLock = <T,>(_name: string, fn: () => Promise<T>) => fn().then((value) => ({ acquired: true as const, value }));

describe("director preview guards", () => {
    test("hidden tab abandons without recording or submitting", async () => {
        let recorded = 0;
        const calls: unknown[] = [];
        const outcome = await runDirectorPreview({ ...base, isHidden: () => true, record: async () => (recorded++, "res-1"), submit: async (...args) => void calls.push(args) });
        expect(outcome).toBe("abandoned");
        expect(recorded).toBe(0);
        expect(calls).toEqual([]);
    });

    test("lock held by another tab skips recording and submit", async () => {
        let recorded = 0;
        const calls: unknown[] = [];
        const outcome = await runDirectorPreview({ ...base, withLock: async () => ({ acquired: false }), record: async () => (recorded++, "res-1"), submit: async (...args) => void calls.push(args) });
        expect(outcome).toBe("skipped");
        expect(recorded).toBe(0);
        expect(calls).toEqual([]);
    });

    test("run no longer waiting on this request skips recording", async () => {
        let recorded = 0;
        const outcome = await runDirectorPreview({ ...base, withLock: freeLock, isPending: async () => false, record: async () => (recorded++, "res-1"), submit: async () => {} });
        expect(outcome).toBe("skipped");
        expect(recorded).toBe(0);
    });

    test("a requestId handled earlier in this tab is not recorded again", async () => {
        const { handled } = memoryHandled();
        handled.markHandled("dpv_1");
        let recorded = 0;
        const outcome = await runDirectorPreview({ ...base, handled, record: async () => (recorded++, "res-1"), submit: async () => {} });
        expect(outcome).toBe("skipped");
        expect(recorded).toBe(0);
    });

    test("marks the request handled before recording", async () => {
        const { handled, set } = memoryHandled();
        let markedBeforeRecord = false;
        await runDirectorPreview({
            ...base,
            handled,
            withLock: freeLock,
            isPending: async () => true,
            record: async () => {
                markedBeforeRecord = set.has("dpv_1");
                return "res-1";
            },
            submit: async () => {},
        });
        expect(markedBeforeRecord).toBe(true);
    });

    test("an error submission reports a distinct outcome from a video submission", async () => {
        expect(await runDirectorPreview({ ...base, record: async () => "res-1", submit: async () => {} })).toBe("submitted");
        const outcome = await runDirectorPreview({
            ...base,
            record: async () => {
                throw new Error("录制失败");
            },
            submit: async () => {},
        });
        expect(outcome).toBe("submitted_error");
    });

    test("open events without a parsable createdAt are stale", () => {
        expect(isDirectorOpenStale(undefined)).toBe(true);
        expect(isDirectorOpenStale("not-a-date")).toBe(true);
    });

    test("read actions never open the director", () => {
        let opened = 0;
        expect(handleDirectorOpen({ ...openEvent, action: "read" }, { currentCanvasId: "canvas-1", openPrevisWorkbench: () => opened++ })).toBe("ignored");
        expect(opened).toBe(0);
    });
});

describe("pending check, lock and handled store", () => {
    const evt = (seq: number, type: string, payload: Record<string, unknown>) => ({ eventId: `run-1:${seq}`, runId: "run-1", seq, type, payload, createdAt: "2026-10-29T00:00:00Z" });
    const run = (events: ReturnType<typeof evt>[], status = "running") => ({ id: "run-1", status, events }) as unknown as AgentRun;

    test("pending only while the request has no tool result", () => {
        const requested = evt(5, "director_preview_requested", { requestId: "dpv_1", callId: "call-1" });
        expect(directorPreviewPending(run([requested]), "dpv_1")).toBe(true);
        expect(directorPreviewPending(run([requested, evt(6, "tool_completed", { callId: "call-1", toolName: "director" })]), "dpv_1")).toBe(false);
        expect(directorPreviewPending(run([requested, evt(6, "tool_failed", { callId: "call-1", toolName: "director" })]), "dpv_1")).toBe(false);
        expect(directorPreviewPending(run([requested], "completed"), "dpv_1")).toBe(false);
        expect(directorPreviewPending(run([]), "dpv_1")).toBe(false);
        expect(directorPreviewPending(run([evt(5, "director_preview_requested", { requestId: "other", callId: "call-1" })]), "dpv_1")).toBe(false);
    });

    test("pending lookup pages from the director_open seq so an old request event is still found", async () => {
        const open = parseDirectorOpenEvent({ type: "director_open", runId: "run-1", seq: 5, payload: { ...openEvent, action: "preview", requestId: "dpv_1" } });
        expect(open?.seq).toBe(5);
        expect(directorPreviewRunQuery({ openSeq: open?.seq })).toEqual({ sinceSeq: 4, eventLimit: 500 });
        // No seq: read from the start rather than the default tail.
        expect(directorPreviewRunQuery({})).toEqual({ fromStart: true, eventLimit: 500 });

        // Request at seq 6 followed by 60 unrelated events: the default 40-event tail would miss it.
        const all = [evt(5, "director_open", { requestId: "dpv_1" }), evt(6, "director_preview_requested", { requestId: "dpv_1", callId: "call-1" }), ...Array.from({ length: 60 }, (_, i) => evt(7 + i, "assistant_delta", {}))];
        const requests: unknown[] = [];
        const fetchRun = async (runId: string, options: { sinceSeq?: number; eventLimit?: number; fromStart?: boolean }) => {
            requests.push({ runId, ...options });
            const events = options.sinceSeq ? all.filter((e) => e.seq > options.sinceSeq!).slice(0, options.eventLimit) : options.fromStart ? all.slice(0, options.eventLimit) : all.slice(-40);
            return { run: run(events) };
        };
        expect(directorPreviewPending(run(all.slice(-40)), "dpv_1")).toBe(false);
        expect(await directorPreviewJobPending({ runId: "run-1", requestId: "dpv_1", openSeq: open?.seq }, fetchRun)).toBe(true);
        expect(requests).toEqual([{ runId: "run-1", sinceSeq: 4, eventLimit: 500 }]);

        // A later completion inside the page still ends the wait.
        all.push(evt(70, "tool_completed", { callId: "call-1", toolName: "director" }));
        expect(await directorPreviewJobPending({ runId: "run-1", requestId: "dpv_1", openSeq: 5 }, fetchRun)).toBe(false);
    });

    test("navigator.locks ifAvailable grants a single holder", async () => {
        let held = false;
        const locks = {
            request: async (_name: string, _options: { ifAvailable: boolean }, callback: (lock: object | null) => Promise<unknown>) => {
                if (held) return callback(null);
                held = true;
                try {
                    return await callback({});
                } finally {
                    held = false;
                }
            },
        };
        let release!: () => void;
        const first = withDirectorPreviewLock("dpv_1", () => new Promise<string>((resolve) => (release = () => resolve("a"))), { locks });
        const second = await withDirectorPreviewLock("dpv_1", async () => "b", { locks });
        expect(second).toEqual({ acquired: false });
        release();
        expect(await first).toEqual({ acquired: true, value: "a" });
    });

    test("localStorage lease fallback rejects a live lease from another tab", async () => {
        const data = new Map<string, string>();
        const storage = { getItem: (key: string) => data.get(key) ?? null, setItem: (key: string, value: string) => void data.set(key, value), removeItem: (key: string) => void data.delete(key) };
        data.set("director-preview-lock:dpv_1", JSON.stringify({ owner: "other-tab", expiresAt: Date.now() + 60_000 }));
        expect(await withDirectorPreviewLock("dpv_1", async () => "x", { storage, owner: "me" })).toEqual({ acquired: false });
        data.set("director-preview-lock:dpv_1", JSON.stringify({ owner: "other-tab", expiresAt: Date.now() - 1 }));
        expect(await withDirectorPreviewLock("dpv_1", async () => "x", { storage, owner: "me" })).toEqual({ acquired: true, value: "x" });
        expect(data.has("director-preview-lock:dpv_1")).toBe(false);
    });

    test("sessionStorage handled set survives reload and tolerates blocked storage", () => {
        const data = new Map<string, string>();
        const storage = { getItem: (key: string) => data.get(key) ?? null, setItem: (key: string, value: string) => void data.set(key, value) };
        const handled = directorPreviewHandledStore(storage);
        expect(handled.isHandled("dpv_1")).toBe(false);
        handled.markHandled("dpv_1");
        expect(directorPreviewHandledStore(storage).isHandled("dpv_1")).toBe(true);
        const broken = directorPreviewHandledStore({
            getItem: () => {
                throw new Error("blocked");
            },
            setItem: () => {
                throw new Error("blocked");
            },
        });
        expect(() => broken.markHandled("dpv_2")).not.toThrow();
        expect(broken.isHandled("dpv_2")).toBe(true);
    });
});

describe("preview outcome notice", () => {
    test("only a submitted video is reported as success", () => {
        expect(directorPreviewOutcomeNotice("submitted")?.level).toBe("success");
        expect(directorPreviewOutcomeNotice("submitted_error")).toEqual({ level: "warning", text: "预演录制失败，已把失败原因告知 Agent" });
        expect(directorPreviewOutcomeNotice("expired")?.level).toBe("warning");
        expect(directorPreviewOutcomeNotice("abandoned")).toEqual({ level: "info", text: "未录制导演台预演（页面在后台或导演台已关闭）；Agent 将在超时后收到可重试的提示" });
        expect(directorPreviewOutcomeNotice("skipped")).toBeNull();
    });
});
