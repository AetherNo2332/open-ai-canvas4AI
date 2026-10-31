import { describe, expect, test } from "bun:test";
import { waitForStoryboardWriteback, storyboardWritebackSummary, confirmStoryboardQuote } from "@/services/storyboard-writeback";
import { CanvasNodeType, type CanvasNodeData, type StoryboardRow } from "@/types/canvas";

const row = (id: string, extra: Partial<StoryboardRow> = {}) => ({ id, shotNumber: 1, durationSeconds: 5, plotDescription: id, ...extra }) as StoryboardRow;
const node = (status: "loading" | "success" | "error", errorDetails?: string) =>
    ({
        id: "board",
        type: CanvasNodeType.Script,
        title: "分镜",
        position: { x: 0, y: 0 },
        width: 600,
        height: 400,
        metadata: { taskId: "task", taskStatus: "succeeded" as const, status, errorDetails, storyboard: { rows: [row("new")], visibleColumns: [], referenceNodeIds: [] } },
    }) as CanvasNodeData;

describe("storyboard server writeback", () => {
    test("model completion waits for the canvas projection", async () => {
        let elapsed = 0;
        let reads = 0;
        const result = await waitForStoryboardWriteback({
            taskId: "task",
            nodeId: "board",
            signal: new AbortController().signal,
            refresh: async () => [node(++reads === 1 ? "loading" : "success")],
            now: () => elapsed,
            pause: async () => {
                elapsed += 1000;
            },
        });
        expect(reads).toBe(2);
        expect(result?.[0].id).toBe("new");
    });
    test("invalid output or edited table remains an error", async () => {
        await expect(
            waitForStoryboardWriteback({
                taskId: "task",
                nodeId: "board",
                signal: new AbortController().signal,
                refresh: async () => [node("error", "分镜表已修改，结果未写入")],
            }),
        ).rejects.toThrow("分镜表已修改");
    });
    test("timeout never reports success, including an older task's projection", async () => {
        let elapsed = 0;
        expect(
            await waitForStoryboardWriteback({
                taskId: "different-task",
                nodeId: "board",
                signal: new AbortController().signal,
                refresh: async () => [node("success")],
                now: () => elapsed,
                timeoutMs: 2000,
                pause: async () => {
                    elapsed += 1000;
                },
            }),
        ).toBeUndefined();
    });
    test("cancellation stops refresh", async () => {
        const controller = new AbortController();
        controller.abort();
        let read = false;
        await expect(
            waitForStoryboardWriteback({
                taskId: "task",
                nodeId: "board",
                signal: controller.signal,
                refresh: async () => {
                    read = true;
                    return [];
                },
            }),
        ).rejects.toThrow();
        expect(read).toBe(false);
    });
    test("summary reports replaced, new and review rows", () => {
        expect(storyboardWritebackSummary([row("a"), row("b"), row("c", { locked: true })], [row("a"), row("b", { mediaReview: "pending" }), row("c", { locked: true })], ["b"])).toBe("已替换 1 行，新增 0 行，1 行画面待复核");
        expect(storyboardWritebackSummary([row("a"), row("b")], [row("new1"), row("new2")], ["a", "b"])).toBe("已替换 2 行，新增 2 行，0 行画面待复核");
    });
});

test("an expired confirmation re-quotes and requires fresh approval", async () => {
    let clock = 0;
    let quotes = 0;
    let approvals = 0;
    const result = await confirmStoryboardQuote({
        signal: new AbortController().signal,
        now: () => clock,
        quote: async () => ({ partial: false, quote: { amountMicrocredits: ++quotes, quoteHash: String(quotes), expiresAt: new Date(clock + 1000).toISOString() } }),
        confirm: async () => {
            if (++approvals === 1) clock += 2000;
            return true;
        },
    });
    expect(quotes).toBe(2);
    expect(approvals).toBe(2);
    expect(result?.quote.quoteHash).toBe("2");
});
