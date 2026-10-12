import { describe, expect, test } from "bun:test";
import { createPrevisSaveCoordinator, type FlushFunction, type StorageLike } from "../src/lib/canvas/previs/previs-save";
import { previsSceneToDirectorDocument } from "../src/integrations/director-desk/previs-adapter";
import type { DirectorProject } from "../src/integrations/director-desk/previs-runtime-types";
import { createDirectorEditBridge, createDirectorCommitTransport, type DirectorCommitRecord } from "../src/integrations/director-desk/edit-bridge";
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
    id: "scene-1",
    version: 1,
    title: "Director Bridge Scene",
    background: "#123456",
    environmentIntensity: 0.5,
    gridVisible: true,
    objects: [
        {
            id: "actor-1",
            name: "主角",
            kind: "actor",
            primitive: "character",
            transform: { position: [1, 0, 2], rotation: [0, 0, 0], scale: [1, 1, 1] },
            color: "#336699",
            visible: true,
            castShadow: true,
            receiveShadow: true,
            keyframes: [],
        },
    ],
    cameras: [
        {
            id: "cam-1",
            name: "主机位",
            transform: { position: [0, 1.6, 5], rotation: [0, 0, 0], scale: [1, 1, 1] },
            target: [0, 1, 0],
            focalLength: 35,
            fov: 50,
            aperture: 2.8,
            focusDistance: 5,
            near: 0.05,
            far: 500,
            keyframes: [],
        },
    ],
    lights: [],
    shots: [
        {
            id: "shot-1",
            name: "镜头 1",
            cameraId: "cam-1",
            duration: 4,
            fps: 24,
            shotSize: "medium",
            cameraMove: "static",
            prompt: "测试镜头",
        },
    ],
    activeShotId: "shot-1",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
});

type CoordinatorHarness = {
    requests: Array<{ scene: PrevisScene; revision: number; baseUpdatedAt: string }>;
    setFlushBehavior: (next: FlushFunction) => void;
    scheduler: ManualScheduler;
    coordinator: ReturnType<typeof createPrevisSaveCoordinator>;
    flushMicrotasks: () => Promise<void>;
    dispose: () => void;
};

const createHarness = (initialScene: PrevisScene = makeScene()): CoordinatorHarness => {
    const scheduler = new ManualScheduler();
    let now = 1000;
    const requests: CoordinatorHarness["requests"] = [];
    let flushBehavior: FlushFunction = async () => undefined;

    const coordinator = createPrevisSaveCoordinator({
        initialScene,
        scope: "test-user",
        storage: new MemoryStorage(),
        flush: async (request) => {
            requests.push(structuredClone(request));
            await flushBehavior(request);
        },
        now: () => now++,
        debounceMs: 25,
        schedule: (callback, delay) => scheduler.schedule(callback, delay),
        cancelSchedule: (handle) => scheduler.cancel(handle),
    });

    return {
        requests,
        setFlushBehavior: (next) => {
            flushBehavior = next;
        },
        scheduler,
        coordinator,
        flushMicrotasks: async () => {
            scheduler.runAll();
            await Promise.resolve();
            await Promise.resolve();
        },
        dispose: () => {
            coordinator.dispose();
            scheduler.runAll();
        },
    };
};

type EventLog = { succeeded: DirectorCommitRecord[]; conflicted: DirectorCommitRecord[] };

const createEvents = (): EventLog => ({ succeeded: [], conflicted: [] });

const expectRecord = (record: DirectorCommitRecord | undefined, partial: Partial<DirectorCommitRecord>): void => {
    expect(record).toMatchObject(partial);
};

describe("DirectorDesk edit bridge — runtime commits through the save coordinator", () => {
    test("01 transform commit converts the document, tracks a revision and confirms as succeeded", async () => {
        const h = createHarness();
        const transport = createDirectorCommitTransport(h.coordinator);
        const events = createEvents();
        const bridge = createDirectorEditBridge(transport, {
            expectedSceneId: "scene-1",
            onCommitSucceeded: (record) => events.succeeded.push(record),
            onCommitConflict: (record) => events.conflicted.push(record),
        });

        const scene = makeScene();
        const document = previsSceneToDirectorDocument(scene);
        // Simulate a transform commit from the engine: the entity moved.
        document.entities[0].position = [4, 0, 7];

        const record = bridge.commitDocument(document);
        expectRecord(record, { status: "pending", revision: 1 });
        expect(record.scene?.id).toBe("scene-1");
        expect(record.scene?.objects[0].transform.position).toEqual([4, 0, 7]);
        expect(bridge.getRecord()).toBe(record);

        await h.flushMicrotasks();

        expect(h.requests).toHaveLength(1);
        expect(h.requests[0].scene.objects[0].transform.position).toEqual([4, 0, 7]);
        expect(events.succeeded).toHaveLength(1);
        expect(events.conflicted).toHaveLength(0);
        expect(bridge.getRecord()).toMatchObject({ status: "succeeded", revision: 1 });
    });

    test("02 failed save reports a conflict, and a newer commit confirms after the retry succeeds", async () => {
        const h = createHarness();
        const transport = createDirectorCommitTransport(h.coordinator);
        const events = createEvents();
        const bridge = createDirectorEditBridge(transport, {
            expectedSceneId: "scene-1",
            onCommitSucceeded: (record) => events.succeeded.push(record),
            onCommitConflict: (record) => events.conflicted.push(record),
        });

        h.setFlushBehavior(async () => {
            throw new Error("remote rejected");
        });

        const first = bridge.commitDocument(previsSceneToDirectorDocument(makeScene({ title: "first" })));
        expectRecord(first, { status: "pending", revision: 1 });
        await h.flushMicrotasks();
        expect(events.conflicted).toHaveLength(1);
        expect(events.conflicted[0]).toMatchObject({ status: "conflict", conflictReason: "save-failed" });
        expect(bridge.getRecord()).toMatchObject({ status: "conflict", conflictReason: "save-failed" });

        // Retry: the host re-commits (e.g. the runtime pushes the same document again).
        const retried = bridge.commitDocument(previsSceneToDirectorDocument(makeScene({ title: "first" })));
        expectRecord(retried, { status: "pending", revision: 2 });
        h.setFlushBehavior(async () => undefined);
        await h.flushMicrotasks();
        expect(events.succeeded).toHaveLength(1);
        expect(bridge.getRecord()).toMatchObject({ status: "succeeded", revision: 2 });
    });

    test("03 a document without the canvas extension is a translation conflict and never touches the coordinator", async () => {
        const h = createHarness();
        const transport = createDirectorCommitTransport(h.coordinator);
        const events = createEvents();
        const bridge = createDirectorEditBridge(transport, {
            expectedSceneId: "scene-1",
            onCommitSucceeded: (record) => events.succeeded.push(record),
            onCommitConflict: (record) => events.conflicted.push(record),
        });

        const document = previsSceneToDirectorDocument(makeScene());
        const stripped = structuredClone(document) as Record<string, unknown>;
        delete stripped.extensions;

        const record = bridge.commitDocument(stripped as unknown as DirectorProject);
        expectRecord(record, { status: "conflict", conflictReason: "translation" });
        expect(record.scene).toBeUndefined();
        expect(h.requests).toHaveLength(0);
        expect(h.coordinator.getSnapshot().revision).toBe(0);
        expect(events.conflicted).toHaveLength(1);
        expect(events.succeeded).toHaveLength(0);
    });

    test("04 a document for another scene is rejected without touching the coordinator", () => {
        const h = createHarness();
        const transport = createDirectorCommitTransport(h.coordinator);
        const events = createEvents();
        const bridge = createDirectorEditBridge(transport, {
            expectedSceneId: "scene-1",
            onCommitSucceeded: (record) => events.succeeded.push(record),
            onCommitConflict: (record) => events.conflicted.push(record),
        });

        const document = previsSceneToDirectorDocument(makeScene({ id: "scene-other", shots: [] }));

        const record = bridge.commitDocument(document);
        expectRecord(record, { status: "conflict", conflictReason: "rejected" });
        expect(h.requests).toHaveLength(0);
        expect(h.coordinator.getSnapshot().revision).toBe(0);
        expect(events.conflicted).toHaveLength(1);
    });

    test("05 a transport that refuses the commit is reported as rejected", () => {
        const h = createHarness();
        const events = createEvents();
        const bridge = createDirectorEditBridge(
            {
                commit: () => false,
                snapshot: () => h.coordinator.getSnapshot(),
                onStatus: () => () => undefined,
            },
            {
                expectedSceneId: "scene-1",
                onCommitSucceeded: (record) => events.succeeded.push(record),
                onCommitConflict: (record) => events.conflicted.push(record),
            },
        );

        const record = bridge.commitDocument(previsSceneToDirectorDocument(makeScene()));
        expectRecord(record, { status: "conflict", conflictReason: "rejected" });
        expect(events.conflicted).toHaveLength(1);
        expect(h.requests).toHaveLength(0);
    });

    test("06 rapid commits settle into one confirmed success for the latest revision", async () => {
        const h = createHarness();
        const transport = createDirectorCommitTransport(h.coordinator);
        const events = createEvents();
        const bridge = createDirectorEditBridge(transport, {
            expectedSceneId: "scene-1",
            onCommitSucceeded: (record) => events.succeeded.push(record),
            onCommitConflict: (record) => events.conflicted.push(record),
        });

        const first = makeScene();
        const second = makeScene({ title: "second" });
        const firstDoc = previsSceneToDirectorDocument(first);
        const secondDoc = previsSceneToDirectorDocument(second);
        firstDoc.entities[0].position = [1, 0, 1];
        secondDoc.entities[0].position = [2, 0, 2];

        bridge.commitDocument(firstDoc);
        const settled = bridge.commitDocument(secondDoc);
        expectRecord(settled, { status: "pending", revision: 2 });

        await h.flushMicrotasks();

        expect(h.requests).toHaveLength(1);
        expect(h.requests[0].scene.title).toBe("second");
        expect(events.succeeded).toHaveLength(1);
        expect(events.succeeded[0]).toMatchObject({ status: "succeeded", revision: 2 });
        expect(bridge.getRecord()).toMatchObject({ status: "succeeded", revision: 2 });
    });

    test("07 dispose stops outcome delivery and clears the pending record", async () => {
        const h = createHarness();
        const transport = createDirectorCommitTransport(h.coordinator);
        const events = createEvents();
        const bridge = createDirectorEditBridge(transport, {
            expectedSceneId: "scene-1",
            onCommitSucceeded: (record) => events.succeeded.push(record),
            onCommitConflict: (record) => events.conflicted.push(record),
        });

        bridge.commitDocument(previsSceneToDirectorDocument(makeScene()));
        expect(bridge.getRecord()).toMatchObject({ status: "pending" });

        bridge.dispose();
        expect(bridge.getRecord()).toBeNull();

        h.scheduler.runAll();
        await Promise.resolve();
        expect(events.succeeded).toHaveLength(0);
        expect(events.conflicted).toHaveLength(0);

        // Calling commit after dispose is inert: it must not allocate a new
        // revision beyond the pre-dispose commit (which legitimately got 1).
        const revisionAfterDispose = h.coordinator.getSnapshot().revision;
        expect(revisionAfterDispose).toBe(1);
        const after = bridge.commitDocument(previsSceneToDirectorDocument(makeScene()));
        expectRecord(after, { status: "conflict", conflictReason: "rejected" });
        expect(h.coordinator.getSnapshot().revision).toBe(revisionAfterDispose);
    });
});