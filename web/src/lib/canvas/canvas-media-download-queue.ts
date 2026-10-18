import type { CanvasNodeData } from "@/types/canvas";

export type CanvasMediaDownloadQueueItem = { node: CanvasNodeData; fileName: string };
export type CanvasMediaDownloadQueueResult = { item: CanvasMediaDownloadQueueItem; status: "success" | "failed"; error?: unknown };

type QueueOptions = { delayMs?: number };

export async function downloadCanvasMediaQueue(items: CanvasMediaDownloadQueueItem[], downloadOne: (item: CanvasMediaDownloadQueueItem) => Promise<void> | void, options: QueueOptions = {}) {
    const results: CanvasMediaDownloadQueueResult[] = [];
    const delayMs = Math.max(0, options.delayMs ?? 250);
    for (const [index, item] of items.entries()) {
        if (index > 0 && delayMs > 0) await new Promise((resolve) => setTimeout(resolve, delayMs));
        try {
            await downloadOne(item);
            results.push({ item, status: "success" });
        } catch (error) {
            results.push({ item, status: "failed", error });
        }
    }
    return results;
}
