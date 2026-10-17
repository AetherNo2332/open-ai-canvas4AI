import { expect, test } from "bun:test";
import { createPrevisScene } from "@/lib/canvas/previs/previs-scene";
import { previsSceneToDirectorDocument, directorDocumentToPrevisScene } from "@/integrations/director-desk/previs-adapter";

test("round-trip preserves unknown scene extensions and metadata", () => {
    const scene = createPrevisScene();
    (scene as any).extensions = { userMarker: { mood: "blue" }, futureField: [1, 2, 3] };
    const document = previsSceneToDirectorDocument(scene);
    const restored = directorDocumentToPrevisScene(document);
    expect((restored as any).extensions).toEqual((scene as any).extensions);
    expect(document.extensions?.["infinite-canvas"]?.sourceSceneId).toBe(scene.id);
});

