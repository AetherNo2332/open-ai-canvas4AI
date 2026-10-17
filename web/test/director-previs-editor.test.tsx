import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";
import { DirectorPrevisEditor } from "../src/components/canvas/previs/director-previs-editor";
import { createPrevisSaveCoordinator, type FlushFunction, type StorageLike } from "../src/lib/canvas/previs/previs-save";
import { idlePrevisSaveProgress, type PrevisSaveProgress } from "../src/lib/canvas/previs/previs-save-wiring";
import { decideRuntimeSync } from "../src/integrations/director-desk/scene-sync";
import { previsSceneToDirectorDocument } from "../src/integrations/director-desk/previs-adapter";
import { directorDocumentToPrevisScene } from "../src/integrations/director-desk/previs-adapter";
import type { DirectorRuntime } from "../src/integrations/director-desk";
import type { PrevisSaveController } from "../src/components/canvas/previs/use-previs-save-coordinator";
import type { PrevisScene } from "../src/types/previs";

class MemoryStorage implements StorageLike {
    private readonly values = new Map<string, string>();
    getItem(key: string): string | null {
        return this.values.get(key) ?? null;
    }
    setItem(key: string, value: string): void {
        this.values.set(key, value);
    }
    removeItem(key: string): void {
        this.values.delete(key);
    }
}

class ManualScheduler {
    private callbacks = new Map<number, () => void>();
    private id = 0;
    schedule(callback: () => void, _delay: number): unknown {
        this.callbacks.set(this.id++, callback);
        return this.id - 1;
    }
    cancel(handle: unknown): void {
        this.callbacks.delete(handle as number);
    }
    runAll(): void {
        for (const callback of [...this.callbacks.values()]) callback();
        this.callbacks.clear();
    }
}

const makeScene = (overrides: Partial<PrevisScene> = {}): PrevisScene => ({
    id: "scene-e1",
    version: 1,
    title: "编辑器场景",
    background: "#222222",
    environmentIntensity: 1,
    gridVisible: true,
    objects: [{ id: "actor-1", name: "主角", kind: "actor", primitive: "character", transform: { position: [1, 0, 2], rotation: [0, 0, 0], scale: [1, 1, 1] }, color: "#336699", visible: true, castShadow: true, receiveShadow: true, keyframes: [] }],
    cameras: [{ id: "cam-1", name: "主机位", transform: { position: [0, 1.6, 5], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1, 0], focalLength: 35, fov: 50, aperture: 2.8, focusDistance: 5, near: 0.05, far: 500, keyframes: [] }],
    lights: [],
    shots: [{ id: "shot-1", name: "镜头 1", cameraId: "cam-1", duration: 4, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" }],
    activeShotId: "shot-1",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
});

const fakeRuntime = (): DirectorRuntime => ({
    scene: { userData: {} } as DirectorRuntime["scene"],
    editorCamera: {} as DirectorRuntime["editorCamera"],
    shotCamera: {} as DirectorRuntime["shotCamera"],
    renderers: [],
    host: { stage: {} as HTMLElement, shot: {} as HTMLElement },
    disposed: false,
    viewState: { selectedEntityId: "", activeShotId: "", time: 0 },
    render: () => undefined,
    syncDocument: () => undefined,
    applyViewState: () => undefined,
    resetViewState: () => undefined,
    play: () => undefined,
    pause: () => undefined,
    isPlaying: () => false,
    setPlayhead: () => undefined,
});

const makeSaveController = (): PrevisSaveController => ({
    progress: idlePrevisSaveProgress,
    currentSnapshot: () => idlePrevisSaveProgress,
    onSaveStatus: () => () => undefined,
    commitScene: () => true,
    retry: async () => false,
    restoreCandidate: () => null,
    restoreDraft: () => false,
    discardDraft: () => false,
    prepareClose: async () => "close",
    handlePageHide: async () => true,
});

describe("DirectorPrevisEditor — 渲染合同", () => {
    const props = {
        scene: makeScene(),
        policy: "canary" as const,
        stageRef: { current: null },
        shotRef: { current: null },
        runtime: fakeRuntime(),
        runtimeStatus: "ready" as const,
        time: 2,
        playing: false,
        selectedEntityId: "actor-1",
        saveController: makeSaveController(),
        onChange: () => undefined,
        onClose: () => undefined,
    };

    test("01 全屏编辑器渲染视口/时间轴/镜头列表/检查器/保存状态/关闭", () => {
        const html = renderToStaticMarkup(createElement(DirectorPrevisEditor, props));
        expect(html).toContain('data-director-runtime="stage"');
        expect(html).toContain('data-director-runtime="shot"');
        expect(html).toContain("DirectorDesk · 灰度同步");
        expect(html).toContain("编辑器场景");
        expect(html).toContain("已保存");
        expect(html).toContain("关闭预演台");
        // 时间轴
        expect(html).toContain("播放");
        expect(html).toContain("2.0s");
        // 镜头列表
        expect(html).toContain("镜头 1");
        expect(html).toContain("新增镜头");
        // 检查器（选中 actor-1 + 活动镜头机位）
        expect(html).toContain("主角");
        expect(html).toContain("主机位");
    });

    test("02 legacy 策略标记与初始化状态可见", () => {
        const html = renderToStaticMarkup(createElement(DirectorPrevisEditor, { ...props, policy: "authoritative", runtimeStatus: "idle" }));
        expect(html).toContain('data-runtime-policy="authoritative"');
        expect(html).toContain('data-runtime-status="idle"');
        expect(html).toContain("初始化");
    });

    test("03 保存失败态显示重试入口", () => {
        const failingProgress: PrevisSaveProgress = { revision: 2, confirmedRevision: 1, status: "error", draftStored: true };
        const html = renderToStaticMarkup(
            createElement(DirectorPrevisEditor, {
                ...props,
                saveController: { ...makeSaveController(), progress: failingProgress },
            }),
        );
        expect(html).toContain("保存失败");
        expect(html).toContain("重试");
    });
});

describe("P1 验收 — 打开→编辑→保存→关闭→重开 全链路", () => {
    test("04 编辑经协调器 flush；clean 关闭；重开后引擎世界内容保真", async () => {
        const scheduler = new ManualScheduler();
        const requests: Array<{ scene: PrevisScene }> = [];
        let storedScene: PrevisScene | null = null;
        let flushBehavior: FlushFunction = async () => undefined;

        const coordinator = createPrevisSaveCoordinator({
            initialScene: makeScene(),
            scope: "user-1",
            storage: new MemoryStorage(),
            flush: async (request) => {
                requests.push(structuredClone(request));
                storedScene = structuredClone(request.scene);
                await flushBehavior(request);
            },
            now: () => Date.now(),
            debounceMs: 25,
            schedule: (callback, delay) => scheduler.schedule(callback, delay),
            cancelSchedule: (handle) => scheduler.cancel(handle),
        });

        // 打开 → 编辑（检查器改焦段）→ commitScene
        const edited: PrevisScene = { ...makeScene(), cameras: [{ ...makeScene().cameras[0], focalLength: 50 }], updatedAt: new Date().toISOString() };
        expect(coordinator.edit(edited)).toBe(true);
        scheduler.runAll();
        await Promise.resolve();
        await Promise.resolve();

        expect(requests).toHaveLength(1);
        expect(requests[0].scene.cameras[0].focalLength).toBe(50);
        expect(storedScene?.cameras[0].focalLength).toBe(50);

        // 关闭：干净（已确认）→ "close"
        await expect(coordinator.prepareClose()).resolves.toBe("close");

        // 重开：以 store 中持久化的场景为初始
        const reopened = createPrevisSaveCoordinator({
            initialScene: storedScene!,
            scope: "user-1",
            storage: new MemoryStorage(),
            flush: async (request) => {
                requests.push(structuredClone(request));
                await flushBehavior(request);
            },
            now: () => Date.now(),
            debounceMs: 25,
            schedule: (callback, delay) => scheduler.schedule(callback, delay),
            cancelSchedule: (handle) => scheduler.cancel(handle),
        });
        const snapshot = reopened.getSnapshot();
        expect(snapshot.status).toBe("saved");
        expect(storedScene!.cameras[0].focalLength).toBe(50);

        // 引擎世界保真：store 场景 → DirectorDocument → 回 PrevisScene（重开后引擎渲染的内容）
        const document = previsSceneToDirectorDocument(storedScene!);
        const roundTripped = directorDocumentToPrevisScene(document);
        const roundTrippedCamera = roundTripped.cameras.map((camera) => camera.id);
        expect(roundTrippedCamera).toContain("cam-1");
        expect(roundTripped.shots).toHaveLength(1);
        expect(roundTripped.objects[0].transform.position).toEqual([1, 0, 2]);

        // 场景修订变化 → 决策为 content（引擎重建 + 视图状态保留路径，而非新会话）
        const before = makeScene();
        const after = { ...makeScene(), updatedAt: before.updatedAt + "x" };
        expect(decideRuntimeSync(before, after).kind).toBe("content");

        coordinator.dispose();
        reopened.dispose();
        scheduler.runAll();
    });

    test("05 远端保存失败时 dirty 关闭走草稿退出：flush 失败 → offer-draft-exit", async () => {
        const scheduler = new ManualScheduler();
        const storage = new MemoryStorage();
        const coordinator = createPrevisSaveCoordinator({
            initialScene: makeScene(),
            scope: "user-1",
            storage,
            flush: async () => {
                throw new Error("remote down");
            },
            now: () => Date.now(),
            debounceMs: 25,
            schedule: (callback, delay) => scheduler.schedule(callback, delay),
            cancelSchedule: (handle) => scheduler.cancel(handle),
        });

        const edited = { ...makeScene(), title: "带草稿", updatedAt: new Date().toISOString() };
        coordinator.edit(edited);
        scheduler.runAll();
        await Promise.resolve();
        await Promise.resolve();

        const decision = await coordinator.prepareClose();
        expect(decision).toBe("offer-draft-exit");

        // 草稿已写入本地：重开同一 storage + 同一初始场景后能恢复
        const reopened = createPrevisSaveCoordinator({
            initialScene: makeScene(),
            scope: "user-1",
            storage,
            flush: async () => undefined,
            now: () => Date.now(),
            debounceMs: 25,
            schedule: (callback, delay) => scheduler.schedule(callback, delay),
            cancelSchedule: (handle) => scheduler.cancel(handle),
        });
        const candidate = reopened.restoreCandidate();
        expect(candidate).not.toBeNull();
        expect(candidate!.scene.title).toBe("带草稿");
        expect(reopened.explicitRestore(candidate!)).toBe(true);

        coordinator.dispose();
        reopened.dispose();
        scheduler.runAll();
    });
});
