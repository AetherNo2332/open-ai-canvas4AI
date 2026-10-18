import type { Operation } from "./command-contract.generated";
import type { CanvasDocument } from "./canvas-event-projection";
import { canvasTimelineCommands } from "./canvas-timeline-commands";
import { canvasDrawingCommands } from "./canvas-drawing-commands";
import { buildCanvasReferenceEdit } from "./canvas-reference-edits";
import { canvasPrevisCommands, type PrevisEditorFields } from "./canvas-previs-commands";
export type CanvasCapabilities = {
    protocolVersion: number;
    nodes: Array<{ type: string; baseType?: string; workflowKind?: string; patchFields: Record<string, { path: string }> }>;
    structuredFields: Record<string, Record<string, unknown>>;
    timelineFields?: { track: Record<string, unknown>; clip: Record<string, unknown>; media: Record<string, unknown> };
    previsFields?: PrevisEditorFields;
};
type Entity = Record<string, any>;
const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const entities = (doc: CanvasDocument, key: string) => (doc[key] as Entity[]) || [];
const at = (object: Entity | undefined, path: string) => path.split(".").reduce((value, key) => value?.[key], object);
function changedPaths(before: unknown, after: unknown, prefix = ""): string[] {
    if (same(before, after)) return [];
    const record = (v: unknown) => !!v && typeof v === "object" && !Array.isArray(v);
    if ((record(before) || before === undefined) && (record(after) || after === undefined) && (record(before) || record(after)))
        return [...new Set([...Object.keys(before || {}), ...Object.keys(after || {})])].flatMap((key) => changedPaths((before as Entity | undefined)?.[key], (after as Entity | undefined)?.[key], prefix ? prefix + "." + key : key));
    return [prefix];
}
function claim(paths: Set<string>, prefix: string) {
    for (const path of paths) if (path === prefix || path.startsWith(prefix + ".")) paths.delete(path);
}
function unmapped(id: string, paths: Set<string>) {
    if (paths.size) throw new Error("canvas_editor_unmapped:" + id + ":" + [...paths].sort().join(","));
}

/** Derive semantic operations from an editor commit, using paths published by
 * the host registry. Unknown unchanged extensions survive on the host; unknown
 * edits fail visibly until their actual domain operation has been migrated. */
export function canvasEditorCommands(before: CanvasDocument, after: CanvasDocument, capabilities: CanvasCapabilities): Operation[] {
    const operations: Operation[] = [];
    const referenceFields = after.referenceFieldsVersion === 1;
    const oldNodes = entities(before, "nodes"),
        nextNodes = entities(after, "nodes");
    const oldById = new Map(oldNodes.map((n) => [n.id, n])),
        nextById = new Map(nextNodes.map((n) => [n.id, n]));
    const oldEdges = entities(before, "connections"),
        nextEdges = entities(after, "connections"),
        nextEdgeIds = new Set(nextEdges.map((e) => e.id));
    for (const edge of oldEdges) if (!nextEdgeIds.has(edge.id) && !(referenceFields && edge.managedBy === "reference-fields-v1")) operations.push({ type: "delete_connection", id: edge.id });
    const removed = new Set(oldNodes.filter((n) => !nextById.has(n.id)).map((n) => n.id));
    const implicit = new Set<string>();
    for (const node of oldNodes) if (removed.has(node.id)) for (const id of node.metadata?.batchChildIds || []) if (removed.has(id)) implicit.add(id);
    for (const node of oldNodes) if (removed.has(node.id) && !implicit.has(node.id)) operations.push({ type: "delete_node", id: node.id });
    for (const node of nextNodes)
        if (!oldById.has(node.id)) {
            const add: Operation = { type: "add_node", id: node.id, nodeType: node.type };
            if (node.position?.x !== undefined) add.x = node.position.x;
            if (node.position?.y !== undefined) add.y = node.position.y;
            if (node.title !== undefined) add.title = node.title;
            if (node.metadata?.content !== undefined) add.content = node.metadata.content;
            operations.push(add);
        }
    for (const node of nextNodes) {
        const previous = oldById.get(node.id);
        const paths = new Set(changedPaths(previous || {}, node));
        const descriptor = capabilities.nodes.find((d) => d.baseType === node.type && d.workflowKind === node.metadata?.workflowKind) || capabilities.nodes.find((d) => d.type === node.type);
        if (!descriptor) throw new Error("canvas_editor_type_unsupported:" + node.type);
        for (const path of ["id", "type", "createdAt", "updatedAt", "parentId"]) claim(paths, path);
        if (!previous) {
            claim(paths, "position");
            claim(paths, "title");
            claim(paths, "metadata.content");
            if (node.metadata?.status === "idle" || node.metadata?.status === "success") claim(paths, "metadata.status");
        }
        const patch: Record<string, unknown> = {};
        if (referenceFields && node.type === "script" && !same(previous?.metadata?.storyboard?.referenceNodeIds || [], node.metadata?.storyboard?.referenceNodeIds || [])) {
            operations.push(...buildCanvasReferenceEdit(after, { kind: "set_board_references", nodeId: node.id, referenceNodeIds: node.metadata?.storyboard?.referenceNodeIds || [] }));
            claim(paths, "metadata.storyboard.referenceNodeIds");
        }
        for (const [key, field] of Object.entries(descriptor.patchFields)) {
            if (!field.path || !paths.size) continue;
            if (!same(at(previous, field.path), at(node, field.path)) && [...paths].some((path) => path === field.path || path.startsWith(field.path + ".") || field.path.startsWith(path + "."))) {
                patch[key] = structuredClone(at(node, field.path) ?? null);
                claim(paths, field.path);
            }
        }
        if (Object.keys(patch).length) operations.push({ type: "update_node", id: node.id, patch });
        if (node.type === "drawing" && node.metadata?.drawingDocument) {
            const drawing = canvasDrawingCommands(node.id, node.metadata.drawingId || "", previous?.metadata?.drawingDocument, node.metadata.drawingDocument);
            if (drawing) operations.push(drawing);
            for (const key of ["drawingDocument", "drawingRevision", "drawingUpdatedAt", "drawingShapeCount", "drawingPageCount", "drawingId"]) claim(paths, "metadata." + key);
        }
        if (node.type === "script" || node.type === "batch-table") {
            const kind = node.type === "script" ? "storyboard" : "batch",
                key = kind === "storyboard" ? "storyboard" : "batchTable";
            const oldRows = previous?.metadata?.[key]?.rows || [],
                rows = node.metadata?.[key]?.rows || [];
            const nextIds = new Set(rows.map((r: Entity) => r.id));
            const rowById = new Map<string, Entity>(oldRows.map((r: Entity) => [r.id, r]));
            const type = kind === "storyboard" ? "edit_storyboard" : "edit_batch_table";
            const outputOperations: Operation[] = [];
            for (const row of oldRows) if (!nextIds.has(row.id)) operations.push({ type, id: node.id, action: "remove", rowId: row.id });
            for (const row of rows) {
                const old = rowById.get(row.id);
                const rowPaths = new Set(changedPaths(old || {}, row));
                claim(rowPaths, "id");
                claim(rowPaths, "shotNumber");
                if (!old && row.status === "idle") claim(rowPaths, "status");
                for (const field of kind === "storyboard" ? ["imageNodeId", "videoNodeId"] : ["outputNodeId"]) {
                    if (same(old?.[field], row[field])) continue;
                    if (referenceFields && kind === "storyboard") {
                        outputOperations.push({ type: "bind_storyboard_output", id: node.id, rowId: row.id, outputMode: field === "imageNodeId" ? "image" : "video", sourceNodeId: row[field] || "" });
                        claim(rowPaths, field);
                        continue;
                    }
                    const relation = kind === "storyboard" ? "storyboard-output" : "batch-output";
                    const matches = (edge: Entity, target: string) => edge.relation === relation && edge.fromNodeId === node.id && edge.storyboardRowId === row.id && edge.toNodeId === target;
                    // Output IDs are domain effects of real binding edges, never
                    // arbitrary editable row fields or renderer task state.
                    if ((row[field] && nextEdges.some((edge) => matches(edge, row[field]))) || (!row[field] && old?.[field] && oldEdges.some((edge) => matches(edge, old[field]) && !nextEdgeIds.has(edge.id)))) claim(rowPaths, field);
                }
                const rowPatch: Record<string, unknown> = {};
                for (const field of Object.keys(capabilities.structuredFields[kind] || {}))
                    if (!same(old?.[field], row[field])) {
                        rowPatch[field] = structuredClone(row[field] ?? null);
                        claim(rowPaths, field);
                    }
                unmapped("row:" + row.id, rowPaths);
                if (!old || Object.keys(rowPatch).length) operations.push({ type, id: node.id, action: old ? "update" : "append", rowId: row.id, patch: rowPatch });
            }
            operations.push(...outputOperations);
            const previousOrder = oldRows.map((r: Entity) => r.id),
                order = rows.map((r: Entity) => r.id);
            if (
                !same(
                    previousOrder.filter((id: string) => nextIds.has(id)),
                    order.slice(0, previousOrder.filter((id: string) => nextIds.has(id)).length),
                )
            )
                operations.push({ type: "reorder_rows", id: node.id, rowIds: order });
            claim(paths, "metadata." + key + ".rows");
        }
        // A new object may appear as a single metadata branch in changedPaths.
        if (paths.has("metadata") && node.metadata && Object.keys(node.metadata).every((key) => key === "content" || key === "status" || Object.values(descriptor.patchFields).some((f) => f.path === "metadata." + key))) paths.delete("metadata");
        unmapped("node:" + node.id, paths);
    }
    for (const node of nextNodes) if ((oldById.get(node.id)?.parentId || "") !== (node.parentId || "")) operations.push({ type: "set_parent", id: node.id, parentId: node.parentId || "" });
    const edgeById = new Map(oldEdges.map((e) => [e.id, e]));
    for (const edge of nextEdges) {
        if (referenceFields && edge.managedBy === "reference-fields-v1") continue;
        const previous = edgeById.get(edge.id);
        if (same(previous, edge)) continue;
        const fields = new Set(changedPaths(previous || {}, edge));
        for (const key of ["id", "fromNodeId", "toNodeId", "fromHandleId", "toHandleId", "fromAnchorRatio", "toAnchorRatio", "relation", "storyboardRowId"]) claim(fields, key);
        unmapped("connection:" + edge.id, fields);
        operations.push({
            type: previous ? "update_connection" : "connect_nodes",
            id: edge.id,
            fromNodeId: edge.fromNodeId,
            toNodeId: edge.toNodeId,
            ...(edge.fromHandleId ? { fromHandleId: edge.fromHandleId } : {}),
            ...(edge.toHandleId ? { toHandleId: edge.toHandleId } : {}),
            ...(edge.fromAnchorRatio !== undefined ? { fromAnchorRatio: edge.fromAnchorRatio } : {}),
            ...(edge.toAnchorRatio !== undefined ? { toAnchorRatio: edge.toAnchorRatio } : {}),
            ...(edge.relation ? { relation: edge.relation } : {}),
            ...(edge.storyboardRowId ? { storyboardRowId: edge.storyboardRowId } : {}),
        });
    }
    const rootChanges = new Set(changedPaths(before, after));
    for (const key of ["nodes", "connections", "id", "revision", "createdAt", "updatedAt", "remoteContentHash", "viewport", "selection", "panelState", "presence"]) claim(rootChanges, key);
    if (!same(before.title, after.title)) {
        operations.push({ type: "rename_canvas", id: String(after.id || before.id), title: String(after.title) });
        claim(rootChanges, "title");
    }
    const settings: Record<string, unknown> = {};
    for (const key of ["appearance", "backgroundMode", "showImageInfo", "starterMode"])
        if (!same(before[key], after[key])) {
            settings[key] = editorValuePatch(before[key], after[key]);
            claim(rootChanges, key);
        }
    if (Object.keys(settings).length) operations.push({ type: "edit_canvas_settings", id: String(after.id || before.id), patch: settings });
    if (!same(before.timeline, after.timeline)) {
        const timelineOperations = canvasTimelineCommands(before.timeline as Entity | undefined, after.timeline as Entity | undefined, capabilities.timelineFields);
        if (timelineOperations.length) operations.push({ type: "edit_timeline", id: String(after.id || before.id), timelineOperations });
        claim(rootChanges, "timeline");
    }
    if (!same(before.previsScenes, after.previsScenes)) {
        operations.push(...canvasPrevisCommands(String(after.id || before.id), before.previsScenes as Entity[] | undefined, after.previsScenes as Entity[] | undefined, capabilities.previsFields));
        claim(rootChanges, "previsScenes");
    }
    unmapped("canvas:" + String(after.id || before.id), rootChanges);
    if (
        !same(
            oldNodes
                .map((n) => n.id)
                .filter((id) => nextById.has(id))
                .concat(nextNodes.filter((n) => !oldById.has(n.id)).map((n) => n.id)),
            nextNodes.map((n) => n.id),
        )
    )
        operations.push({ type: "reorder_nodes", id: String(after.id || before.id), nodeIds: nextNodes.map((n) => n.id) });
    if (
        !referenceFields &&
        !same(
            oldEdges
                .map((e) => e.id)
                .filter((id) => nextEdgeIds.has(id))
                .concat(nextEdges.filter((e) => !edgeById.has(e.id)).map((e) => e.id)),
            nextEdges.map((e) => e.id),
        )
    )
        operations.push({ type: "reorder_connections", id: String(after.id || before.id), nodeIds: nextEdges.map((e) => e.id) });
    return operations;
}
function editorValuePatch(before: unknown, after: unknown): unknown {
    if (after && typeof after === "object" && !Array.isArray(after) && before && typeof before === "object" && !Array.isArray(before))
        return Object.fromEntries(
            [...new Set([...Object.keys(before), ...Object.keys(after)])].filter((key) => !same((before as Entity)[key], (after as Entity)[key])).map((key) => [key, editorValuePatch((before as Entity)[key], (after as Entity)[key])]),
        );
    return structuredClone(after ?? null);
}
