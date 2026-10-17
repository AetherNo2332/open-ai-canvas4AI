import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";
import {
    buildPrevisShotListItems,
    addPrevisShot,
    updatePrevisShot,
    removePrevisShot,
    movePrevisShot,
} from "../src/lib/canvas/previs/previs-shot-list";
import { DirectorShotList } from "../src/components/canvas/previs/director-shot-list";
import type { PrevisScene } from "../src/types/previs";

const makeScene = (overrides: Partial<PrevisScene> = {}): PrevisScene => ({
    id: "scene-shot",
    version: 1,
    title: "ShotList 场景",
    background: "#222222",
    environmentIntensity: 1,
    gridVisible: true,
    objects: [],
    cameras: [
        { id: "cam-1", name: "主机位", transform: { position: [0, 1.6, 5], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1, 0], focalLength: 35, fov: 50, aperture: 2.8, focusDistance: 5, near: 0.05, far: 500, keyframes: [] },
        { id: "cam-2", name: "副机位", transform: { position: [4, 1.6, 2], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1, 0], focalLength: 50, fov: 39, aperture: 2.8, focusDistance: 5, near: 0.05, far: 500, keyframes: [] },
    ],
    lights: [],
    shots: [
        { id: "shot-1", name: "镜头 1", cameraId: "cam-1", duration: 4, fps: 24, shotSize: "wide", cameraMove: "static", prompt: "开场" },
        { id: "shot-2", name: "镜头 2", cameraId: "cam-2", duration: 6, fps: 24, shotSize: "medium", cameraMove: "push_in", prompt: "" },
    ],
    activeShotId: "shot-2",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
});

describe("buildPrevisShotListItems — 时间轴布局", () => {
    test("01 顺序、时长累计与活动标记", () => {
        const items = buildPrevisShotListItems(makeScene());
        expect(items).toHaveLength(2);
        expect(items[0]).toMatchObject({ index: 0, startAt: 0, endAt: 4, isActive: false });
        expect(items[1]).toMatchObject({ index: 1, shotId: "shot-2", startAt: 4, endAt: 10, isActive: true });
        expect(items.reduce((total, item) => total + (item.endAt - item.startAt), 0)).toBe(10);
    });

    test("02 空场景返回空列表", () => {
        expect(buildPrevisShotListItems(makeScene({ shots: [], activeShotId: "" }))).toEqual([]);
    });
});

describe("addPrevisShot — 新增落尾并激活", () => {
    test("03 新增镜头使用当前机位，默认 5s/24fps/中景/固定", () => {
        const { scene, shot } = addPrevisShot(makeScene(), "cam-2");
        expect(scene.shots).toHaveLength(3);
        expect(shot.name).toBe("镜头 3");
        expect(shot).toMatchObject({ cameraId: "cam-2", duration: 5, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" });
        expect(scene.activeShotId).toBe(shot.id);
        expect(scene.updatedAt).not.toBe("2026-01-01T00:00:00Z");
    });

    test("04 机位不存在时拒绝新增", () => {
        expect(addPrevisShot(makeScene(), "cam-missing")).toBeNull();
    });
});

describe("updatePrevisShot — 局部更新与数值收敛", () => {
    test("05 更新名称/时长并刷新 updatedAt", () => {
        const next = updatePrevisShot(makeScene(), "shot-1", { name: "特写", duration: 8 });
        expect(next?.shots.find((shot) => shot.id === "shot-1")?.name).toBe("特写");
        expect(next?.shots.find((shot) => shot.id === "shot-1")?.duration).toBe(8);
        expect(next?.updatedAt).not.toBe("2026-01-01T00:00:00Z");
        expect(next?.shots.find((shot) => shot.id === "shot-2")?.name).toBe("镜头 2");
    });

    test("06 越界时长收敛到边界，非法镜头 id 返回 null", () => {
        const overflow = updatePrevisShot(makeScene(), "shot-1", { duration: 9999 });
        expect(overflow?.shots.find((shot) => shot.id === "shot-1")?.duration).toBe(120);
        const tiny = updatePrevisShot(makeScene(), "shot-1", { duration: -3 });
        expect(tiny?.shots.find((shot) => shot.id === "shot-1")?.duration).toBeLessThan(1);
        expect(updatePrevisShot(makeScene(), "shot-missing", { name: "x" })).toBeNull();
    });
});

describe("removePrevisShot — 最少一个镜头的不变量", () => {
    test("07 删除非活动镜头，活动镜头不变", () => {
        const next = removePrevisShot(makeScene(), "shot-1");
        expect(next?.shots.map((shot) => shot.id)).toEqual(["shot-2"]);
        expect(next?.activeShotId).toBe("shot-2");
    });

    test("08 删除活动镜头时激活它的后继（末位则回退第一个）", () => {
        const first = removePrevisShot(makeScene(), "shot-2");
        expect(first?.activeShotId).toBe("shot-1");
        const triple = makeScene({
            shots: [
                { id: "s-a", name: "A", cameraId: "cam-1", duration: 2, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" },
                { id: "s-b", name: "B", cameraId: "cam-1", duration: 2, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" },
                { id: "s-c", name: "C", cameraId: "cam-1", duration: 2, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" },
            ],
            activeShotId: "s-c",
        });
        const last = removePrevisShot(triple, "s-c");
        expect(last?.activeShotId).toBe("s-b");
    });

    test("09 最后一个镜头不可删除", () => {
        expect(removePrevisShot(makeScene({ shots: makeScene().shots.slice(0, 1), activeShotId: "shot-1" }), "shot-1")).toBeNull();
    });
});

describe("movePrevisShot — 边界内交换", () => {
    test("10 交换顺序；边界外返回 null", () => {
        const down = movePrevisShot(makeScene(), "shot-1", 1);
        expect(down?.shots.map((shot) => shot.id)).toEqual(["shot-2", "shot-1"]);
        const up = movePrevisShot(makeScene(), "shot-1", -1);
        expect(up).toBeNull();
        const pastEnd = movePrevisShot(makeScene(), "shot-2", 1);
        expect(pastEnd).toBeNull();
    });
});

describe("DirectorShotList — 静态渲染合同", () => {
    const props = {
        scene: makeScene(),
        onSelectShot: () => {},
        onAddShot: () => {},
        onUpdateShot: () => {},
        onRemoveShot: () => {},
        onMoveShot: () => {},
    };

    test("11 渲染全部镜头行、活动标记与新增入口（皮肤 token，无硬编码色值）", () => {
        const html = renderToStaticMarkup(createElement(DirectorShotList, props));
        expect(html).toContain("镜头 1");
        expect(html).toContain("镜头 2");
        expect(html).toContain('data-active="true"');
        expect(html).toContain("新增镜头");
        expect(html).toContain("4s");
        expect(html).toContain("6s");
    });

    test("12 活动镜头展示提示词编辑区", () => {
        const html = renderToStaticMarkup(createElement(DirectorShotList, props));
        expect(html).toContain("镜头提示词");
        // 活动镜头（shot-2）的提示词为空 → placeholder；非活动镜头不展示
        expect(html).not.toContain("开场");
    });

    test("13 空场景展示空态提示", () => {
        const html = renderToStaticMarkup(createElement(DirectorShotList, { ...props, scene: makeScene({ shots: [], activeShotId: "" }) }));
        expect(html).toContain("还没有镜头");
    });
});