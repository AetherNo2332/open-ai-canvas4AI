import type { PrevisCamera, PrevisObject, PrevisScene, PrevisVec3 } from "@/types/previs";
import {
    DIRECTOR_CANVAS_EXTENSION_KEY,
    DIRECTOR_PLACEHOLDER_CAMERA_ID,
    type DirectorCanvasExtension,
    type DirectorEntity,
    type DirectorMotionPath,
    type DirectorPreviewInput,
    type DirectorProject,
    type DirectorSceneDocument,
} from "./previs-runtime-types";

/**
 * PrevisScene ↔ 原版 DirectorDesk 文档。
 *
 * 正向:产出原版应用入口(readSceneDocument/assertProject)可接收的 Project v1——
 * 资产、帧率、画幅、颜色、焦段都收敛到应用白名单;影策原值(对象/机位/镜头/灯光/扩展字段)
 * 以纯 JSON 放进 kebab-case 扩展区,保证应用不认识的字段不丢。
 * 反向:接收应用回传的 Project v1/v2 或 SceneDocument v3(取当前戏段),
 * 把每个实体与「原值的正向投影」比对,只把应用真正改动的字段合并回原值,
 * 因此白名单收敛不会把原值改坏,往返零丢字段、ID 稳定。
 */

const clone = <T>(value: T): T => structuredClone(value);
/** 扩展区必须是纯 JSON(应用拒绝 undefined/非普通对象),用 JSON 往返剥掉 undefined。 */
const toJson = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T;
const same = (left: unknown, right: unknown) => JSON.stringify(left) === JSON.stringify(right);
const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);

// 与 director-desk/src/model.ts 的 FRAME_RATES / ASPECTS 对齐;不在白名单的原值保留在扩展区。
const APP_FRAME_RATES = [24, 30, 50, 59, 60, 90, 120];
const APP_ASPECTS: Record<string, string> = { "9:16": "9:16", "16:9": "16:9", "2.39:1": "21:9", "21:9": "21:9", "3:4": "3:4", "4:3": "4:3", "1:1": "1:1" };
const FALLBACK_PROP_COLOR = "#a7bdd7";
const CAMERA_COLOR = "#ffffff";
const PLACEHOLDER_CAMERA_ID = DIRECTOR_PLACEHOLDER_CAMERA_ID;

/** 资源地址与存储键属于宿主资源边界,不下发给应用;回传时由宿主当前场景补回。 */
const RESOURCE_FIELDS = ["storageKey", "url", "mimeType"] as const;

function stripResourceFields(object: PrevisObject): PrevisObject {
    const { storageKey: _storageKey, url: _url, mimeType: _mimeType, ...safe } = object;
    return safe as PrevisObject;
}

const appColor = (color: string) => (/^#[0-9a-f]{6}$/iu.test(color) ? color : FALLBACK_PROP_COLOR);
const appScale = (scale: PrevisVec3): PrevisVec3 => scale.map((value) => (Number.isFinite(value) && value > 0 ? value : 0.001)) as PrevisVec3;
const clamp = (value: number, min: number, max: number, fallback: number) => (Number.isFinite(value) ? Math.min(max, Math.max(min, value)) : fallback);
const appName = (name: string) => name.slice(0, 200);

/**
 * 应用里摆放的白模资产(长椅、楼体、门……)在 PrevisScene 里没有对应字段。
 * 按对象 id 记在场景扩展区,重开时还原为同一资产;只记与默认映射不同的对象。
 */
export const DIRECTOR_APP_ASSETS_KEY = "director-desk-assets";
type AppAssetRecord = { kind: DirectorEntity["kind"]; asset: string; parameters?: unknown; assetParameters?: unknown; build?: DirectorEntity["build"] };
type AppAssetMap = Record<string, AppAssetRecord>;

function appAssetsOf(scene: Pick<PrevisScene, "extensions">): AppAssetMap {
    const raw = scene.extensions?.[DIRECTOR_APP_ASSETS_KEY];
    return isRecord(raw) ? (raw as AppAssetMap) : {};
}

function objectAsset(object: PrevisObject): string {
    if (object.kind === "actor") return object.archetype === "woman" ? "woman" : "person";
    switch (object.primitive) {
        case "sphere":
            return "sphere";
        case "cylinder":
            return "cylinder";
        case "plane":
            return "ground";
        default:
            return "cube";
    }
}

function primitiveForAsset(asset: string): PrevisObject["primitive"] {
    if (asset === "sphere" || asset === "cylinder") return asset;
    if (asset === "ground") return "plane";
    return "box";
}

function pathOf(motionPath: PrevisObject["motionPath"]): DirectorMotionPath | null {
    if (!motionPath || !motionPath.points.length) return null;
    const startDelay = Math.max(0, motionPath.startDelay || 0);
    return {
        smooth: true,
        interpolation: "continuous",
        points: motionPath.points.map((position, index) => ({ time: startDelay + index, position: clone(position) })),
    };
}

function mergePath(entityPath: DirectorMotionPath | null, original: PrevisObject["motionPath"]): PrevisObject["motionPath"] {
    if (!entityPath || !entityPath.points.length) return undefined;
    return {
        points: entityPath.points.map((point) => clone(point.position)),
        speed: original?.speed ?? "walk",
        startDelay: Math.max(0, entityPath.points[0].time),
        orientToPath: original?.orientToPath ?? true,
    };
}

function baseEntity(): Omit<DirectorEntity, "id" | "kind" | "name" | "asset" | "color" | "position" | "rotation" | "scale" | "visible" | "path"> {
    return { locked: false, height: 1.75, build: "normal", gender: "male", face: "path", faceTarget: "", clips: [], pose: {}, poseKeys: [], camera: null, count: 1, spacing: 0.75, seed: 42, reference: "" };
}

function objectToEntity(object: PrevisObject, appAssets: AppAssetMap = {}): DirectorEntity {
    const isActor = object.kind === "actor";
    const kind = isActor ? "actor" : "prop";
    const record = appAssets[object.id];
    // 只在类型一致时还原应用资产(宿主把对象改成演员/道具后,资产记录失效)。
    const app = record && record.kind === kind && typeof record.asset === "string" ? record : undefined;
    return {
        ...baseEntity(),
        id: object.id,
        kind: isActor ? "actor" : "prop",
        name: appName(object.name),
        asset: app?.asset ?? objectAsset(object),
        ...(app?.parameters !== undefined ? { parameters: clone(app.parameters) } : {}),
        ...(app?.assetParameters !== undefined ? { assetParameters: clone(app.assetParameters) } : {}),
        ...(app?.build ? { build: app.build } : {}),
        color: appColor(object.color),
        position: clone(object.transform.position),
        rotation: clone(object.transform.rotation),
        scale: appScale(object.transform.scale),
        visible: object.visible,
        height: isActor ? clamp(object.actorProfile?.height ?? 1.75, 0.2, 10, 1.75) : 1.75,
        gender: object.archetype === "woman" ? "female" : "male",
        path: pathOf(object.motionPath),
    };
}

function cameraToEntity(camera: PrevisCamera): DirectorEntity {
    return {
        ...baseEntity(),
        id: camera.id,
        kind: "camera",
        name: appName(camera.name),
        asset: "camera",
        color: CAMERA_COLOR,
        position: clone(camera.transform.position),
        rotation: clone(camera.transform.rotation),
        scale: appScale(camera.transform.scale),
        visible: true,
        height: 1.6,
        face: "target",
        path: pathOf(camera.motionPath),
        camera: {
            aim: "target",
            focal: clamp(camera.focalLength, 8, 300, 35),
            target: clone(camera.target),
            targetId: "",
            targetHeight: Number.isFinite(camera.target[1]) ? camera.target[1] : 1,
            mode: "free",
            offset: [0, 0, 0],
            inheritRotation: false,
            hideWalls: [],
        },
    };
}

function placeholderCamera(): DirectorEntity {
    return cameraToEntity({
        id: PLACEHOLDER_CAMERA_ID,
        name: "占位机位",
        transform: { position: [4.8, 2.7, 6.8], rotation: [0, 0, 0], scale: [1, 1, 1] },
        target: [0, 1, 0],
        focalLength: 35,
        fov: 50,
        aperture: 2.8,
        focusDistance: 5,
        near: 0.05,
        far: 500,
        keyframes: [],
    });
}

export function previsSceneToDirectorDocument(scene: PrevisScene): DirectorProject {
    const cameraEntities = scene.cameras.map(cameraToEntity);
    if (!cameraEntities.length) cameraEntities.push(placeholderCamera());
    const cameraIds = new Set(cameraEntities.map((entity) => entity.id));
    const duration = Math.max(
        0.1,
        scene.shots.reduce((total, shot) => total + Math.max(0, shot.duration), 0),
    );
    // 应用要求切镜从 0 秒开始、严格递增、落在时长内且机位存在;不合格的镜头只保留在扩展区。
    const cuts: { time: number; cameraId: string }[] = [];
    let cutTime = 0;
    for (const shot of scene.shots) {
        if (shot.duration > 0 && cameraIds.has(shot.cameraId) && cutTime < duration && (cuts.length === 0 || cutTime > cuts[cuts.length - 1].time)) {
            cuts.push({ time: cutTime, cameraId: shot.cameraId });
        }
        cutTime += Math.max(0, shot.duration);
    }
    if (!cuts.length || cuts[0].time !== 0) cuts.unshift({ time: 0, cameraId: cuts[0]?.cameraId ?? cameraEntities[0].id });
    const firstShot = scene.shots[0];
    const extension: DirectorCanvasExtension = toJson({
        sourceSceneId: scene.id,
        scene: {
            id: scene.id,
            title: scene.title,
            background: scene.background,
            environmentIntensity: scene.environmentIntensity,
            environment: scene.environment,
            gridVisible: scene.gridVisible,
            activeShotId: scene.activeShotId,
            createdAt: scene.createdAt,
            updatedAt: scene.updatedAt,
        },
        objects: scene.objects.map(stripResourceFields),
        cameras: scene.cameras,
        lights: scene.lights,
        shots: scene.shots,
        sceneExtensions: scene.extensions,
    });
    return {
        format: "director-desk",
        version: 1,
        name: appName(scene.title),
        duration,
        fps: firstShot && APP_FRAME_RATES.includes(firstShot.fps) ? firstShot.fps : 24,
        aspect: APP_ASPECTS[firstShot?.aspectRatio ?? ""] ?? "16:9",
        room: { enabled: false, width: 10, depth: 10, height: 4 },
        entities: [...scene.objects.map((object) => objectToEntity(object, appAssetsOf(scene))), ...cameraEntities],
        cuts,
        references: [],
        extensions: { [DIRECTOR_CANVAS_EXTENSION_KEY]: extension },
    };
}

type DocumentView = { name: string; entities: DirectorEntity[]; extensions: unknown };

/** 应用回传可能是 Project v1/v2,也可能是 SceneDocument v3(取当前戏段)。 */
function documentView(input: unknown): DocumentView {
    if (!isRecord(input) || input.format !== "director-desk") throw new Error("director_document_invalid");
    if (input.version === 3) {
        const document = input as unknown as DirectorSceneDocument;
        const active = Array.isArray(document.scenes) ? document.scenes.find((entry) => isRecord(entry) && entry.id === document.activeSceneId) : undefined;
        if (!active || !isRecord(active.state) || !Array.isArray(active.state.entities)) throw new Error("director_document_invalid");
        return { name: String(document.name ?? ""), entities: active.state.entities, extensions: active.state.extensions };
    }
    if (!Array.isArray(input.entities)) throw new Error("director_document_invalid");
    return { name: String(input.name ?? ""), entities: input.entities as DirectorEntity[], extensions: input.extensions };
}

function canvasExtension(extensions: unknown): DirectorCanvasExtension {
    const extension = isRecord(extensions) ? extensions[DIRECTOR_CANVAS_EXTENSION_KEY] : undefined;
    if (!isRecord(extension)) throw new Error("director_document_missing_canvas_extension");
    const shaped = extension as Partial<DirectorCanvasExtension>;
    if (!isRecord(shaped.scene) || !Array.isArray(shaped.objects) || !Array.isArray(shaped.cameras) || !Array.isArray(shaped.lights) || !Array.isArray(shaped.shots)) {
        throw new Error("director_document_invalid_canvas_extension");
    }
    return shaped as DirectorCanvasExtension;
}

function mergeObject(entity: DirectorEntity, original: PrevisObject | undefined, host: PrevisObject | undefined, appAssets: AppAssetMap = {}): PrevisObject {
    if (!original) {
        const isActor = entity.kind === "actor";
        return {
            id: entity.id,
            name: entity.name,
            kind: isActor ? "actor" : "primitive",
            ...(isActor ? { archetype: entity.gender === "female" ? "woman" : "adult", pose: "stand" } : { primitive: primitiveForAsset(entity.asset) }),
            transform: { position: clone(entity.position), rotation: clone(entity.rotation), scale: clone(entity.scale) },
            color: entity.color,
            visible: entity.visible,
            castShadow: true,
            receiveShadow: true,
            keyframes: [],
            ...(entity.path ? { motionPath: mergePath(entity.path, undefined) } : {}),
        } as PrevisObject;
    }
    const projected = objectToEntity(original, appAssets);
    const next = clone(original);
    if (!same(entity.position, projected.position)) next.transform = { ...next.transform, position: clone(entity.position) };
    if (!same(entity.rotation, projected.rotation)) next.transform = { ...next.transform, rotation: clone(entity.rotation) };
    if (!same(entity.scale, projected.scale)) next.transform = { ...next.transform, scale: clone(entity.scale) };
    if (entity.name !== projected.name) next.name = entity.name;
    if (entity.color !== projected.color) next.color = entity.color;
    if (entity.visible !== projected.visible) next.visible = entity.visible;
    if (!same(entity.path, projected.path)) {
        const motionPath = mergePath(entity.path, original.motionPath);
        if (motionPath) next.motionPath = motionPath;
        else delete next.motionPath;
    }
    for (const field of RESOURCE_FIELDS) {
        if (host?.[field] !== undefined) next[field] = host[field];
    }
    return next;
}

function mergeCamera(entity: DirectorEntity, original: PrevisCamera | undefined): PrevisCamera {
    const base: PrevisCamera = original
        ? clone(original)
        : {
              id: entity.id,
              name: entity.name,
              transform: { position: clone(entity.position), rotation: clone(entity.rotation), scale: clone(entity.scale) },
              target: clone(entity.camera?.target ?? [0, 1, 0]),
              focalLength: entity.camera?.focal ?? 35,
              fov: 50,
              aperture: 2.8,
              focusDistance: 5,
              near: 0.05,
              far: 500,
              keyframes: [],
          };
    if (!original) return base;
    const projected = cameraToEntity(original);
    if (!same(entity.position, projected.position)) base.transform = { ...base.transform, position: clone(entity.position) };
    if (!same(entity.rotation, projected.rotation)) base.transform = { ...base.transform, rotation: clone(entity.rotation) };
    if (!same(entity.scale, projected.scale)) base.transform = { ...base.transform, scale: clone(entity.scale) };
    if (entity.name !== projected.name) base.name = entity.name;
    if (entity.camera && projected.camera) {
        if (entity.camera.focal !== projected.camera.focal) base.focalLength = entity.camera.focal;
        if (!same(entity.camera.target, projected.camera.target)) base.target = clone(entity.camera.target);
    }
    if (!same(entity.path, projected.path)) {
        const motionPath = mergePath(entity.path, original.motionPath);
        if (motionPath) base.motionPath = motionPath;
        else delete base.motionPath;
    }
    return base;
}

/**
 * @param document 应用回传的文档(不可信,形状不合法时抛错,调用方保留草稿)。
 * @param hostScene 宿主当前场景,用于补回未下发给应用的资源字段。
 */
export function directorDocumentToPrevisScene(document: DirectorProject | DirectorSceneDocument | unknown, hostScene?: PrevisScene | null): PrevisScene {
    const view = documentView(document);
    const extension = canvasExtension(view.extensions);
    const originalObjects = new Map(extension.objects.map((object) => [object.id, object]));
    const originalCameras = new Map(extension.cameras.map((camera) => [camera.id, camera]));
    const hostObjects = new Map((hostScene?.id === extension.scene.id ? hostScene.objects : []).map((object) => [object.id, object]));
    const previousAssets = appAssetsOf({ extensions: extension.sceneExtensions });
    const objectEntities = view.entities.filter((entity) => entity.kind !== "camera");
    const objects = objectEntities.map((entity) => mergeObject(entity, originalObjects.get(entity.id), hostObjects.get(entity.id), previousAssets));
    const nextAssets: AppAssetMap = {};
    objectEntities.forEach((entity, index) => {
        const record = entity as DirectorEntity & { parameters?: unknown; assetParameters?: unknown };
        const isDefault = entity.asset === objectAsset(objects[index]) && record.parameters === undefined && record.assetParameters === undefined && entity.build === "normal";
        if (isDefault) return;
        nextAssets[entity.id] = toJson({ kind: entity.kind, asset: entity.asset, parameters: record.parameters, assetParameters: record.assetParameters, build: entity.build });
    });
    const cameras = view.entities.filter((entity) => entity.kind === "camera" && entity.id !== PLACEHOLDER_CAMERA_ID).map((entity) => mergeCamera(entity, originalCameras.get(entity.id)));
    const cameraIds = new Set(cameras.map((camera) => camera.id));
    // 应用内删掉的机位不能留下悬空镜头:改挂到第一台仍存在的机位。
    const shots = extension.shots.map((shot) => (cameraIds.has(shot.cameraId) || !cameras[0] ? clone(shot) : { ...clone(shot), cameraId: cameras[0].id }));
    const projectedTitle = appName(extension.scene.title);
    const scene: PrevisScene = {
        id: extension.scene.id,
        version: 1,
        title: view.name && view.name !== projectedTitle ? view.name : extension.scene.title,
        background: extension.scene.background,
        environmentIntensity: extension.scene.environmentIntensity,
        environment: clone(extension.scene.environment),
        gridVisible: extension.scene.gridVisible,
        objects,
        cameras,
        lights: clone(extension.lights),
        shots,
        activeShotId: extension.scene.activeShotId,
        createdAt: extension.scene.createdAt,
        updatedAt: extension.scene.updatedAt,
    };
    const sceneExtensions: Record<string, unknown> = isRecord(extension.sceneExtensions) ? clone(extension.sceneExtensions) : {};
    if (Object.keys(nextAssets).length) sceneExtensions[DIRECTOR_APP_ASSETS_KEY] = nextAssets;
    else delete sceneExtensions[DIRECTOR_APP_ASSETS_KEY];
    if (extension.sceneExtensions !== undefined || Object.keys(sceneExtensions).length) scene.extensions = sceneExtensions;
    return scene;
}

/** 读取文档里保留的影策扩展(运行时视图状态等只需镜头/场景元数据)。 */
export function directorCanvasExtension(document: DirectorProject): DirectorCanvasExtension | undefined {
    return document.extensions?.[DIRECTOR_CANVAS_EXTENSION_KEY];
}

export function directorPreviewInput(scene: PrevisScene, shotId = scene.activeShotId, time = 0): DirectorPreviewInput {
    const project = previsSceneToDirectorDocument(scene);
    if (!scene.shots.some((shot) => shot.id === shotId)) throw new Error(`previs_shot_missing:${shotId}`);
    return { project, shotId, time, sourceSceneId: scene.id };
}
