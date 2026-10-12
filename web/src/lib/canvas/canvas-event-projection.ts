import type { ChangeSet, Operation } from "./command-contract.generated";
export type CanvasDocument = Record<string, unknown>;
type Entity = Record<string, unknown> & { id: string };
const clone = <T>(value: T): T => structuredClone(value);
const objects = (value: unknown): Entity[] => (Array.isArray(value) ? (value as Entity[]) : []);
function put(target: Record<string, unknown>, key: string, value: unknown) {
    Object.defineProperty(target, key, { value, writable: true, enumerable: true, configurable: true });
}
function ordered(items: Entity[], order: string[] | undefined) {
    if (!order) return items;
    const byId = new Map(items.map((item) => [item.id, item]));
    if (order.length !== items.length || new Set(order).size !== items.length || order.some((id) => !byId.has(id))) throw new Error("invalid_event_order");
    return order.map((id) => byId.get(id)!);
}
function mergeDraftValue(base: unknown, draft: unknown, current: unknown): unknown {
    if (JSON.stringify(base) === JSON.stringify(draft)) return clone(current);
    if (base && draft && current && typeof base === "object" && typeof draft === "object" && typeof current === "object" && !Array.isArray(base) && !Array.isArray(draft) && !Array.isArray(current)) {
        const before = base as Record<string, unknown>,
            desired = draft as Record<string, unknown>,
            out = clone(current) as Record<string, unknown>;
        for (const key of new Set([...Object.keys(before), ...Object.keys(desired)])) {
            if (!(key in desired)) {
                if (key in before) delete out[key];
                continue;
            }
            put(out, key, mergeDraftValue(before[key], desired[key], out[key]));
        }
        return out;
    }
    return clone(draft);
}
// Draft ordering only orders objects known to that edit. Preserve objects that
// arrived from the host while the draft was awaiting acknowledgement.
export function applyCanvasDraftChanges(document: CanvasDocument, changes: ChangeSet, baseEntities: Record<string, Record<string, unknown>> = {}, baseFields: Record<string, unknown> = {}): CanvasDocument {
    const merged: ChangeSet = {
        entities: changes.entities.map((change) => {
            if (change.delete || !change.value) return change;
            const field = change.key.startsWith("node:") ? "nodes" : "connections";
            const id = change.key.slice(change.key.indexOf(":") + 1);
            const current = objects(document[field]).find((n) => n.id === id);
            return { ...change, value: mergeDraftValue(baseEntities[change.key], change.value, current) as Record<string, unknown> };
        }),
        fields: {},
    };
    for (const [key, change] of Object.entries(changes.fields || {})) merged.fields![key] = change.delete ? change : { value: mergeDraftValue(baseFields[key], change.value, document[key]) };
    const next = applyCanvasEventChanges(document, merged);
    for (const [field, order] of [
        ["nodes", changes.nodeOrder],
        ["connections", changes.connectionOrder],
    ] as const) {
        if (!order) continue;
        const items = objects(next[field]);
        const ids = new Set(items.map((n) => n.id));
        const desired = order.filter((id) => ids.has(id));
        const included = new Set(desired);
        put(next, field, ordered(items, [...desired, ...items.filter((n) => !included.has(n.id)).map((n) => n.id)]));
    }
    return next;
}
export function applyCanvasEventChanges(document: CanvasDocument, changes: ChangeSet): CanvasDocument {
    const next = clone(document);
    for (const change of changes.entities) {
        const colon = change.key.indexOf(":");
        const group = change.key.slice(0, colon),
            id = change.key.slice(colon + 1);
        if (colon < 1 || !id || !["node", "connection"].includes(group)) throw new Error("invalid_event_entity");
        const field = group === "node" ? "nodes" : "connections";
        const items = objects(next[field]);
        const index = items.findIndex((item) => item.id === id);
        if (change.delete) {
            if (index >= 0) items.splice(index, 1);
        } else {
            if (!change.value || change.value.id !== id) throw new Error("invalid_event_entity_id");
            const value = clone(change.value) as Entity;
            if (index < 0) items.push(value);
            else items[index] = value;
        }
        put(next, field, items);
    }
    for (const [field, change] of Object.entries(changes.fields || {})) {
        if (field === "nodes" || field === "connections") throw new Error("invalid_event_collection_field");
        if (change.delete) delete next[field];
        else put(next, field, change.value === undefined ? null : clone(change.value));
    }
    put(next, "nodes", ordered(objects(next.nodes), changes.nodeOrder));
    put(next, "connections", ordered(objects(next.connections), changes.connectionOrder));
    return next;
}
export function canvasDocumentChanges(before: CanvasDocument, after: CanvasDocument): ChangeSet {
    const changes: ChangeSet = { entities: [], fields: {} };
    for (const [field, prefix] of [
        ["nodes", "node:"],
        ["connections", "connection:"],
    ] as const) {
        const old = new Map(objects(before[field]).map((item) => [item.id, item]));
        const current = objects(after[field]);
        for (const item of current) {
            if (JSON.stringify(old.get(item.id)) !== JSON.stringify(item)) changes.entities.push({ key: prefix + item.id, value: clone(item) });
            old.delete(item.id);
        }
        for (const id of old.keys()) changes.entities.push({ key: prefix + id, delete: true });
        const order = current.map((n) => n.id);
        if (JSON.stringify(objects(before[field]).map((n) => n.id)) !== JSON.stringify(order)) changes[field === "nodes" ? "nodeOrder" : "connectionOrder"] = order;
    }
    for (const key of new Set([...Object.keys(before), ...Object.keys(after)])) {
        if (["nodes", "connections"].includes(key)) continue;
        if (JSON.stringify(before[key]) === JSON.stringify(after[key])) continue;
        changes.fields![key] = key in after ? { value: clone(after[key]) } : { delete: true };
    }
    return changes;
}
// This is presentation only. Host Plan is the authority for validation and the
// complete domain effects. Complex editors may supply their native draft diff.
export function projectCanvasOperations(document: CanvasDocument, operations: Operation[]): CanvasDocument {
    const next = clone(document);
    for (const op of operations) {
        const nodes = objects(next.nodes),
            edges = objects(next.connections),
            node = nodes.find((n) => n.id === op.id);
        switch (op.type) {
            case "update_node":
                if (node) {
                    const meta = (node.metadata && typeof node.metadata === "object" ? node.metadata : {}) as Record<string, unknown>;
                    for (const [key, value] of Object.entries(op.patch || {})) {
                        if (key === "x" || key === "y") {
                            const pos = (node.position || {}) as Record<string, unknown>;
                            put(pos, key, clone(value));
                            put(node, "position", pos);
                        } else if (["title", "width", "height", "parentId", "zIndex", "type"].includes(key)) put(node, key, clone(value));
                        else put(meta, key, clone(value));
                    }
                    put(node, "metadata", meta);
                }
                break;
            case "add_node":
                nodes.push({ id: op.id, type: op.nodeType, title: op.title || "", position: { x: op.x || 0, y: op.y || 0 }, width: 320, height: 240, metadata: { content: op.content || "" } });
                break;
            case "delete_node":
                next.nodes = nodes.filter((n) => n.id !== op.id);
                next.connections = edges.filter((e) => e.fromNodeId !== op.id && e.toNodeId !== op.id);
                break;
            case "delete_connection":
                next.connections = edges.filter((e) => e.id !== op.id);
                break;
            case "connect_nodes":
                edges.push({ id: op.id, fromNodeId: op.fromNodeId, toNodeId: op.toNodeId, fromHandleId: op.fromHandleId, toHandleId: op.toHandleId });
                break;
            case "set_parent":
                if (node) {
                    if (op.parentId) node.parentId = op.parentId;
                    else delete node.parentId;
                }
                break;
            case "replace_text":
                if (node && node.metadata) {
                    const m = node.metadata as Record<string, unknown>;
                    const key = (op.patch?.field as string)?.split(".").pop() || "content";
                    const text = String(m[key] || "");
                    if (op.match) m[key] = text.replace(op.match, op.replacement || "");
                }
                break;
            case "rename_canvas":
                next.title = op.title;
                break;
            case "reorder_nodes":
                if (op.nodeIds) next.nodes = ordered(nodes, op.nodeIds);
                break;
            case "reorder_connections":
                if (op.nodeIds) next.connections = ordered(edges, op.nodeIds);
                break;
            case "edit_canvas_settings":
                for (const [key, value] of Object.entries(op.patch || {})) put(next, key, mergeSettings(next[key], value));
                break;
            default:
                break;
        }
        if (!next.nodes) next.nodes = nodes;
        if (!next.connections) next.connections = edges;
    }
    return next;
}
function mergeSettings(before: unknown, patch: unknown): unknown {
    if (patch && typeof patch === "object" && !Array.isArray(patch)) {
        const next = clone(before && typeof before === "object" && !Array.isArray(before) ? before : {}) as Record<string, unknown>;
        for (const [key, value] of Object.entries(patch)) put(next, key, mergeSettings(next[key], value));
        return next;
    }
    return clone(patch);
}
