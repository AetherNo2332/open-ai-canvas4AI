import type { PrevisCamera, PrevisLight, PrevisObject, PrevisScene, PrevisShot, PrevisVec3 } from "@/types/previs";

export type DirectorVec3 = PrevisVec3;

export type DirectorWaypoint = { time: number; position: DirectorVec3; heading?: number; easing?: string };

export type DirectorMotionPath = { interpolation?: "continuous"; smooth: boolean; points: DirectorWaypoint[]; speed?: "slow" | "walk" | "run"; startDelay?: number; orientToPath?: boolean };

export type DirectorCamera = {
    aim: "target" | "manual";
    focal: number;
    target: DirectorVec3;
    targetId: string;
    targetHeight: number;
    mode: "free" | "follow" | "pov";
    offset: DirectorVec3;
    inheritRotation: boolean;
    hideWalls: string[];
};

export type DirectorEntity = {
    id: string;
    kind: "actor" | "prop" | "camera" | "crowd";
    name: string;
    asset: string;
    color: string;
    position: DirectorVec3;
    rotation: DirectorVec3;
    scale: DirectorVec3;
    visible: boolean;
    locked: boolean;
    height: number;
    build: "slim" | "normal" | "broad";
    gender: "male" | "female";
    path: DirectorMotionPath | null;
    face: "path" | "fixed" | "target";
    faceTarget: string;
    clips: unknown[];
    pose: Record<string, unknown>;
    poseKeys: unknown[];
    camera: DirectorCamera | null;
    count: number;
    spacing: number;
    seed: number;
    reference: string;
};

/**
 * 影策场景在原版应用文档里的扩展区标识。原版应用的插件数据校验
 * (director-desk/src/plugins/project-data.ts)只接受 kebab-case 且值必须是纯 JSON。
 */
export const DIRECTOR_CANVAS_EXTENSION_KEY = "infinite-canvas";

/** 场景没有机位时应用仍要求至少一台摄影机;该占位机位只存在于应用文档,回传与视图状态都忽略它。 */
export const DIRECTOR_PLACEHOLDER_CAMERA_ID = "infinite-canvas-placeholder-camera";

/**
 * 无损往返所需的影策原值:应用只认识自己的白名单(资产、帧率、画幅、颜色),
 * 应用实体只承载可编辑投影,原始对象/机位/镜头在这里保留,回传时与投影比对合并。
 * 资源地址与存储键不进入扩展区(不下发给应用),回传时由宿主当前场景补回。
 */
export type DirectorCanvasExtension = {
    sourceSceneId: string;
    scene: Pick<PrevisScene, "id" | "title" | "background" | "environmentIntensity" | "environment" | "gridVisible" | "activeShotId" | "createdAt" | "updatedAt">;
    objects: PrevisObject[];
    cameras: PrevisCamera[];
    lights: PrevisLight[];
    shots: PrevisShot[];
    sceneExtensions?: Record<string, unknown>;
};

export type DirectorProjectExtensions = {
    [DIRECTOR_CANVAS_EXTENSION_KEY]: DirectorCanvasExtension;
};

export type DirectorProject = {
    format: "director-desk";
    version: 1;
    name: string;
    duration: number;
    fps: number;
    aspect: string;
    room: { enabled: boolean; width: number; depth: number; height: number };
    entities: DirectorEntity[];
    cuts: { time: number; cameraId: string }[];
    references: { id: string; name: string; data: string }[];
    extensions: DirectorProjectExtensions;
};

/** 原版应用的工作文档(SceneDocument v3,见 director-desk/src/scenes/sequence-project.ts)中宿主读取的部分。 */
export type DirectorSceneDocument = {
    format: "director-desk";
    version: 3;
    name: string;
    activeSceneId: string;
    resources: unknown[];
    scenes: { id: string; name: string; state: Pick<DirectorProject, "duration" | "fps" | "aspect" | "room" | "entities" | "cuts" | "references"> & { extensions?: Record<string, unknown> } }[];
};

export type DirectorPreviewInput = {
    project: DirectorProject;
    shotId: string;
    time: number;
    sourceSceneId: string;
};
