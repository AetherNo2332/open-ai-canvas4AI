import type { Operation } from "./command-contract.generated";
type Entity = Record<string, any>;
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
function drawingRecords(document: Entity): Map<string, Entity> {
    const snapshot = document.snapshot || {},
        records = document.engine === "excalidraw" ? ([...(snapshot.elements || []), ...Object.values(snapshot.files || {})] as Entity[]) : (Object.values((snapshot.document || snapshot).store || {}) as Entity[]);
    const result = new Map<string, Entity>();
    for (const record of records) {
        if (!record.id || result.has(record.id)) throw new Error("canvas_drawing_record_invalid");
        result.set(record.id, record);
    }
    return result;
}
/** Input is the published, credential-free document, with images already saved
 * as resources. Native records preserve their unknown engine extension fields. */
export function canvasDrawingCommands(nodeId: string, drawingId: string, previous: Entity | undefined, next: Entity): Operation | undefined {
    if (!["tldraw", "excalidraw"].includes(next.engine) || (previous && previous.engine !== next.engine)) throw new Error("canvas_drawing_engine_changed");
    if (same(previous?.snapshot, next.snapshot)) return;
    const before = previous ? drawingRecords(previous) : new Map<string, Entity>(),
        after = drawingRecords(next);
    const op: Operation = { type: "edit_drawing", id: nodeId, engine: next.engine, ...(drawingId ? { drawingId } : {}), drawingOperations: [] };
    for (const [id] of before) if (!after.has(id)) op.drawingOperations!.push({ type: "remove", id });
    for (const [id, record] of after) if (!same(before.get(id), record)) op.drawingOperations!.push({ type: "upsert", id, record: structuredClone(record) });
    if (!previous) {
        const snapshot = structuredClone(next.snapshot);
        delete snapshot.session;
        if (next.engine === "excalidraw") {
            snapshot.elements = [];
            snapshot.files = {};
        } else (snapshot.document || snapshot).store = {};
        op.drawingSnapshot = snapshot;
    }
    if (next.engine === "excalidraw") {
        const oldOrder = (previous?.snapshot.elements || []).map((record: Entity) => record.id),
            order = (next.snapshot.elements || []).map((record: Entity) => record.id);
        if (!same(oldOrder, order)) op.drawingElementIds = order;
        const beforeState = previous?.snapshot.appState || {},
            state = next.snapshot.appState || {},
            options: Entity = {};
        if (previous)
            for (const key of new Set([...Object.keys(beforeState), ...Object.keys(state)]))
                if (!same(beforeState[key], state[key])) {
                    if (!["viewBackgroundColor", "gridModeEnabled", "gridSize", "gridStep"].includes(key)) throw new Error("canvas_editor_unmapped:drawing:appState:" + key);
                    options[key] = structuredClone(state[key] ?? null);
                }
        if (Object.keys(options).length) op.drawingOptions = options;
    } else if (previous) {
        const oldEnvelope = structuredClone(previous.snapshot),
            envelope = structuredClone(next.snapshot);
        delete oldEnvelope.session;
        delete envelope.session;
        (oldEnvelope.document || oldEnvelope).store = {};
        (envelope.document || envelope).store = {};
        if (!same(oldEnvelope, envelope)) throw new Error("canvas_drawing_schema_changed");
    }
    if (!op.drawingSnapshot && !op.drawingOperations!.length && !op.drawingElementIds && !op.drawingOptions) return;
    return op;
}
