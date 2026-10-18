import { describe, expect, test } from "bun:test";
import { readZip } from "@/lib/zip";
import { createCanvasMediaArchive } from "@/lib/canvas/canvas-media-archive";
import type { CanvasMediaDownloadQueueItem } from "@/lib/canvas/canvas-media-download-queue";

const item = (id: string, data: string): CanvasMediaDownloadQueueItem => ({
    node: { id, type: "video", title: id, position: { x: 0, y: 0 }, width: 1, height: 1, metadata: { content: data } },
    fileName: `${id}.mp4`,
});

describe("画布媒体 ZIP", () => {
    test("把所有可读取节点按节点名写入一个压缩包并继续记录失败项", async () => {
        const result = await createCanvasMediaArchive([item("片头镜头", "data:video/mp4;base64,YQ=="), item("坏文件", "https://invalid.test/missing.mp4")], { fetchImpl: async (url) => url.startsWith("data:") ? new Response("a", { status: 200 }) : new Response(null, { status: 404 }) });
        expect(result.results.map((entry) => entry.status)).toEqual(["success", "failed"]);
        const files = await readZip(result.archive);
        expect([...files.keys()]).toEqual(["片头镜头.mp4"]);
        expect(await files.get("片头镜头.mp4")?.text()).toBe("a");
    });
});
