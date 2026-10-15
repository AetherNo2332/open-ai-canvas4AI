import { createZip } from "@/lib/zip";
import { getCachedResourceBlob } from "@/services/resource-blob-cache";
import { resourceIdFromStorageKey } from "@/services/api/resources";
import type { CanvasMediaDownloadQueueItem, CanvasMediaDownloadQueueResult } from "./canvas-media-download-queue";

type ArchiveOptions = { fetchImpl?: typeof fetch };

export async function createCanvasMediaArchive(items: CanvasMediaDownloadQueueItem[], options: ArchiveOptions = {}) {
    const fetchImpl = options.fetchImpl || fetch;
    const results: CanvasMediaDownloadQueueResult[] = [];
    const files: Array<{ name: string; data: Blob }> = [];
    for (const item of items) {
        try {
            const storageKey = item.node.metadata?.storageKey?.trim();
            const content = item.node.metadata?.content?.trim();
            const blob = storageKey && resourceIdFromStorageKey(storageKey)
                ? await getCachedResourceBlob(storageKey)
                : content ? await (async () => {
                    const response = await fetchImpl(content, { credentials: "same-origin" });
                    if (!response.ok) throw new Error(`媒体下载失败（${response.status}）`);
                    return response.blob();
                })() : null;
            if (!blob) throw new Error("媒体资源不存在");
            files.push({ name: item.fileName, data: blob });
            results.push({ item, status: "success" });
        } catch (error) {
            results.push({ item, status: "failed", error });
        }
    }
    return { archive: await createZip(files), results };
}
