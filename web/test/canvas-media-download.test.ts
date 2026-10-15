import { describe, expect, test } from "bun:test";
import { buildCanvasMediaDownloadFileName } from "@/lib/canvas/canvas-media-download";
import { isCanvasDownloadableMediaNode, resolveCanvasMediaDownloadItems } from "@/lib/canvas/canvas-media-batch-download";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

function node(overrides: Partial<CanvasNodeData>): CanvasNodeData {
    return { id: "node", type: CanvasNodeType.Video, title: "片头镜头", position: { x: 0, y: 0 }, width: 100, height: 100, metadata: { content: "https://cdn.test/clip.mp4", mimeType: "video/mp4" }, ...overrides };
}

describe("画布媒体下载命名", () => {
    test("使用节点标题和媒体扩展名", () => {
        expect(buildCanvasMediaDownloadFileName(node({}))).toBe("片头镜头.mp4");
    });

    test("清洗非法字符、空标题和保留设备名", () => {
        expect(buildCanvasMediaDownloadFileName(node({ title: '片头:/?*"<>|', type: CanvasNodeType.Image, metadata: { content: "data:image/png;base64,AA" } }))).toBe("片头.png");
        expect(buildCanvasMediaDownloadFileName(node({ title: "", type: CanvasNodeType.Audio, metadata: { content: "data:audio/mpeg;base64,AA" } }))).toBe("音频节点.mp3");
        expect(buildCanvasMediaDownloadFileName(node({ title: "CON" }))).toBe("_CON.mp4");
    });

    test("同名文件在扩展名前追加序号", () => {
        const used = new Set(["片头镜头.mp4"]);
        expect(buildCanvasMediaDownloadFileName(node({}), used)).toBe("片头镜头 (2).mp4");
    });

    test("混合选择只返回有媒体定位的图片视频音频节点", () => {
        const items = resolveCanvasMediaDownloadItems([
            node({ id: "video" }),
            node({ id: "text", type: CanvasNodeType.Text, title: "说明", metadata: { content: "文本" } }),
            node({ id: "empty", metadata: {} }),
            node({ id: "image", type: CanvasNodeType.Image, title: "封面", metadata: { storageKey: "resource:image" } }),
        ]);
        expect(items.map((item) => item.node.id)).toEqual(["video", "image"]);
        expect(isCanvasDownloadableMediaNode(node({ type: CanvasNodeType.Text }))).toBe(false);
    });
});
