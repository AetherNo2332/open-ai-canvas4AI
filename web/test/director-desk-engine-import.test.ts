import { expect, test } from "bun:test";
import { updateDirectorEntityTransform } from "@/integrations/director-desk";

test("bundled DirectorDesk engine exposes the real browser editor core", async () => {
    const module = await import("@/integrations/director-desk/vendor/director-desk-engine");
    expect(typeof module.Engine).toBe("function");
    expect(typeof module.configureCamera).toBe("function");
});

test("DirectorDesk transform updates the selected entity only", () => {
    const document = {
        format: "director-desk" as const,
        version: 1 as const,
        name: "scene",
        duration: 1,
        fps: 24,
        aspect: "16:9",
        room: { enabled: true, width: 1, depth: 1, height: 1 },
        entities: [
            { id: "actor-a", kind: "actor" as const, name: "A", asset: "person", color: "#ffffff", position: [0, 0, 0] as [number, number, number], rotation: [0, 0, 0] as [number, number, number], scale: [1, 1, 1] as [number, number, number], visible: true, locked: false, height: 1.8, build: "normal" as const, gender: "male" as const, path: null, face: "path" as const, faceTarget: "", clips: [], pose: {}, poseKeys: [], camera: null, count: 1, spacing: 0.75, seed: 1, reference: "" },
            { id: "actor-b", kind: "actor" as const, name: "B", asset: "person", color: "#ffffff", position: [1, 0, 0] as [number, number, number], rotation: [0, 0, 0] as [number, number, number], scale: [1, 1, 1] as [number, number, number], visible: true, locked: false, height: 1.8, build: "normal" as const, gender: "male" as const, path: null, face: "path" as const, faceTarget: "", clips: [], pose: {}, poseKeys: [], camera: null, count: 1, spacing: 0.75, seed: 1, reference: "" },
        ],
        cuts: [],
        references: [],
        extensions: { infiniteCanvas: { sourceSceneId: "scene", scene: { id: "scene", title: "scene", background: "#fff", environmentIntensity: 1, gridVisible: true, activeShotId: "", createdAt: "", updatedAt: "" }, lights: [], shots: [], motionPaths: {} } },
    };
    const next = updateDirectorEntityTransform(document, "actor-b", [2, 0, 0], [0, 1, 0], [1, 1, 1]);
    expect(next.entities.find((entity) => entity.id === "actor-a")?.position).toEqual([0, 0, 0]);
    expect(next.entities.find((entity) => entity.id === "actor-b")?.position).toEqual([2, 0, 0]);
});
