import { describe, expect, test } from "bun:test";
import { readSceneDocument, projectForScene, updateDocumentScene } from "@desk/scenes/sequence-project";
import { createPrevisActor, createPrevisCamera, createPrevisObject, createPrevisScene } from "@/lib/canvas/previs/previs-scene";
import { directorDocumentToPrevisScene, previsSceneToDirectorDocument } from "@/integrations/director-desk/previs-adapter";
import type { PrevisScene } from "@/types/previs";

/**
 * T6:宿主下发的文档必须能被原版应用自己的入口(readSceneDocument → assertSceneDocument/assertProject)接收,
 * 应用回传的 v3 SceneDocument 必须能还原为同一个 PrevisScene(零丢字段、ID 稳定)。
 * 校验函数直接取自 director-desk 源码(@desk),不在测试里复制规则。
 */

function richScene(): PrevisScene {
    const scene = createPrevisScene("往返场景");
    const actor = createPrevisActor("主角", [1, 0, 2], "#2f7de1", "woman");
    actor.motionPath = { points: [[1, 0, 2], [3, 0, 2], [3, 0, 5]], speed: "walk", startDelay: 0.5, orientToPath: true };
    actor.pose = "sit";
    const box = createPrevisObject("box", "箱子", [0, 0.5, 0], "#8795a5");
    const plane = createPrevisObject("plane", "地板", [0, 0, 0], "#d0d2d0");
    const secondCamera = createPrevisCamera("侧机位");
    secondCamera.focalLength = 85;
    scene.objects.push(actor, box, plane);
    scene.cameras.push(secondCamera);
    scene.shots.push({ ...scene.shots[0], id: "shot-second", name: "第二镜", cameraId: secondCamera.id, duration: 3 });
    (scene as PrevisScene & { extensions?: Record<string, unknown> }).extensions = { userMarker: { mood: "blue" }, futureField: [1, 2, 3] };
    return scene;
}

describe("DirectorDesk 原版应用场景往返", () => {
    test("01 适配器输出通过原版应用入口校验", () => {
        const document = previsSceneToDirectorDocument(richScene());
        expect(() => readSceneDocument(document)).not.toThrow();
    });

    test("02 应用白名单外的帧率/画幅/颜色/图元仍可被接收(原值保留在扩展区)", () => {
        const scene = richScene();
        scene.shots = scene.shots.map((shot) => ({ ...shot, fps: 25, aspectRatio: "2.39:1" }));
        scene.objects[1].color = "steelblue";
        const restored = directorDocumentToPrevisScene(readSceneDocument(previsSceneToDirectorDocument(scene)));
        expect(restored.shots.map((shot) => [shot.fps, shot.aspectRatio])).toEqual([[25, "2.39:1"], [25, "2.39:1"]]);
        expect(restored.objects[1].color).toBe("steelblue");
    });

    test("03 扩展标识全部符合应用插件数据规则(kebab-case)", () => {
        const document = previsSceneToDirectorDocument(richScene()) as unknown as Record<string, unknown> & { entities: Record<string, unknown>[] };
        for (const key of Object.keys(document.extensions as object)) expect(key).toMatch(/^[a-z][a-z0-9-]{0,79}$/);
        for (const entity of document.entities) expect(entity.extensions).toBeUndefined();
    });

    test("04 经应用 v3 文档往返零丢字段、ID 稳定(资源地址由宿主当前场景补回)", () => {
        const scene = richScene();
        const v3 = readSceneDocument(previsSceneToDirectorDocument(scene));
        expect(directorDocumentToPrevisScene(v3, scene)).toEqual(scene);
    });

    test("04b 资源地址与存储键不下发给应用(安全边界)", () => {
        const scene = richScene();
        scene.objects[1].storageKey = "users/u1/private/model.glb";
        scene.objects[1].url = "https://cdn.example/signed?token=secret";
        const serialized = JSON.stringify(previsSceneToDirectorDocument(scene));
        expect(serialized).not.toContain("users/u1/private");
        expect(serialized).not.toContain("token=secret");
        expect(serialized).not.toContain("storageKey");
        const restored = directorDocumentToPrevisScene(readSceneDocument(JSON.parse(serialized)), scene);
        expect(restored.objects[1].storageKey).toBe("users/u1/private/model.glb");
        expect(restored.objects[1].url).toBe("https://cdn.example/signed?token=secret");
    });

    test("05 应用内编辑(移动实体)回传后反映到 PrevisScene", () => {
        const scene = richScene();
        const v3 = readSceneDocument(previsSceneToDirectorDocument(scene));
        const project = projectForScene(v3);
        const boxId = scene.objects.find((object) => object.name === "箱子")!.id;
        const box = project.entities.find((entity) => entity.id === boxId)!;
        box.position = [4, 0.5, -2];
        const camera = project.entities.find((entity) => entity.id === scene.cameras[0].id)!;
        camera.camera!.focal = 50;
        const edited = updateDocumentScene(v3, v3.activeSceneId, project);
        const restored = directorDocumentToPrevisScene(edited, scene);
        expect(restored.objects.find((object) => object.id === boxId)!.transform.position).toEqual([4, 0.5, -2]);
        expect(restored.cameras[0].focalLength).toBe(50);
        // 未改动的对象保持原样。
        expect(restored.objects[0]).toEqual(scene.objects[0]);
    });

    test("06 应用内新增实体回传为新的 PrevisObject", () => {
        const scene = richScene();
        const v3 = readSceneDocument(previsSceneToDirectorDocument(scene));
        const project = projectForScene(v3);
        const boxId = scene.objects.find((object) => object.name === "箱子")!.id;
        const template = project.entities.find((entity) => entity.id === boxId)!;
        project.entities.push({ ...structuredClone(template), id: "app-new-chair", name: "椅子", asset: "chair" });
        const restored = directorDocumentToPrevisScene(updateDocumentScene(v3, v3.activeSceneId, project));
        const added = restored.objects.find((object) => object.id === "app-new-chair");
        expect(added?.name).toBe("椅子");
        expect(restored.objects).toHaveLength(scene.objects.length + 1);
    });

    test("06b 应用内摆放的白模资产经保存→重开仍是同一资产(不退化为立方体)", () => {
        const scene = richScene();
        const v3 = readSceneDocument(previsSceneToDirectorDocument(scene));
        const project = projectForScene(v3);
        const boxId = scene.objects.find((object) => object.name === "箱子")!.id;
        const template = project.entities.find((entity) => entity.id === boxId)!;
        project.entities.push({ ...structuredClone(template), id: "app-bench", name: "长椅", asset: "bench", position: [3, 0, -3] });
        // 第一次回传 = 用户保存;得到的 PrevisScene 即服务端持久化内容。
        const saved = directorDocumentToPrevisScene(updateDocumentScene(v3, v3.activeSceneId, project), scene);
        // 重开 = 从持久化的 PrevisScene 重新下发给应用。
        const reopened = readSceneDocument(previsSceneToDirectorDocument(saved));
        const bench = projectForScene(reopened).entities.find((entity) => entity.id === "app-bench");
        expect(bench?.asset).toBe("bench");
        expect(bench?.position).toEqual([3, 0, -3]);
        // 原有对象不受影响。
        expect(projectForScene(reopened).entities.find((entity) => entity.id === boxId)?.asset).toBe("cube");
    });

    test("06c 宿主(Agent/旧工作台)改动对象变换时以 PrevisScene 为准,应用资产仍保留", () => {
        const scene = richScene();
        const v3 = readSceneDocument(previsSceneToDirectorDocument(scene));
        const project = projectForScene(v3);
        const boxId = scene.objects.find((object) => object.name === "箱子")!.id;
        const template = project.entities.find((entity) => entity.id === boxId)!;
        project.entities.push({ ...structuredClone(template), id: "app-bench", name: "长椅", asset: "bench" });
        const saved = directorDocumentToPrevisScene(updateDocumentScene(v3, v3.activeSceneId, project), scene);
        const moved = structuredClone(saved);
        moved.objects.find((object) => object.id === "app-bench")!.transform.position = [9, 0, 9];
        const bench = projectForScene(readSceneDocument(previsSceneToDirectorDocument(moved))).entities.find((entity) => entity.id === "app-bench");
        expect(bench?.asset).toBe("bench");
        expect(bench?.position).toEqual([9, 0, 9]);
    });

    test("07 缺少影策扩展或形状不合法的回传被拒绝(不静默生成空场景)", () => {
        expect(() => directorDocumentToPrevisScene({ format: "director-desk", version: 3 } as never)).toThrow();
        const v3 = readSceneDocument(previsSceneToDirectorDocument(richScene()));
        const stripped = structuredClone(v3);
        stripped.scenes[0].state.extensions = {};
        expect(() => directorDocumentToPrevisScene(stripped)).toThrow("director_document_missing_canvas_extension");
    });
});
