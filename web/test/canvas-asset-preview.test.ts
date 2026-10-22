import { afterEach, expect, test } from "bun:test";
import { appQueryClient } from "@/lib/query-client";
import { apiClient } from "@/services/api/request";
import { resolveCanvasAssetPreviewInput } from "@/services/canvas-asset-preview";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const originalAdapter = apiClient.defaults.adapter;
afterEach(() => {
    apiClient.defaults.adapter = originalAdapter;
    appQueryClient.clear();
});

const card = (metadata: CanvasNodeData["metadata"] = {}): CanvasNodeData => ({
    id: "character-node", type: CanvasNodeType.Text, title: "顾行", position: { x: 0, y: 0 }, width: 320, height: 400,
    metadata: { workflowKind: "character", characterAssetId: "character-asset", characterVersionId: "version-1", characterVersionPolicy: "current", ...metadata },
});

function characterTransport(representations: Array<{ role: string; resourceId: string; mediaType: string }>, versionId = "version-1") {
    let requests = 0;
    apiClient.defaults.adapter = async (config) => {
        expect(config.url).toBe("/characters/character-asset");
        requests++;
        return { config, status: 200, statusText: "OK", headers: {}, data: { code: 0, msg: "", data: {
            asset: { id: "character-asset", title: "顾行" }, character: { versionId, representations, visualStatus: "ready", voiceStatus: "missing", definition: {} },
        } } };
    };
    return () => requests;
}

test("an Agent-created character without a cover cache previews its real turnaround and shares lookups across rows", async () => {
    const requests = characterTransport([
        { role: "primary", resourceId: "primary-image", mediaType: "image" },
        { role: "turnaround_sheet", resourceId: "turnaround-image", mediaType: "image" },
    ]);
    const [first, second] = await Promise.all([resolveCanvasAssetPreviewInput(card()), resolveCanvasAssetPreviewInput(card())]);
    expect(first.storageKey).toBe("resource:turnaround-image");
    expect(first.fallback).toEndWith("/resources/turnaround-image/file");
    expect(second).toEqual(first);
    expect(requests()).toBe(1);
});

test("cached character file URLs become resource inputs for authorized display", async () => {
    const requests = characterTransport([]);
    const result = await resolveCanvasAssetPreviewInput(card({ characterCoverUrl: "/api/resources/character%20cover/file" }));
    expect(result).toEqual({ storageKey: "resource:character cover", fallback: "/api/resources/character%20cover/file" });
    expect(requests()).toBe(0);
});

test("a primary image works without a turnaround, while a voice-only card has no image", async () => {
    characterTransport([{ role: "primary", resourceId: "primary", mediaType: "image/png" }]);
    expect((await resolveCanvasAssetPreviewInput(card())).storageKey).toBe("resource:primary");
    appQueryClient.clear();
    characterTransport([{ role: "primary", resourceId: "voice", mediaType: "audio" }]);
    expect(await resolveCanvasAssetPreviewInput(card())).toEqual({ fallback: "" });
});

test("a pinned character cannot silently preview a different current version", async () => {
    characterTransport([{ role: "primary", resourceId: "new-image", mediaType: "image" }], "version-2");
    await expect(resolveCanvasAssetPreviewInput(card({ characterVersionPolicy: "pinned" }))).rejects.toThrow("固定版本");
});

test("ordinary media retain their storage and fallback inputs", async () => {
    const image = card({ workflowKind: undefined, storageKey: "resource:scene", content: "https://example.invalid/scene.png" });
    image.type = CanvasNodeType.Image;
    expect(await resolveCanvasAssetPreviewInput(image)).toEqual({ storageKey: "resource:scene", fallback: "https://example.invalid/scene.png" });
});
