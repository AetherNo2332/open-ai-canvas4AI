export type DirectorAssetReference = { assetId: string; url?: string };
export type DirectorResolvedAsset = { url: string; mimeType?: string };
export type DirectorLoadedAsset = { assetId: string; blob: Blob; mimeType?: string };

type AssetLoaderOptions = {
    resolve: (assetId: string, signal: AbortSignal) => Promise<DirectorResolvedAsset>;
    fetcher?: typeof fetch;
};

function assertSafeResolvedUrl(url: string) {
    if (!url || url.startsWith("file:") || url.startsWith("blob:") || url.includes("\\")) throw new Error("director_asset_unauthorized");
    let parsed: URL;
    try { parsed = new URL(url, typeof window === "undefined" ? "http://localhost" : window.location.origin); } catch { throw new Error("director_asset_unauthorized"); }
    if (!/^(?:\/api)?\/resources\/[A-Za-z0-9:_-]{1,160}\/file$/u.test(parsed.pathname)) throw new Error("director_asset_unauthorized");
    for (const key of parsed.searchParams.keys()) if (key !== "variant") throw new Error("director_asset_unauthorized");
}

export function createDirectorAssetLoader(options: AssetLoaderOptions) {
    const fetcher = options.fetcher || fetch;
    return {
        async load(reference: DirectorAssetReference, signal: AbortSignal = new AbortController().signal): Promise<DirectorLoadedAsset> {
            if (!/^[A-Za-z0-9:_-]{1,160}$/u.test(reference.assetId)) throw new Error("director_asset_unauthorized");
            if (reference.url) assertSafeResolvedUrl(reference.url);
            const resolved = reference.url ? { url: reference.url } : await options.resolve(reference.assetId, signal);
            assertSafeResolvedUrl(resolved.url);
            const resolvedUrl = new URL(resolved.url, typeof window === "undefined" ? "http://localhost" : window.location.origin);
            const sameOrigin = typeof window === "undefined" || resolvedUrl.origin === window.location.origin;
            const response = await fetcher(resolvedUrl.toString(), { credentials: sameOrigin ? "include" : "omit", redirect: "error", signal });
            if (!response.ok) throw new Error(`director_asset_fetch_failed:${response.status}`);
            return { assetId: reference.assetId, blob: await response.blob(), mimeType: resolved.mimeType || response.headers.get("content-type") || undefined };
        },
    };
}
