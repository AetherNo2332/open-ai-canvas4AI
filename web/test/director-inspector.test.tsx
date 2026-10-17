import { describe, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { createElement } from "react";
import { previsInspectorViewModel, updatePrevisObjectTransform, updatePrevisCameraParams } from "../src/lib/canvas/previs/previs-inspector";
import { DirectorInspector } from "../src/components/canvas/previs/director-inspector";
import type { PrevisScene, PrevisTransform } from "../src/types/previs";

const makeScene = (overrides: Partial<PrevisScene> = {}): PrevisScene => ({
    id: "scene-insp",
    version: 1,
    title: "Inspector 场景",
    background: "#222222",
    environmentIntensity: 1,
    gridVisible: true,
    objects: [
        {
            id: "actor-1",
            name: "主角",
            kind: "actor",
            primitive: "character",
            transform: { position: [1, 0, 2], rotation: [0, 0.5, 0], scale: [1, 1, 1] },
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
    shots: [{ id: "shot-1", name: "镜头 1", cameraId: "cam-1", duration: 4, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" }],
    activeShotId: "shot-1",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    ...overrides,
});

describe("previsInspectorViewModel — 选中对象与活动镜头机位", () => {
    test("01 选中对象 + 活动镜头机位同时可见", () => {
        const vm = previsInspectorViewModel(makeScene(), "actor-1", "shot-1");
        expect(vm.object).toMatchObject({ id: "actor-1", name: "主角" });
        expect(vm.object?.position).toEqual([1, 0, 2]);
        expect(vm.camera).toMatchObject({ id: "cam-1", name: "主机位", focalLength: 35, target: [0, 1, 0] });
    });

    test("02 未选中对象时只有机位；镜头脱离机位时两者皆空", () => {
        expect(previsInspectorViewModel(makeScene(), "", "shot-1").object).toBeNull();
        expect(previsInspectorViewModel(makeScene(), "", "shot-1").camera).not.toBeNull();
        const detached = makeScene({ shots: [{ id: "shot-1", name: "镜头 1", cameraId: "cam-missing", duration: 4, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" }] });
        const vm = previsInspectorViewModel(detached, "", "shot-1");
        expect(vm.camera).toBeNull();
    });
});

describe("updatePrevisObjectTransform — 局部变换更新", () => {
    test("03 更新位置/旋转/缩放并刷新 updatedAt", () => {
        const patch: Partial<PrevisTransform> = { position: [2, 0.5, 1] };
        const next = updatePrevisObjectTransform(makeScene(), "actor-1", patch);
        expect(next?.objects[0].transform.position).toEqual([2, 0.5, 1]);
        expect(next?.objects[0].transform.rotation).toEqual([0, 0.5, 0]);
        expect(next?.updatedAt).not.toBe("2026-01-01T00:00:00Z");
    });

    test("04 非法对象 id 返回 null，越界缩放收敛", () => {
        expect(updatePrevisObjectTransform(makeScene(), "missing", { position: [0, 0, 0] })).toBeNull();
        const tiny = updatePrevisObjectTransform(makeScene(), "actor-1", { scale: [0, 0, 0] });
        expect(tiny?.objects[0].transform.scale).not.toEqual([0, 0, 0]);
        const big = updatePrevisObjectTransform(makeScene(), "actor-1", { scale: [999, 999, 999] });
        expect(big?.objects[0].transform.scale[0]).toBeLessThanOrEqual(100);
    });
});

describe("updatePrevisCameraParams — 焦段与目标", () => {
    test("05 更新焦段与目标点并刷新 updatedAt", () => {
        const next = updatePrevisCameraParams(makeScene(), "cam-1", { focalLength: 50, target: [0.5, 1.2, -1] });
        expect(next?.cameras[0].focalLength).toBe(50);
        expect(next?.cameras[0].target).toEqual([0.5, 1.2, -1]);
        expect(next?.updatedAt).not.toBe("2026-01-01T00:00:00Z");
    });

    test("06 焦段越界收敛到 [1, 300]，非法机位返回 null", () => {
        const short = updatePrevisCameraParams(makeScene(), "cam-1", { focalLength: 0.2 });
        expect(short?.cameras[0].focalLength).toBe(1);
        const long = updatePrevisCameraParams(makeScene(), "cam-1", { focalLength: 5000 });
        expect(long?.cameras[0].focalLength).toBe(300);
        expect(updatePrevisCameraParams(makeScene(), "cam-missing", { focalLength: 24 })).toBeNull();
    });
});

describe("DirectorInspector — 静态渲染合同", () => {
    const baseProps = {
        scene: makeScene(),
        selectedEntityId: "",
        activeShotId: "shot-1",
        onUpdateObject: () => {},
        onUpdateCamera: () => {},
    };

    test("07 选中对象时渲染对象变换区与摄影机区", () => {
        const html = renderToStaticMarkup(createElement(DirectorInspector, { ...baseProps, selectedEntityId: "actor-1" }));
        expect(html).toContain("对象");
        expect(html).toContain("主角");
        expect(html).toContain("摄影机");
        expect(html).toContain("主机位");
        expect(html).toContain("35");
    });

    test("08 无选中对象时只显示摄影机区", () => {
        const html = renderToStaticMarkup(createElement(DirectorInspector, baseProps));
        expect(html).toContain("摄影机");
        expect(html).not.toContain("主角");
    });

    test("09 无任何目标时展示空态", () => {
        const emptyScene = makeScene({ shots: [], cameras: [], activeShotId: "", objects: [] });
        const html = renderToStaticMarkup(createElement(DirectorInspector, { ...baseProps, scene: emptyScene, activeShotId: "" }));
        expect(html).toContain("选择对象或切换镜头");
    });
});
