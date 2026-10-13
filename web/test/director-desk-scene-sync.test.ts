import { describe, expect, test } from "bun:test";
import { decideRuntimeSync, initialRuntimeViewState, resolveRuntimeViewState, type RuntimeSyncKey, type RuntimeViewState } from "../src/integrations/director-desk/scene-sync";
import { previsSceneToDirectorDocument } from "../src/integrations/director-desk/previs-adapter";
import { moduleGroupSource } from "./helpers/module-group-source";
import type { PrevisScene } from "../src/types/previs";

/**
 * Task 9 合同：Agent/外部更新场景时，runtime 按 sceneId/updatedAt 决策刷新，
 * 且刷新后选择、播放头、当前镜头要么保留、要么确定性地回退，而不是悬空。
 */

const key = (id: string, updatedAt: string): RuntimeSyncKey => ({ id, updatedAt });

describe("decideRuntimeSync — 按 sceneId/revision 决策", () => {
    test("01 首次同步是 session 初始化", () => {
        expect(decideRuntimeSync(null, key("s1", "t1"))).toEqual({ kind: "session" });
    });

    test("02 同一场景同 updatedAt 是 noop，不重建引擎文档", () => {
        expect(decideRuntimeSync(key("s1", "t1"), key("s1", "t1"))).toEqual({ kind: "noop" });
    });

    test("03 同一场景 updatedAt 变化是 content 刷新（保留视图状态）", () => {
        expect(decideRuntimeSync(key("s1", "t1"), key("s1", "t2"))).toEqual({ kind: "content" });
    });

    test("04 场景 id 变化是 session 切换（重置视图状态）", () => {
        expect(decideRuntimeSync(key("s1", "t1"), key("s2", "t1"))).toEqual({ kind: "session" });
        expect(decideRuntimeSync(key("s1", "t1"), key("s2", "t2"))).toEqual({ kind: "session" });
    });
});

const baseScene = (): PrevisScene => ({
    id: "scene-t9",
    version: 1,
    title: "测试场景",
    background: "#c6c8c6",
    environmentIntensity: 1,
    gridVisible: true,
    objects: [
        { id: "obj-a", name: "演员A", kind: "actor", transform: { position: [0, 0, 0], rotation: [0, 0, 0], scale: [1, 1, 1] }, color: "#e0b08a", visible: true, castShadow: true, receiveShadow: true, pose: "stand", archetype: "adult", keyframes: [] },
        { id: "obj-b", name: "箱体B", kind: "primitive", primitive: "box", transform: { position: [2, 0, 1], rotation: [0, 0, 0], scale: [1, 1, 1] }, color: "#888888", visible: true, castShadow: true, receiveShadow: true, keyframes: [] },
    ],
    cameras: [
        { id: "cam-1", name: "主机位", transform: { position: [4, 2, 6], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1.5, 0], focalLength: 50, fov: 36, aperture: 2.8, focusDistance: 6, near: 0.1, far: 200, keyframes: [] },
    ],
    lights: [],
    shots: [
        { id: "shot-1", name: "镜头1", cameraId: "cam-1", duration: 5, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "p1" },
        { id: "shot-2", name: "镜头2", cameraId: "cam-1", duration: 5, fps: 24, shotSize: "wide", cameraMove: "static", prompt: "p2" },
    ],
    activeShotId: "shot-1",
    createdAt: "2026-10-12T00:00:00.000Z",
    updatedAt: "2026-10-12T01:00:00.000Z",
});

const state = (selectedEntityId: string, activeShotId: string, time: number): RuntimeViewState => ({ selectedEntityId, activeShotId, time });

describe("resolveRuntimeViewState — 刷新后视图状态回退", () => {
    test("01 选择、镜头、时间都仍存在时原样保留", () => {
        const document = previsSceneToDirectorDocument(baseScene());
        const resolved = resolveRuntimeViewState(document, state("obj-a", "shot-1", 3));
        expect(resolved).toEqual({ selectedEntityId: "obj-a", activeShotId: "shot-1", time: 3 });
    });

    test("02 被删除的实体选择回退到剩余第一个实体", () => {
        const scene = baseScene();
        scene.objects = scene.objects.filter((object) => object.id !== "obj-a");
        const document = previsSceneToDirectorDocument(scene);
        const resolved = resolveRuntimeViewState(document, state("obj-a", "shot-1", 3));
        expect(resolved.selectedEntityId).toBe("obj-b");
    });

    test("03 角色/道具全部被删除时选择回退到剩余实体（机位也是实体）", () => {
        const scene = baseScene();
        scene.objects = [];
        const document = previsSceneToDirectorDocument(scene);
        const resolved = resolveRuntimeViewState(document, state("obj-a", "shot-1", 3));
        expect(resolved.selectedEntityId).toBe("cam-1");
        // 连机位也没有时选择才真正清空（与 05 的空文档一致）
        const emptyScene = baseScene();
        emptyScene.objects = [];
        emptyScene.cameras = [];
        emptyScene.shots = [];
        const emptyDocument = previsSceneToDirectorDocument(emptyScene);
        expect(resolveRuntimeViewState(emptyDocument, state("obj-a", "shot-1", 3)).selectedEntityId).toBe("");
    });

    test("04 被删除的当前镜头回退到剩余第一个镜头", () => {
        const scene = baseScene();
        scene.shots = scene.shots.filter((shot) => shot.id !== "shot-1");
        scene.activeShotId = "shot-2";
        const document = previsSceneToDirectorDocument(scene);
        const resolved = resolveRuntimeViewState(document, state("obj-a", "shot-1", 3));
        expect(resolved.activeShotId).toBe(document.extensions?.["infinite-canvas"]?.shots[0].id);
    });

    test("05 文档没有镜头时当前镜头清空", () => {
        const scene = baseScene();
        scene.shots = [];
        scene.objects = [];
        scene.cameras = [];
        const document = previsSceneToDirectorDocument(scene);
        const resolved = resolveRuntimeViewState(document, state("", "", 0));
        expect(resolved.activeShotId).toBe("");
        expect(resolved.time).toBe(0);
    });

    test("06 播放头超出新时长时钳到时长边界，负值钳到 0", () => {
        const document = previsSceneToDirectorDocument(baseScene());
        expect(resolveRuntimeViewState(document, state("obj-a", "shot-1", 99)).time).toBe(document.duration);
        expect(resolveRuntimeViewState(document, state("obj-a", "shot-1", -4)).time).toBe(0);
    });

    test("07 无镜头时时长最小化（adapter 下限 0.1），播放头钳到该边界", () => {
        const scene = baseScene();
        scene.shots = [];
        scene.objects = [];
        scene.cameras = [{ id: "cam-1", name: "主机位", transform: { position: [4, 2, 6], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1.5, 0], focalLength: 50, fov: 36, aperture: 2.8, focusDistance: 6, near: 0.1, far: 200, keyframes: [] }];
        const document = previsSceneToDirectorDocument(scene);
        const resolved = resolveRuntimeViewState(document, state("", "", 7));
        expect(resolved.time).toBe(document.duration);
    });
});

describe("initialRuntimeViewState — 新会话起点", () => {
    test("08 无选择、播放头 0、当前镜头取文档 activeShot", () => {
        const document = previsSceneToDirectorDocument(baseScene());
        expect(initialRuntimeViewState(document)).toEqual({ selectedEntityId: "", activeShotId: "shot-1", time: 0 });
    });

    test("09 activeShot 不存在时回退第一个镜头", () => {
        const scene = baseScene();
        scene.activeShotId = "shot-missing";
        const document = previsSceneToDirectorDocument(scene);
        expect(initialRuntimeViewState(document).activeShotId).toBe("shot-1");
    });
});

describe("runtime hook 接线合同（源码约束）", () => {
    test("10 场景刷新按 sceneId/updatedAt 决策并回退视图状态", () => {
        const source = moduleGroupSource("components/canvas/previs/use-director-desk-previs-runtime.ts");
        expect(source).toContain("decideRuntimeSync(");
        expect(source).toContain("resolveRuntimeViewState(");
        // 决策前的状态记忆：同一场景同修订不重建
        expect(source).toContain("lastSyncKeyRef");
    });
});