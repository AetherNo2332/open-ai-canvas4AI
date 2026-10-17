import { describe, expect, test } from "bun:test";
import { touchPrevisScene } from "../src/lib/canvas/previs/previs-scene";
import { directorDeskRuntimePolicy } from "../src/lib/canvas/director-desk-feature";
import { buildPrevisShotListItems, addPrevisShot, updatePrevisShot } from "../src/lib/canvas/previs/previs-shot-list";
import { updatePrevisObjectTransform, updatePrevisCameraParams } from "../src/lib/canvas/previs/previs-inspector";
import type { PrevisScene } from "../src/types/previs";

const makeScene = (): PrevisScene => ({
    id: "scene-1",
    version: 1,
    title: "回退演练场景",
    background: "#222222",
    environmentIntensity: 1,
    gridVisible: true,
    objects: [{ id: "actor-1", name: "主角", kind: "actor", primitive: "character", transform: { position: [1, 0, 2], rotation: [0, 0, 0], scale: [1, 1, 1] }, color: "#336699", visible: true, castShadow: true, receiveShadow: true, keyframes: [] }],
    cameras: [{ id: "cam-1", name: "主机位", transform: { position: [0, 1.6, 5], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1, 0], focalLength: 35, fov: 50, aperture: 2.8, focusDistance: 5, near: 0.05, far: 500, keyframes: [] }],
    lights: [],
    shots: [{ id: "shot-1", name: "镜头 1", cameraId: "cam-1", duration: 4, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" }],
    activeShotId: "shot-1",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T01:00:00Z",
});

/** 合法 PrevisScene 顶层字段(持久化源真)— 引擎内部状态不得进入。 */
const canonicalTopLevelKeys = new Set(["id", "version", "title", "background", "environmentIntensity", "gridVisible", "objects", "cameras", "lights", "shots", "activeShotId", "createdAt", "updatedAt", "extensions"]);

describe("P5 回退演练 — 关闭开关后旧工作台可无损打开新运行时保存的场景", () => {
    test("01 新编辑器编辑路径产出的场景只包含 canonical 字段(无引擎私有状态泄漏)", () => {
        let scene = makeScene();
        // 模拟原生编辑器的一组编辑:检查器改焦段/位姿、镜头列表加镜头改时长。
        const apply = (next: PrevisScene | null): PrevisScene => {
            expect(next).not.toBeNull();
            return touchPrevisScene(next!);
        };
        scene = apply(updatePrevisCameraParams(scene, "cam-1", { focalLength: 50 }));
        scene = apply(updatePrevisObjectTransform(scene, "actor-1", { position: [2, 0, 3] }));
        const added = addPrevisShot(scene, "cam-1");
        scene = added ? touchPrevisScene(added.scene) : scene;
        scene = apply(updatePrevisShot(scene, scene.shots[0].id, { duration: 6 }));

        const keys = Object.keys(JSON.parse(JSON.stringify(scene)));
        for (const key of keys) {
            expect(canonicalTopLevelKeys.has(key)).toBe(true);
        }
        // extensions 是纯透传白名单区:未启用 DirectorDesk 扩展时不得被编辑路径写入。
        expect(scene.extensions).toBeUndefined();
        // 全部镜头列表/时间轴消费仍然成立(旧工作台依赖同一 canonical 契约)。
        expect(buildPrevisShotListItems(scene)).toHaveLength(2);
    });

    test("02 开关矩阵:服务端任意值 → fail-closed 策略(关闭/未配置一律回退旧工作台)", () => {
        expect(directorDeskRuntimePolicy("off")).toBe("legacy");
        expect(directorDeskRuntimePolicy("canary")).toBe("canary");
        expect(directorDeskRuntimePolicy("on")).toBe("authoritative");
        expect(directorDeskRuntimePolicy("unsafe")).toBe("legacy");
        expect(directorDeskRuntimePolicy(undefined)).toBe("legacy");
        expect(directorDeskRuntimePolicy(null)).toBe("legacy");
        expect(directorDeskRuntimePolicy("ON")).toBe("legacy");
    });

    test("03 回退演练链路:off 时的旧工作台入口与 canary 时的双轨一致(源合同)", async () => {
        const { readFileSync } = await import("node:fs");
        const { resolve } = await import("node:path");
        const base = import.meta.dir;
        const wrapper = readFileSync(resolve(base, "../src/components/canvas/previs/director-desk-previs-workbench.tsx"), "utf8");
        // 旧工作台只在 legacy 策略 / 未打开 / 无场景 / 原版应用加载失败时出现。
        expect(wrapper).toContain('const usingApp = policy !== "legacy" && props.open && Boolean(scene)');
        expect(wrapper).toContain("if (!usingApp || !scene) return <CanvasPrevisWorkbench {...props} />");
        // 运行时故障回退注入同一协调器(数据不离手)。
        expect(wrapper).toContain("if (appFailed) {");
        expect(wrapper).toContain("saveController={saveController}");
        // 政策解析只来自服务端 mode 值(客户端不做第二套分群判断)。
        const feature = readFileSync(resolve(base, "../src/lib/canvas/director-desk-feature.ts"), "utf8");
        expect(feature).toContain('mode === "on"');
        expect(feature).toContain('mode === "canary"');
        expect(feature).toContain('return "legacy"');
    });
});
