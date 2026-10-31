import { describe, expect, test } from "bun:test";
import { splitCanvasStoryboard } from "@/services/api/canvas-storyboard-split";

describe("canvas storyboard split API", () => {
    test("sends only the server split inputs and returns its receipt", async () => {
        let received: { path: string; body: Record<string, unknown>; signal?: AbortSignal } | undefined;
        const result = await splitCanvasStoryboard(
            "canvas-1",
            {
                nodeId: "board-1",
                rowIds: ["row-2", "row-3"],
                request: "让两镜之间的动作连续",
                shotCount: 4,
                shotDurationSeconds: 5,
                logicalModelId: "model-1",
                config: { channelId: "channel-1" },
                quoteOnly: true,
            },
            async (path, body, signal) => {
                received = { path, body, signal };
                return { taskId: "", partial: true, rowIds: ["row-2", "row-3"], quote: { amountMicrocredits: 12 } };
            },
        );

        expect(received?.path).toBe("/canvas-projects/canvas-1/storyboard/split");
        expect(received?.body).toEqual({
            nodeId: "board-1",
            rowIds: ["row-2", "row-3"],
            request: "让两镜之间的动作连续",
            shotCount: 4,
            shotDurationSeconds: 5,
            logicalModelId: "model-1",
            config: { channelId: "channel-1" },
            quoteOnly: true,
        });
        expect(result.partial).toBe(true);
        expect(result.quote.amountMicrocredits).toBe(12);
    });
});
