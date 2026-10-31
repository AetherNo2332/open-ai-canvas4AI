import type { CanvasStoryboardSplitResult } from "@/services/api/canvas-storyboard-split";
import type { CanvasNodeData, StoryboardRow } from "@/types/canvas";

export async function waitForStoryboardWriteback(input: { taskId: string; nodeId: string; signal: AbortSignal; refresh: () => Promise<CanvasNodeData[]>; now?: () => number; pause?: () => Promise<void>; timeoutMs?: number }) {
    const now = input.now ?? Date.now;
    const pause = input.pause ?? (() => new Promise<void>((resolve) => setTimeout(resolve, 1000)));
    const deadline = now() + (input.timeoutMs ?? 30_000);
    while (now() < deadline) {
        input.signal.throwIfAborted();
        const nodes = await input.refresh();
        input.signal.throwIfAborted();
        const node = nodes.find((item) => item.id === input.nodeId);
        if (node?.metadata?.taskId === input.taskId) {
            if (node.metadata.status === "error") throw new Error(node.metadata.errorDetails || "拆镜结果写回画布失败");
            if (node.metadata.taskStatus === "succeeded" && node.metadata.status === "success") return node.metadata.storyboard?.rows ?? [];
        }
        await pause();
    }
    return undefined;
}

export function storyboardWritebackSummary(before: StoryboardRow[], after: StoryboardRow[], rowIds?: string[]) {
    const replaced = before.filter((row) => (rowIds?.length ? rowIds.includes(row.id) : !row.locked)).length;
    const previous = new Set(before.map((row) => row.id));
    const added = after.filter((row) => !previous.has(row.id)).length;
    const review = after.filter((row) => row.mediaReview === "pending" && (!rowIds?.length || rowIds.includes(row.id))).length;
    return `已替换 ${replaced} 行，新增 ${added} 行，${review} 行画面待复核`;
}

export async function confirmStoryboardQuote(input: { quote: () => Promise<CanvasStoryboardSplitResult>; confirm: (quote: CanvasStoryboardSplitResult) => Promise<boolean>; signal: AbortSignal; now?: () => number }) {
    const now = input.now ?? Date.now;
    for (let attempt = 0; attempt < 3; attempt += 1) {
        input.signal.throwIfAborted();
        const quote = await input.quote();
        const expires = Date.parse(quote.quote.expiresAt);
        if (!Number.isFinite(expires) || !quote.quote.quoteHash) throw new Error("服务端拆镜报价回执无效");
        if (expires <= now()) continue;
        if (!(await input.confirm(quote))) return undefined;
        input.signal.throwIfAborted();
        if (expires > now()) return quote;
    }
    throw new Error("拆镜报价已过期，请重新获取报价并确认费用");
}
