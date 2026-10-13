import { describe, expect, test } from "bun:test";
import { createDirectorRuntime, disposeDirectorRuntime, type DirectorFrameDriver } from "@/integrations/director-desk";
import type { DirectorProject } from "@/integrations/director-desk/previs-runtime-types";

const makeDocument = (duration = 2): DirectorProject => ({
    format: "director-desk",
    version: 1,
    name: "Playback",
    duration,
    fps: 30,
    aspect: "16:9",
    room: { enabled: false, width: 1, depth: 1, height: 1 },
    entities: [],
    cuts: [],
    references: [],
    extensions: {
        "infinite-canvas": {
            sourceSceneId: "scene-1",
            scene: { id: "scene-1", title: "PB", background: "#111111", environmentIntensity: 1, gridVisible: true, activeShotId: "", createdAt: "", updatedAt: "" },
            objects: [],
            cameras: [],
            lights: [],
            shots: [],
        },
    },
});

/** 手控帧驱动：request 入队，run() 按给定时间戳依次执行。 */
const createFakeDriver = (): DirectorFrameDriver & { run: (timestamps: number[]) => void; pending: number } => {
    const queue: Array<{ handle: number; callback: (timestamp: number) => void }> = [];
    let id = 0;
    const driver = {
        request: (callback: (timestamp: number) => void) => {
            id += 1;
            queue.push({ handle: id, callback });
            return id;
        },
        cancel: (handle: number) => {
            const index = queue.findIndex((item) => item.handle === handle);
            if (index >= 0) queue.splice(index, 1);
        },
        run: (timestamps: number[]) => {
            // rAF 回调是 one-shot：每个时间戳只消费「该刻」登记的回调。
            for (const timestamp of timestamps) {
                const current = queue.splice(0, queue.length);
                for (const { callback } of current) callback(timestamp);
            }
        },
        get pending() {
            return queue.length;
        },
    };
    return driver;
};

const createRuntime = (host: Parameters<typeof createDirectorRuntime>[0]) => {
    const stage = { replaceChildren: () => undefined } as unknown as HTMLElement;
    const shot = { replaceChildren: () => undefined } as unknown as HTMLElement;
    return createDirectorRuntime({
        stage,
        shot,
        createRenderer: () => ({ dispose: () => undefined }),
        ...host,
    });
};

describe("DirectorRuntime — 播放头与播放/暂停", () => {
    test("01 setPlayhead 钳位到 [0, duration] 并通知", () => {
        const times: number[] = [];
        const runtime = createRuntime({ onTimeChange: (t) => times.push(t) });
        runtime.syncDocument(makeDocument(2));
        runtime.setPlayhead(0.5);
        runtime.setPlayhead(9);
        runtime.setPlayhead(-1);
        expect(times).toEqual([0.5, 2, 0]);
        expect(runtime.viewState.time).toBe(0);
        disposeDirectorRuntime(runtime);
    });

    test("02 play 按帧推进，越过末尾环绕回 0；pause 停止", () => {
        const driver = createFakeDriver();
        const times: number[] = [];
        const playing: boolean[] = [];
        const runtime = createRuntime({ frameDriver: driver, onTimeChange: (t) => times.push(t), onPlayingChange: (p) => playing.push(p) });
        runtime.syncDocument(makeDocument(1)); // 1 秒整片

        runtime.play();
        expect(runtime.isPlaying()).toBe(true);
        // rAF 时间戳单位是毫秒；50ms 帧（长帧 delta 会被钳到 100ms，这里不触发）。
        // 首帧 delta=0 无事件；之后每帧 +0.05s，总时长 1s → 第 20 步到达 1.0 时环绕回 0。
        driver.run(Array.from({ length: 40 }, (_, i) => (i + 1) * 50));
        expect(runtime.isPlaying()).toBe(true);
        expect(times).toHaveLength(39); // 帧 2..40，每帧 +0.05s（帧 1 delta=0 无事件）
        expect(times[0]).toBeCloseTo(0.05);
        expect(times).toContain(0); // 第 20 步到达 1.0 → 越过末尾环绕回 0
        expect(times[times.length - 1]).toBeCloseTo(0.95);

        runtime.pause();
        expect(runtime.isPlaying()).toBe(false);
        expect(driver.pending).toBe(0);
        expect(playing).toEqual([true, false]);
        disposeDirectorRuntime(runtime);
    });

    test("03 无文档/空时长时 play 无效；重复 play 不叠加循环", () => {
        const driver = createFakeDriver();
        const runtime = createRuntime({ frameDriver: driver });
        runtime.play();
        expect(runtime.isPlaying()).toBe(false);
        expect(driver.pending).toBe(0);

        runtime.syncDocument(makeDocument(1));
        runtime.play();
        runtime.play();
        expect(driver.pending).toBe(1);
        disposeDirectorRuntime(runtime);
    });

    test("04 dispose 取消播放循环", () => {
        const driver = createFakeDriver();
        const runtime = createRuntime({ frameDriver: driver });
        runtime.syncDocument(makeDocument(1));
        runtime.play();
        expect(driver.pending).toBe(1);
        disposeDirectorRuntime(runtime);
        expect(driver.pending).toBe(0);
        expect(runtime.isPlaying()).toBe(false);
    });

    test("05 syncDocument 后播放头按新时长钳位保留", () => {
        const times: number[] = [];
        const runtime = createRuntime({ onTimeChange: (t) => times.push(t) });
        runtime.syncDocument(makeDocument(10));
        runtime.setPlayhead(8);
        // 新文档只有 1 秒：播放头回落
        runtime.syncDocument(makeDocument(1));
        expect(runtime.viewState.time).toBe(1);
        disposeDirectorRuntime(runtime);
    });
});