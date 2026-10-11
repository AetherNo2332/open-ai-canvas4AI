import type { PrevisLight, PrevisObject, PrevisScene, PrevisShot, PrevisVec3 } from "@/types/previs";

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
    /** Original canvas object is retained in a namespaced extension for lossless round-trip. */
    extensions?: { infiniteCanvasObject?: PrevisObject };
};

export type DirectorProjectExtensions = {
    infiniteCanvas: {
        sourceSceneId: string;
        scene: Pick<PrevisScene, "id" | "title" | "background" | "environmentIntensity" | "environment" | "gridVisible" | "activeShotId" | "createdAt" | "updatedAt">;
        lights: PrevisLight[];
        shots: PrevisShot[];
        motionPaths: Record<string, PrevisObject["motionPath"]>;
        sceneExtensions?: Record<string, unknown>;
    };
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

export type DirectorPreviewInput = {
    project: DirectorProject;
    shotId: string;
    time: number;
    sourceSceneId: string;
};
