import type { PrevisObject, PrevisScene, PrevisVec3 } from "@/types/previs";
import type { DirectorEntity, DirectorMotionPath, DirectorPreviewInput, DirectorProject } from "./previs-runtime-types";

const clone = <T>(value: T): T => structuredClone(value);

function safeObjectExtension(object: PrevisObject): PrevisObject {
    const { storageKey: _storageKey, url: _url, mimeType: _mimeType, ...safe } = object;
    return clone(safe as PrevisObject);
}

function objectToEntity(object: PrevisObject): DirectorEntity {
    const path: DirectorMotionPath | null = object.motionPath ? {
        smooth: true,
        interpolation: "continuous",
        points: object.motionPath.points.map((position, index) => ({ time: object.motionPath!.startDelay + index, position: clone(position) })),
        speed: object.motionPath.speed,
        startDelay: object.motionPath.startDelay,
        orientToPath: object.motionPath.orientToPath,
    } : null;
    return {
        id: object.id,
        kind: object.kind === "actor" ? "actor" : "prop",
        name: object.name,
        asset: object.assetId || object.primitive || object.kind,
        color: object.color,
        position: clone(object.transform.position),
        rotation: clone(object.transform.rotation),
        scale: clone(object.transform.scale),
        visible: object.visible,
        locked: false,
        height: object.actorProfile?.height || 1.75,
        build: "normal",
        gender: object.archetype === "woman" ? "female" : "male",
        path,
        face: "path",
        faceTarget: "",
        clips: clone(object.motionClips || []),
        pose: { value: object.pose || "neutral" },
        poseKeys: clone(object.boneTracks || []),
        camera: null,
        count: 1,
        spacing: 0.75,
        seed: 42,
        reference: object.sourceNodeId || "",
        extensions: { infiniteCanvasObject: safeObjectExtension(object) },
    };
}

function cameraToEntity(scene: PrevisScene, cameraId: string): DirectorEntity {
    const camera = scene.cameras.find((item) => item.id === cameraId);
    if (!camera) throw new Error(`previs_camera_missing:${cameraId}`);
    return {
        id: camera.id,
        kind: "camera",
        name: camera.name,
        asset: "camera",
        color: "#ffffff",
        position: clone(camera.transform.position),
        rotation: clone(camera.transform.rotation),
        scale: clone(camera.transform.scale),
        visible: true,
        locked: false,
        height: 1.6,
        build: "normal",
        gender: "male",
        path: camera.motionPath ? {
            smooth: true,
            interpolation: "continuous",
            points: camera.motionPath.points.map((position, index) => ({ time: camera.motionPath!.startDelay + index, position: clone(position) })),
            speed: camera.motionPath.speed,
            startDelay: camera.motionPath.startDelay,
            orientToPath: camera.motionPath.orientToPath,
        } : null,
        face: "target",
        faceTarget: "",
        clips: [],
        pose: {},
        poseKeys: [],
        camera: {
            aim: "target",
            focal: camera.focalLength,
            target: clone(camera.target),
            targetId: "",
            targetHeight: camera.target[1],
            mode: "free",
            offset: [0, 0, 0],
            inheritRotation: false,
            hideWalls: [],
        },
        count: 1,
        spacing: 0.75,
        seed: 42,
        reference: "",
    };
}

export function previsSceneToDirectorDocument(scene: PrevisScene): DirectorProject {
    const entities = [
        ...scene.objects.map(objectToEntity),
        ...scene.cameras.map((camera) => cameraToEntity(scene, camera.id)),
    ];
    const firstShot = scene.shots[0];
    let cutTime = 0;
    const cuts = scene.shots.map((shot) => {
        const cut = { time: cutTime, cameraId: shot.cameraId };
        cutTime += shot.duration;
        return cut;
    });
    return {
        format: "director-desk",
        version: 1,
        name: scene.title,
        duration: Math.max(0.1, scene.shots.reduce((total, shot) => total + shot.duration, 0)),
        fps: firstShot?.fps || 24,
        aspect: firstShot?.aspectRatio || "16:9",
        room: { enabled: false, width: 10, depth: 10, height: 4 },
        entities,
        cuts,
        references: [],
        extensions: {
            infiniteCanvas: {
                sourceSceneId: scene.id,
                scene: {
                    id: scene.id,
                    title: scene.title,
                    background: scene.background,
                    environmentIntensity: scene.environmentIntensity,
                    environment: clone(scene.environment),
                    gridVisible: scene.gridVisible,
                    activeShotId: scene.activeShotId,
                    createdAt: scene.createdAt,
                    updatedAt: scene.updatedAt,
                },
                lights: clone(scene.lights),
                shots: clone(scene.shots),
                motionPaths: Object.fromEntries(scene.objects.map((object) => [object.id, clone(object.motionPath)])),
                sceneExtensions: clone(scene.extensions),
            },
        },
    };
}

export function directorDocumentToPrevisScene(document: DirectorProject): PrevisScene {
    const bridge = document.extensions?.infiniteCanvas;
    if (!bridge) throw new Error("director_document_missing_canvas_extension");
    const objects = document.entities.filter((entity) => entity.kind !== "camera").map((entity) => {
        const preserved = entity.extensions?.infiniteCanvasObject;
        const motionPath = entity.path ? { points: entity.path.points.map((point) => clone(point.position)), speed: entity.path.speed || "walk", startDelay: entity.path.startDelay ?? (entity.path.points[0]?.time || 0), orientToPath: entity.path.orientToPath ?? true } : undefined;
        if (preserved) return {
            ...clone(preserved),
            transform: { ...preserved.transform, position: clone(entity.position), rotation: clone(entity.rotation), scale: clone(entity.scale) },
            motionPath,
            motionClips: Array.isArray(entity.clips) ? clone(entity.clips) as PrevisObject["motionClips"] : preserved.motionClips,
            pose: typeof entity.pose?.value === "string" ? entity.pose.value as PrevisObject["pose"] : preserved.pose,
        };
        return {
            id: entity.id,
            name: entity.name,
            kind: entity.kind === "actor" ? "actor" : "primitive",
            primitive: "box",
            transform: { position: clone(entity.position), rotation: clone(entity.rotation), scale: clone(entity.scale) },
            color: entity.color,
            visible: entity.visible,
            castShadow: true,
            receiveShadow: true,
            keyframes: [],
            motionPath,
        } as PrevisObject;
    });
    const cameras = document.entities.filter((entity) => entity.kind === "camera").map((entity) => ({
        id: entity.id,
        name: entity.name,
        transform: { position: clone(entity.position), rotation: clone(entity.rotation), scale: clone(entity.scale) },
        target: clone(entity.camera?.target || [0, 1, 0] as PrevisVec3),
        focalLength: entity.camera?.focal || 35,
        fov: 50,
        aperture: 2.8,
        focusDistance: 5,
        near: 0.05,
        far: 500,
        keyframes: [],
        motionPath: entity.path ? { points: entity.path.points.map((point) => clone(point.position)), speed: entity.path.speed || "walk", startDelay: entity.path.startDelay ?? (entity.path.points[0]?.time || 0), orientToPath: entity.path.orientToPath ?? true } : undefined,
    }));
    return {
        id: bridge.scene.id,
        version: 1,
        title: bridge.scene.title,
        background: bridge.scene.background,
        environmentIntensity: bridge.scene.environmentIntensity,
        environment: clone(bridge.scene.environment),
        gridVisible: bridge.scene.gridVisible,
        objects,
        cameras,
        lights: clone(bridge.lights),
        shots: clone(bridge.shots),
        activeShotId: bridge.scene.activeShotId,
        createdAt: bridge.scene.createdAt,
        updatedAt: bridge.scene.updatedAt,
        extensions: clone(bridge.sceneExtensions),
    };
}

export function directorPreviewInput(scene: PrevisScene, shotId = scene.activeShotId, time = 0): DirectorPreviewInput {
    const project = previsSceneToDirectorDocument(scene);
    if (!project.extensions.infiniteCanvas.shots.some((shot) => shot.id === shotId)) throw new Error(`previs_shot_missing:${shotId}`);
    return { project, shotId, time, sourceSceneId: scene.id };
}
