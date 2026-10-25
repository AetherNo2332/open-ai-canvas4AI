import type { CanvasDrawingSnapshot } from "@/lib/canvas/canvas-drawing-storage";
import type { CanvasNodeData, CanvasNodeMetadata } from "@/types/canvas";

export type CanvasDrawingEngine = "tldraw" | "excalidraw";

export type CanvasDrawingEngineSetting = {
    defaultEngine: CanvasDrawingEngine;
    tldrawLicenseKey?: string;
    configured?: boolean;
    updatedBy?: string;
    createdAt?: string;
    updatedAt?: string;
};

export const DEFAULT_DRAWING_ENGINE: CanvasDrawingEngine = "excalidraw";

export function drawingEngineForNode(node?: Pick<CanvasNodeData, "metadata"> | null): CanvasDrawingEngine {
    if (node?.metadata?.drawingDocument) return node.metadata.drawingDocument.engine;
    // 旧绘图节点没有引擎标记，其快照只能由 tldraw 读取。
    return node?.metadata?.drawingEngine === "excalidraw" ? "excalidraw" : "tldraw";
}

/** The host names a drawing's native document `${nodeId}-document` when it first
 * creates it, so the name is shared only together with that document. Until then
 * local drafts use the same default; an empty node never carries an unsynced name. */
export function canvasDrawingDocumentId(node: Pick<CanvasNodeData, "id" | "metadata">) {
    return node.metadata?.drawingId || `${node.id}-document`;
}

/** Node fields that record a published native document, including its name. */
export function canvasDrawingDocumentMetadata(node: Pick<CanvasNodeData, "id" | "metadata">, document: CanvasDrawingSnapshot): CanvasNodeMetadata {
    return {
        drawingId: canvasDrawingDocumentId(node),
        drawingEngine: document.engine,
        drawingDocument: document,
        drawingRevision: document.revision,
        drawingUpdatedAt: document.updatedAt,
        drawingShapeCount: document.shapeCount,
        drawingPageCount: document.pageCount,
    };
}

export function resolveTldrawLicenseKey(configuredLicenseKey?: string) {
    return configuredLicenseKey?.trim() || import.meta.env.VITE_TLDRAW_LICENSE_KEY?.trim() || undefined;
}

export function isDrawingEngineAvailable(engine: CanvasDrawingEngine, configuredLicenseKey?: string) {
    return engine === "excalidraw" || Boolean(resolveTldrawLicenseKey(configuredLicenseKey));
}

export function drawingEngineLabel(engine: CanvasDrawingEngine) {
    return engine === "excalidraw" ? "Excalidraw" : "tldraw";
}
