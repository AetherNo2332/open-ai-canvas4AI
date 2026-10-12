import { AmbientLight, BoxGeometry, Color, DirectionalLight, GridHelper, Mesh, MeshStandardMaterial, PerspectiveCamera, Scene, WebGLRenderer, Group } from "three";
import { Engine } from "./vendor/director-desk-engine.js";
import type { DirectorProject } from "./previs-runtime-types";

export const DIRECTOR_DESK_VERSION = "0.4.11" as const;
export type DirectorRenderer = Pick<WebGLRenderer, "dispose"> & Partial<Pick<WebGLRenderer, "render" | "setSize">>;
export type DirectorRuntimeHost = { stage: HTMLElement; shot: HTMLElement; createRenderer?: (container: HTMLElement) => DirectorRenderer; onDocumentChange?: (document: DirectorProject) => void; onError?: (error: unknown) => void };
export type DirectorRuntime = { readonly scene: Scene; readonly editorCamera: PerspectiveCamera; readonly shotCamera: PerspectiveCamera; readonly renderers: readonly DirectorRenderer[]; readonly host: DirectorRuntimeHost; disposed: boolean; render(): void; syncDocument(document: DirectorProject): void; upstreamEngine?: Engine };

const knownPropAssets = new Set(["ground", "road", "building", "bench", "fence", "streetlight", "bed", "nightstand", "wardrobe", "desk", "chair", "sofa", "table", "lamp", "laptop", "cup", "sword", "car", "tree", "rock", "cube", "sphere", "cylinder", "wall", "stairs", "door"]);
const safeColor = (value: string) => /^#[0-9a-f]{6}$/i.test(value) ? value : "#a7bdd7";
const engineAsset = (entity: DirectorProject["entities"][number]) => entity.kind === "camera" ? "camera" : entity.kind === "actor" ? (entity.gender === "female" ? "woman" : "person") : knownPropAssets.has(entity.asset) ? entity.asset : "cube";

/** Convert the constrained canvas projection into the real DirectorDesk model. */
function directorProjectToEngineProject(document: DirectorProject) {
    return {
        format: "director-desk" as const, version: 1 as const, name: document.name, duration: Math.max(0.1, document.duration), fps: document.fps, aspect: document.aspect,
        room: { enabled: true, width: 20, depth: 20, height: 4 },
        entities: document.entities.map((entity) => ({ id: entity.id, kind: entity.kind, name: entity.name, asset: engineAsset(entity), color: safeColor(entity.color), position: entity.position, rotation: entity.rotation, scale: entity.scale, visible: entity.visible, locked: entity.locked, height: Math.max(0.2, entity.height || 1.75), build: entity.build, gender: entity.gender, path: entity.path, face: entity.face, faceTarget: entity.faceTarget, clips: [], pose: entity.pose || {}, poseKeys: [], camera: entity.camera, count: Math.max(1, entity.count || 1), spacing: entity.spacing || 0.75, seed: entity.seed || 42, reference: entity.reference || "" })),
        cuts: document.cuts, references: [], resources: [],
    };
}

function createBrowserRenderer(container: HTMLElement): DirectorRenderer {
    const renderer = new WebGLRenderer({ antialias: true, alpha: true, preserveDrawingBuffer: true });
    renderer.setPixelRatio(Math.min(globalThis.devicePixelRatio || 1, 1.5)); renderer.setSize(container.clientWidth || 1, container.clientHeight || 1, false); container.replaceChildren(renderer.domElement); return renderer;
}

/** Browser-safe DirectorDesk core. Electron, MCP and filesystem adapters stay outside this boundary. */
export function createDirectorRuntime(host: DirectorRuntimeHost): DirectorRuntime {
    const scene = new Scene(); scene.background = new Color("#c6c8c6"); scene.add(new AmbientLight(0xffffff, 1.1));
    const key = new DirectionalLight(0xffffff, 2.4); key.position.set(4, 6, 4); key.castShadow = true; scene.add(key); scene.add(new GridHelper(20, 20, 0x8b9298, 0xc7cbd0));
    const editorCamera = new PerspectiveCamera(45, 1, 0.03, 2000); editorCamera.position.set(7, 5.9, 7); const shotCamera = new PerspectiveCamera(45, 1, 0.03, 2000); shotCamera.position.set(4.8, 2.7, 6.8);
    let renderers: DirectorRenderer[] = []; let upstreamEngine: Engine | undefined; let currentDocument: DirectorProject | null = null; let transformSnapshot: DirectorProject | null = null;
    const runtime: DirectorRuntime = { scene, editorCamera, shotCamera, renderers, host, disposed: false, upstreamEngine: undefined,
        syncDocument(document) {
            currentDocument = structuredClone(document);
            if (upstreamEngine) { upstreamEngine.rebuild(directorProjectToEngineProject(currentDocument)); upstreamEngine.render(true); return; }
            scene.getObjectByName("director-desk-entities")?.removeFromParent(); const entities = new Group(); entities.name = "director-desk-entities";
            for (const entity of document.entities) { const material = new MeshStandardMaterial({ color: entity.color || "#ffffff", roughness: 0.82 }); const mesh = new Mesh(new BoxGeometry(entity.kind === "camera" ? 0.35 : 0.8, entity.kind === "camera" ? 0.25 : Math.max(0.8, entity.height), entity.kind === "camera" ? 0.6 : 0.8), material); mesh.name = entity.id; mesh.position.set(...entity.position); mesh.rotation.set(...entity.rotation); mesh.scale.set(...entity.scale); mesh.visible = entity.visible; entities.add(mesh); }
            scene.add(entities);
        },
        render() { if (runtime.disposed) return; if (upstreamEngine) { upstreamEngine.render(true); return; } renderers[0]?.render?.(scene, editorCamera); renderers[1]?.render?.(scene, shotCamera); },
    };
    try {
        if (!host.createRenderer) {
            upstreamEngine = new Engine(directorProjectToEngineProject({ format: "director-desk", version: 1, name: "DirectorDesk", duration: 1, fps: 24, aspect: "16:9", room: { enabled: true, width: 20, depth: 20, height: 4 }, entities: [], cuts: [], references: [], extensions: { infiniteCanvas: { sourceSceneId: "", scene: { id: "", title: "", background: "#c6c8c6", environmentIntensity: 1, environment: undefined, gridVisible: true, activeShotId: "", createdAt: "", updatedAt: "" }, lights: [], shots: [], motionPaths: {} } } }), host.stage, host.shot, {
                select: () => undefined, point: () => undefined, ground: () => undefined, stroke: () => undefined, joint: () => undefined,
                transformStart: () => { transformSnapshot = currentDocument ? structuredClone(currentDocument) : null; },
                transform: (position, rotation, scale) => { const selected = currentDocument?.entities[0]; if (selected) Object.assign(selected, { position, rotation, scale }); },
                transformEnd: (cancel = false) => { if (cancel && transformSnapshot) currentDocument = transformSnapshot; if (!cancel && currentDocument) host.onDocumentChange?.(structuredClone(currentDocument)); transformSnapshot = null; },
            });
            runtime.upstreamEngine = upstreamEngine;
        } else {
            const createRenderer = host.createRenderer || createBrowserRenderer; renderers = [createRenderer(host.stage), createRenderer(host.shot)]; (runtime as { renderers: readonly DirectorRenderer[] }).renderers = renderers;
        }
    } catch (error) { upstreamEngine?.dispose(); for (const renderer of renderers) renderer.dispose(); host.stage.replaceChildren?.(); host.shot.replaceChildren?.(); host.onError?.(error); throw error; }
    return runtime;
}

export function disposeDirectorRuntime(runtime: DirectorRuntime): void { if (runtime.disposed) return; runtime.disposed = true; runtime.upstreamEngine?.dispose(); for (const renderer of runtime.renderers) renderer.dispose(); runtime.host.stage.replaceChildren?.(); runtime.host.shot.replaceChildren?.(); runtime.scene.clear(); }
