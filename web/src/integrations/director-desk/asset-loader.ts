export type DirectorAssetReference = { assetId: string; url?: string };
export type DirectorResolvedAsset = { url: string; mimeType?: string };
export type DirectorLoadedAsset = { assetId: string; blob: Blob; mimeType?: string };

type AssetLoaderOptions = {
    resolve: (assetId: string, signal: AbortSignal) => Promise<DirectorResolvedAsset>;
    fetcher?: typeof fetch;
};

function assertSafeResolvedUrl(url: string) {
    if (!url || url.startsWith("file:") || url.includes("token=") || url.includes("access_token=") || url.includes("\\")) throw new Error("director_asset_unauthorized");
    if (!url.startsWith("/") && !url.startsWith("blob:") && !/^https?:\/\/[^/?#]+(?:\/|$)/u.test(url)) throw new Error("director_asset_unauthorized");
}

export function createDirectorAssetLoader(options: AssetLoaderOptions) {
    const fetcher = options.fetcher || fetch;
    return {
        async load(reference: DirectorAssetReference, signal: AbortSignal = new AbortController().signal): Promise<DirectorLoadedAsset> {
            if (!/^[A-Za-z0-9:_-]{1,160}$/u.test(reference.assetId)) throw new Error("director_asset_unauthorized");
            if (reference.url) assertSafeResolvedUrl(reference.url);
            const resolved = reference.url ? { url: reference.url } : await options.resolve(reference.assetId, signal);
            assertSafeResolvedUrl(resolved.url);
            const response = await fetcher(resolved.url, { credentials: "include", redirect: "error", signal });
            if (!response.ok) throw new Error(`director_asset_fetch_failed:${response.status}`);
            return { assetId: reference.assetId, blob: await response.blob(), mimeType: resolved.mimeType || response.headers.get("content-type") || undefined };
        },
    };
}
