import { afterEach, expect, test } from "bun:test";
import { createScene } from "../../director-desk/src/scenes";
import { SceneSession } from "../../director-desk/src/scenes/sequence-session";
import { addDocumentScene, duplicateDocumentScene, readSceneDocument } from "../../director-desk/src/scenes/sequence-project";
import { defaultSurfaceLayer } from "../../director-desk/src/media/model";
import { createPrevisScene } from "@/lib/canvas/previs/previs-scene";
import { directorPreviewInput } from "@/integrations/director-desk/previs-adapter";
import { createDirectorRenderBridge } from "@/integrations/director-desk/render-bridge";
import { requestPrevisRender } from "@/integrations/director-desk/export-to-canvas";

const originalCrypto = Object.getOwnPropertyDescriptor(globalThis, "crypto")!;
const randomSource = globalThis.crypto;
const uuidV4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

afterEach(() => Object.defineProperty(globalThis, "crypto", originalCrypto));

function useHttpCrypto() {
    Object.defineProperty(globalThis, "crypto", {
        configurable: true,
        value: { getRandomValues: (array: Uint8Array) => randomSource.getRandomValues(array) },
    });
}

test("HTTP deployment creates the initial director scene and editing session", () => {
    useHttpCrypto();
    const project = createScene("light-stage");
    expect(project.entities.length).toBeGreaterThan(0);
    const ids = project.entities.map((entity) => entity.id);
    expect(new Set(ids).size).toBe(ids.length);
    ids.forEach((id) => expect(id).toMatch(uuidV4));
    const session = new SceneSession(project);
    expect(session.projectId).toMatch(uuidV4);
    expect(session.context.sessionId).toMatch(uuidV4);
});

test("HTTP deployment creates surface layers and adds or duplicates scenes", () => {
    useHttpCrypto();
    const project = createScene("light-stage");
    const document = readSceneDocument(project);
    const added = addDocumentScene(document, project, "New scene");
    expect(added.activeSceneId).toMatch(uuidV4);
    const duplicated = duplicateDocumentScene(added, added.activeSceneId, "Scene copy");
    expect(duplicated.activeSceneId).toMatch(uuidV4);
    expect(duplicated.activeSceneId).not.toBe(added.activeSceneId);
    expect(defaultSurfaceLayer("resource-1").id).toMatch(uuidV4);
});

test("HTTP deployment generates request identities for previews and canvas exports", async () => {
    const scene = createPrevisScene();
    useHttpCrypto();
    let previewId = "";
    const bridge = createDirectorRenderBridge({
        submit: async (_input, options) => {
            previewId = options.requestId;
            return { taskId: "preview-1", status: "queued" };
        },
    });
    await bridge.preview(scene);
    expect(previewId.slice("director-preview:".length)).toMatch(uuidV4);
    let exportId = "";
    await requestPrevisRender({ canvasId: "canvas-1", sceneId: scene.id, shotId: scene.activeShotId, kind: "whitebox-video", render: directorPreviewInput(scene, scene.activeShotId, 0) }, {
        submitPrevis: async (_input, operationId) => {
            exportId = operationId;
            return { taskId: "export-1" };
        },
    } as Parameters<typeof requestPrevisRender>[1]);
    expect(exportId.slice("director-export:".length)).toMatch(uuidV4);
});
