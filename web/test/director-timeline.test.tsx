import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";
import {
    previsTimelineTotalDuration,
    resolveTimelineScrub,
    findShotAtTime,
    advancePlayhead,
} from "../src/lib/canvas/previs/previs-timeline";
import { DirectorTimeline } from "../src/components/canvas/previs/director-timeline";
import type { PrevisScene } from "../src/types/previs";

const makeScene = (overrides: Partial<PrevisScene> = {}): PrevisScene => ({
    id: "scene-tl",
    version: 1,
    title: "Timeline 场景",
    background: "#222222",
    environmentIntensity: 1,
    gridVisible: true,
    objects: [],
    cameras: [
        { id: "cam-1", name: "主机位", transform: { position: [0, 1.6, 5], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1, 0], focalLength: 35, fov: 50, aperture: 2.8, focusDistance: 5, near: 0.05, far: 500, keyframes: [] },
    ],
    lights: [],
    shots: [
        { id: "shot-1", name: "镜头 1", cameraId: "cam-1", duration: 4, fps: 24, shotSize: "wide", cameraMove: "static", prompt: "" },
        { id: "shot-2", name: "镜头 2", cameraId: "cam-1", duration: 6, fps: 24, shotSize: "medium", cameraMove: "push_in", prompt: "" },
    ],
    activeShotId: "shot-2",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
});

describe("previs-timeline 纯逻辑", () => {
    test("01 整场时长 = 各镜头时长累计", () => {
        expect(previsTimelineTotalDuration(makeScene())).toBe(10);
        expect(previsTimelineTotalDuration(makeScene({ shots: [] }))).toBe(0);
    });

    test("02 scrub 比例映射并收敛到 [0, total]", () => {
        expect(resolveTimelineScrub(makeScene(), 0)).toBe(0);
        expect(resolveTimelineScrub(makeScene(), 0.5)).toBe(5);
        expect(resolveTimelineScrub(makeScene(), 1)).toBe(10);
        expect(resolveTimelineScrub(makeScene(), -1)).toBe(0);
        expect(resolveTimelineScrub(makeScene(), 2)).toBe(10);
        // 空场景：任何比例都为 0
        expect(resolveTimelineScrub(makeScene({ shots: [] }), 0.5)).toBe(0);
    });

    test("03 时间命中镜头（含边界：镜头结束时刻属于该镜头）", () => {
        expect(findShotAtTime(makeScene(), 0)).toBe("shot-1");
        expect(findShotAtTime(makeScene(), 3.9)).toBe("shot-1");
        expect(findShotAtTime(makeScene(), 4)).toBe("shot-2");
        expect(findShotAtTime(makeScene(), 9.9)).toBe("shot-2");
        // 超出末尾与负值：钳位到首末镜头
        expect(findShotAtTime(makeScene(), 99)).toBe("shot-2");
        expect(findShotAtTime(makeScene(), -1)).toBe("shot-1");
        expect(findShotAtTime(makeScene({ shots: [] }), 0)).toBe(null);
    });

    test("04 playhead 推进、钳位与首尾环绕", () => {
        expect(advancePlayhead(4, 1, 10)).toBe(5);
        expect(advancePlayhead(9.5, 1, 10)).toBe(0.5); // 越过末尾环绕到 0
        expect(advancePlayhead(0, -1, 10)).toBe(0); // 起点点不回去就停在起点
        expect(advancePlayhead(3, 0, 10)).toBe(3);
        expect(advancePlayhead(3, 1, 0)).toBe(0); // 空场景
    });
});

describe("DirectorTimeline — 静态渲染合同", () => {
    const props = {
        scene: makeScene(),
        time: 4,
        playing: false,
        onScrub: () => {},
        onTogglePlay: () => {},
        onSelectShot: () => {},
    };

    test("05 渲染镜头段、播放头、时间显示与播放按钮", () => {
        const html = renderToStaticMarkup(createElement(DirectorTimeline, props));
        expect(html).toContain("镜头 1");
        expect(html).toContain("镜头 2");
        expect(html).toContain("4.0s");
        expect(html).toContain("10.0s");
        expect(html).toContain("播放");
        expect(html).toContain('data-time="4"');
    });

    test("06 整场时长按镜头时长占比布局（镜头 2 应占 60%）", () => {
        const html = renderToStaticMarkup(createElement(DirectorTimeline, props));
        expect(html).toContain("0%");
        expect(html).toContain("40%");
        expect(html).toContain("100%");
    });
});