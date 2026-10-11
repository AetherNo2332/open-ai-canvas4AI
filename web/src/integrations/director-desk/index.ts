import { AmbientLight, Color, DirectionalLight, GridHelper, PerspectiveCamera, Scene, WebGLRenderer } from "three";

export const DIRECTOR_DESK_VERSION = "0.4.11" as const;

export type DirectorRenderer = Pick<WebGLRenderer, "dispose"> & Partial<Pick<WebGLRenderer, "render" | "setSize">>;

export type DirectorRuntimeHost = {
    stage: HTMLElement;
    shot: HTMLElement;
    createRenderer?: (container: HTMLElement) => DirectorRenderer;
    onError?: (error: unknown) => void;
};

export type DirectorRuntime = {
    readonly scene: Scene;
    readonly editorCamera: PerspectiveCamera;
    readonly shotCamera: PerspectiveCamera;
    readonly renderers: readonly DirectorRenderer[];
    readonly host: DirectorRuntimeHost;
    disposed: boolean;
    render(): void;
};

function createBrowserRenderer(container: HTMLElement): DirectorRenderer {
    const renderer = new WebGLRenderer({ antialias: true, alpha: true, preserveDrawingBuffer: true });
    renderer.setPixelRatio(Math.min(globalThis.devicePixelRatio || 1, 1.5));
    renderer.setSize(container.clientWidth || 1, container.clientHeight || 1, false);
    container.replaceChildren(renderer.domElement);
    return renderer;
}

/**
 * Browser-safe DirectorDesk core. Electron, MCP and filesystem adapters stay
 * outside this boundary; the canvas host owns persistence and authorization.
 */
export function createDirectorRuntime(host: DirectorRuntimeHost): DirectorRuntime {
    const scene = new Scene();
    scene.background = new Color("#c6c8c6");
    scene.add(new AmbientLight(0xffffff, 1.1));
    const key = new DirectionalLight(0xffffff, 2.4);
    key.position.set(4, 6, 4);
    key.castShadow = true;
    scene.add(key);
    scene.add(new GridHelper(20, 20, 0x8b9298, 0xc7cbd0));

    const editorCamera = new PerspectiveCamera(45, 1, 0.03, 2000);
    editorCamera.position.set(7, 5.9, 7);
    const shotCamera = new PerspectiveCamera(45, 1, 0.03, 2000);
    shotCamera.position.set(4.8, 2.7, 6.8);

    const createRenderer = host.createRenderer || createBrowserRenderer;
    let renderers: DirectorRenderer[] = [];
    try {
        renderers = [createRenderer(host.stage), createRenderer(host.shot)];
    } catch (error) {
        host.onError?.(error);
        throw error;
    }

    const runtime: DirectorRuntime = {
        scene,
        editorCamera,
        shotCamera,
        renderers,
        host,
        disposed: false,
        render() {
            if (runtime.disposed) return;
            renderers[0]?.render?.(scene, editorCamera);
            renderers[1]?.render?.(scene, shotCamera);
        },
    };
    return runtime;
}

export function disposeDirectorRuntime(runtime: DirectorRuntime): void {
    if (runtime.disposed) return;
    runtime.disposed = true;
    for (const renderer of runtime.renderers) renderer.dispose();
    runtime.host.stage.replaceChildren?.();
    runtime.host.shot.replaceChildren?.();
    runtime.scene.clear();
}
