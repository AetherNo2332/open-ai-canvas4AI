import type { CanvasDocument } from "./canvas-event-projection";
type Entity = Record<string, any>;
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
// Task identity and runtime progress come from host receipts. Existing Canary
// callbacks may display them locally, but they are never editor commands.
export const canvasTaskDisplayFields = new Set([
    "status",
    "errorDetails",
    "generationErrorCode",
    "resourceReloadAvailable",
    "failedPromptFingerprint",
    "taskId",
    "taskClientOperationId",
    "retryOf",
    "attemptGroupId",
    "taskStatus",
    "taskProgress",
    "taskStage",
    "taskMediaStage",
    "taskCanRecoverMedia",
    "taskProvider",
    "taskStartedAt",
    "taskCompletedAt",
    "taskDurationMs",
    "taskErrorCode",
    "taskOfficialStatus",
    "taskReceiptRecorded",
    "taskCreatedAt",
    "taskUpdatedAt",
]);
export function canvasPersistentEditorDraft(base: CanvasDocument, live: CanvasDocument): CanvasDocument {
    const next = structuredClone(live),
        nodes = new Map(((base.nodes as Entity[]) || []).map((node) => [node.id, node]));
    for (const node of (next.nodes as Entity[]) || []) {
        const previous = nodes.get(node.id);
        if (!node.metadata) continue;
        for (const key of canvasTaskDisplayFields) {
            if (previous?.metadata && Object.hasOwn(previous.metadata, key)) node.metadata[key] = structuredClone(previous.metadata[key]);
            else delete node.metadata[key];
        }
        for (const collection of ["storyboard", "batchTable"]) {
            const oldRows = new Map<string, Entity>((previous?.metadata?.[collection]?.rows || []).map((row: Entity) => [row.id, row]));
            for (const row of node.metadata?.[collection]?.rows || [])
                for (const key of canvasTaskDisplayFields) {
                    const old = oldRows.get(row.id);
                    if (old && Object.hasOwn(old, key)) row[key] = structuredClone(old[key]);
                    else delete row[key];
                }
        }
    }
    return next;
}

/** Progress can remain visible across another user's edit. A new host task or
 * task state invalidates that overlay before presenting the canonical result. */
export function carryCanvasTaskDisplay(base: CanvasDocument, live: CanvasDocument, target: CanvasDocument): CanvasDocument {
    const oldNodes = new Map(((base.nodes as Entity[]) || []).map((node) => [node.id, node]));
    const liveNodes = new Map(((live.nodes as Entity[]) || []).map((node) => [node.id, node]));
    function carry(before: Entity | undefined, local: Entity | undefined, next: Entity | undefined) {
        if (!local || !next || !same(before?.taskId, next.taskId) || !same(before?.taskStatus, next.taskStatus) || !same(before?.status, next.status)) return;
        for (const key of canvasTaskDisplayFields)
            if (!same(before?.[key], local[key]) && same(before?.[key], next[key])) {
                if (Object.hasOwn(local, key)) next[key] = structuredClone(local[key]);
                else delete next[key];
            }
    }
    for (const node of (target.nodes as Entity[]) || []) {
        const old = oldNodes.get(node.id),
            local = liveNodes.get(node.id);
        carry(old?.metadata, local?.metadata, node.metadata);
        for (const collection of ["storyboard", "batchTable"]) {
            const oldRows = new Map<string, Entity>((old?.metadata?.[collection]?.rows || []).map((row: Entity) => [row.id, row]));
            const liveRows = new Map<string, Entity>((local?.metadata?.[collection]?.rows || []).map((row: Entity) => [row.id, row]));
            for (const row of node.metadata?.[collection]?.rows || []) carry(oldRows.get(row.id), liveRows.get(row.id), row);
        }
    }
    return target;
}
