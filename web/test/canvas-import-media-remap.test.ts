import { describe, expect, test } from "bun:test";

import { remapImportedCanvasNodeMedia } from "@/lib/canvas/canvas-import-media-remap";
import { CanvasNodeType, type CanvasNodeData } from "@/types/canvas";

const isDeadBlob = (value?: string) => Boolean(value?.startsWith("blob:"));

function node(type: CanvasNodeType, metadata: CanvasNodeData["metadata"]): CanvasNodeData {
    return { id: "n", type, title: "镜头", position: { x: 0, y: 0 }, width: 320, height: 180, metadata };
}

describe("canvas import media remap", () => {
    const mapping = new Map([["resource:old", { storageKey: "resource:new", url: "https://cdn.example.com/new.png" }]]);

    test("媒体节点的 content 与 storageKey 跟随新账号资源", () => {
        const remapped = remapImportedCanvasNodeMedia(node(CanvasNodeType.Image, { content: "blob:dead", storageKey: "resource:old", previewContent: "blob:preview" }), mapping, isDeadBlob);
        expect(remapped.metadata?.content).toBe("https://cdn.example.com/new.png");
        expect(remapped.metadata?.storageKey).toBe("resource:new");
        expect(remapped.metadata?.previewContent).toBe("https://cdn.example.com/new.png");
    });

    test("映射缺失时保留可用旧引用，丢弃已死的 blob", () => {
        const remapped = remapImportedCanvasNodeMedia(node(CanvasNodeType.Video, { content: "blob:dead", storageKey: "resource:other" }), mapping, isDeadBlob);
        expect(remapped.metadata?.storageKey).toBe("resource:other");
        expect(remapped.metadata?.content).toBe("");
    });

    test("文本节点不会被媒体地址覆盖", () => {
        const remapped = remapImportedCanvasNodeMedia(node(CanvasNodeType.Text, { content: "设定正文", storageKey: "resource:old" }), mapping, isDeadBlob);
        expect(remapped.metadata?.content).toBe("设定正文");
        expect(remapped.metadata?.storageKey).toBe("resource:new");
    });

    test("旧浏览器的 blob 存储键不随导入持久化", () => {
        const remapped = remapImportedCanvasNodeMedia(node(CanvasNodeType.Image, { content: "blob:old", storageKey: "blob:old" }), mapping, isDeadBlob);
        expect(remapped.metadata?.storageKey).toBeUndefined();
    });
});
