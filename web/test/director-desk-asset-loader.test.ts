import { expect, test } from "bun:test";
import { createDirectorAssetLoader } from "@/integrations/director-desk/asset-loader";

test("asset loader resolves only authorized asset ids and never accepts machine paths or tokens", async () => {
    const requested: string[] = [];
    const loader = createDirectorAssetLoader({
        resolve: async (assetId) => {
            requested.push(assetId);
            return { url: `/api/resources/${assetId}/file`, mimeType: "model/gltf-binary" };
        },
        fetcher: async () => new Response(new Uint8Array([1, 2, 3]), { status: 200 }),
    });
    const result = await loader.load({ assetId: "asset-1" });
    expect(result.assetId).toBe("asset-1");
    expect(result.blob.size).toBe(3);
    expect(requested).toEqual(["asset-1"]);
    await expect(loader.load({ assetId: "asset-1", url: "file:///secret/model.glb" })).rejects.toThrow("director_asset_unauthorized");
    await expect(loader.load({ assetId: "asset-1", url: "https://example.test/model.glb?token=secret" })).rejects.toThrow("director_asset_unauthorized");
});

