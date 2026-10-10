import { isCanvasDrawingPublished, loadCanvasDrawing, restoreCanvasDrawingDocument, type CanvasDrawingSnapshot } from "@/lib/canvas/canvas-drawing-storage";
import type { CanvasDrawingEditorHandle } from "@/components/canvas/canvas-drawing-editor-types";
import { uploadResourceFile } from "@/services/api/resources";
import { imageToDataUrl } from "@/services/image-storage";
import type { CanvasNodeData } from "@/types/canvas";

const MAX_DRAWING_DOCUMENT_BYTES = 4 * 1024 * 1024;
const RESOURCE_REFERENCE = /^resource:[a-zA-Z0-9_-]+$/;
type RecordValue = Record<string, unknown>;
function record(value: unknown): value is RecordValue {
    return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function assetFields(document: CanvasDrawingSnapshot): Array<{ record: RecordValue; field: string; id: string }> {
    if (!record(document.snapshot)) throw new Error("绘图原生文档格式无效");
    const root = document.snapshot;
    if (document.engine === "excalidraw") {
        if (!Array.isArray(root.elements) || !record(root.files || {})) throw new Error("Excalidraw 文档缺少原生元素或文件");
        return Object.entries((root.files || {}) as RecordValue).map(([id, file]) => {
            if (!record(file)) throw new Error("Excalidraw 图片记录无效");
            return { record: file, field: "dataURL", id };
        });
    }
    const content = record(root.document) ? root.document : root;
    if (!record(content.store)) throw new Error("tldraw 文档缺少原生记录");
    return Object.entries(content.store).flatMap(([id, item]) => (record(item) && item.typeName === "asset" && record(item.props) && typeof item.props.src === "string" ? [{ record: item.props, field: "src", id }] : []));
}

function assertCredentialFree(value: unknown) {
    if (Array.isArray(value)) {
        value.forEach(assertCredentialFree);
        return;
    }
    if (record(value)) {
        for (const [key, item] of Object.entries(value)) {
            if (["__proto__", "constructor", "prototype"].includes(key) || /^(api[_-]?key|authorization|cookie|password|secret|token|access[_-]?token|refresh[_-]?token|headers)$/i.test(key)) throw new Error("绘图文档包含凭证或无效字段，未同步");
            assertCredentialFree(item);
        }
    } else if (typeof value === "string") {
        if (/^(data:|blob:)/i.test(value)) throw new Error("绘图文档仍包含内嵌或临时媒体，未同步");
        if (/^https?:\/\//i.test(value)) {
            const url = new URL(value);
            if (url.username || url.password || [...url.searchParams.keys()].some((key) => /token|signature|credential|secret|api.?key|password|^key$|authorization/i.test(key))) throw new Error("绘图文档包含授权 URL，未同步");
        }
    }
}

export async function publishCanvasDrawingDocument(document: CanvasDrawingSnapshot, publishAsset: (source: string, id: string) => Promise<string> = uploadDrawingAsset): Promise<CanvasDrawingSnapshot> {
    const next = structuredClone(document);
    if (next.engine === "tldraw" && record(next.snapshot)) delete next.snapshot.session;
    for (const asset of assetFields(next)) {
        const source = asset.record[asset.field];
        if (typeof source !== "string" || !source) throw new Error("绘图图片来源缺失，未同步");
        if (RESOURCE_REFERENCE.test(source)) continue;
        if (!source.startsWith("data:image/")) throw new Error("绘图图片须为本地图片或平台资源，未同步");
        const reference = await publishAsset(source, asset.id);
        if (!RESOURCE_REFERENCE.test(reference)) throw new Error("绘图图片未成功保存为平台资源");
        asset.record[asset.field] = reference;
    }
    assertCredentialFree(next.snapshot);
    if (new TextEncoder().encode(JSON.stringify(next)).length > MAX_DRAWING_DOCUMENT_BYTES) throw new Error("绘图原生文档过大（上限 4 MiB），已保留本地内容");
    return next;
}

async function uploadDrawingAsset(source: string, id: string) {
    const response = await fetch(source);
    if (!response.ok) throw new Error("无法读取绘图内嵌图片");
    const resource = await uploadResourceFile(await response.blob(), "image", { fileName: `drawing-${id}.png` });
    return `resource:${resource.id}`;
}

export async function hydrateCanvasDrawingDocument(document: CanvasDrawingSnapshot, hydrateAsset: (reference: string) => Promise<string> = (storageKey) => imageToDataUrl({ storageKey })): Promise<CanvasDrawingSnapshot> {
    const next = structuredClone(document);
    for (const asset of assetFields(next)) {
        const source = asset.record[asset.field];
        if (typeof source === "string" && RESOURCE_REFERENCE.test(source)) {
            const dataUrl = await hydrateAsset(source);
            if (!dataUrl.startsWith("data:image/")) throw new Error("绘图图片资源无法恢复");
            asset.record[asset.field] = dataUrl;
        }
    }
    return next;
}

export function selectCanvasDrawingDocument(local: CanvasDrawingSnapshot | null, remote?: CanvasDrawingSnapshot, localPublished = false) {
    if (!remote) return local;
    if (localPublished && local && (local.revision !== remote.revision || local.updatedAt !== remote.updatedAt)) return remote;
    if (!local || remote.revision > local.revision || (remote.revision === local.revision && remote.updatedAt > local.updatedAt)) return remote;
    return local;
}

export async function loadCanvasDrawingForNode(projectId: string, node: CanvasNodeData) {
    const local = await loadCanvasDrawing(projectId, node.metadata?.drawingId || "");
    const localPublished = Boolean(local && (await isCanvasDrawingPublished(projectId, node.metadata?.drawingId || "", local)));
    const selected = selectCanvasDrawingDocument(local, node.metadata?.drawingDocument, localPublished);
    if (!selected || selected === local) return local;
    const hydrated = await hydrateCanvasDrawingDocument(selected);
    return restoreCanvasDrawingDocument(projectId, node.metadata?.drawingId || "", hydrated);
}

export async function persistCanvasDrawingEditorSave(options: {
    node: CanvasNodeData;
    previous: CanvasDrawingSnapshot | null;
    expectedDocument?: CanvasDrawingSnapshot;
    createSave: CanvasDrawingEditorHandle["createSave"];
    saveLocal: (draft: Awaited<ReturnType<CanvasDrawingEditorHandle["createSave"]>>, previous: CanvasDrawingSnapshot | null) => Promise<CanvasDrawingSnapshot>;
    publish?: (saved: CanvasDrawingSnapshot) => Promise<CanvasDrawingSnapshot>;
    currentNode: () => CanvasNodeData | null;
    onSaved: (document: CanvasDrawingSnapshot) => void;
    markPublished?: (document: CanvasDrawingSnapshot) => Promise<void>;
}) {
    const expected = JSON.stringify(Object.hasOwn(options, "expectedDocument") ? options.expectedDocument : options.node.metadata?.drawingDocument);
    const draft = await options.createSave();
    const local = await options.saveLocal(draft, options.previous);
    const published = await (options.publish || publishCanvasDrawingDocument)(local);
    const current = options.currentNode();
    if (!current || current.metadata?.drawingId !== options.node.metadata?.drawingId || JSON.stringify(current.metadata?.drawingDocument) !== expected) throw new Error("绘图保存与远端修改冲突，已保留本地草稿；请关闭后重新核对");
    options.onSaved(published);
    await options.markPublished?.(published);
    return published;
}
