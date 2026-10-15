import { describe, expect, test } from "bun:test";
import { downloadCanvasMediaQueue, type CanvasMediaDownloadQueueItem } from "@/lib/canvas/canvas-media-download-queue";

const item = (id: string): CanvasMediaDownloadQueueItem => ({ node: { id, type: "video", title: id, position: { x: 0, y: 0 }, width: 1, height: 1, metadata: { content: `${id}.mp4` } }, fileName: `${id}.mp4` });

describe("画布媒体下载队列", () => {
    test("按顺序下载并在单项失败后继续", async () => {
        const order: string[] = [];
        const result = await downloadCanvasMediaQueue(
            [item("a"), item("b"), item("c")],
            async ({ fileName }) => {
                order.push(fileName);
                if (fileName === "b.mp4") throw new Error("blocked");
            },
            { delayMs: 0 },
        );

        expect(order).toEqual(["a.mp4", "b.mp4", "c.mp4"]);
        expect(result.map((entry) => entry.status)).toEqual(["success", "failed", "success"]);
        expect(result[1]?.error).toBeInstanceOf(Error);
    });

    test("空队列不调用下载器", async () => {
        let calls = 0;
        expect(
            await downloadCanvasMediaQueue([], async () => {
                calls += 1;
            }),
        ).toEqual([]);
        expect(calls).toBe(0);
    });
});
