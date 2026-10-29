import { nanoid } from "nanoid";

import { attachNodeToStoryboardRow } from "@/lib/canvas/canvas-project-domain";
import { canvasGenerationMode, readNodeGenerationSpec, validateGenerationSpec, type GenerationSpec, type ReferenceBinding } from "@/lib/canvas/generation-contract";
import type { CanvasConnection, CanvasNodeData } from "@/types/canvas";

/**
 * Declarative references (doc-level referenceFieldsVersion = 1): node fields own an input and the
 * visible line is projected from them, mirroring backend canvas/references (resolve.go, reconcile.go).
 * The browser save path (operations) never reconciles, so the editor keeps fields and lines in step.
 */
export const REFERENCE_FIELDS_VERSION = 1;
const MANAGED_BY = "reference-fields-v1";
const SPECIAL_ROLES = new Set(["first-frame", "last-frame", "mask"]);

export type ReferenceConnection = CanvasConnection & { managedBy?: string; referenceOwnerNodeId?: string; referenceBindingId?: string };
export type ReferenceGraph = { nodes: CanvasNodeData[]; connections: CanvasConnection[] };
export type ReferenceConnectionChange = { kind: "add" | "remove"; connection: CanvasConnection };
type Line = Omit<ReferenceConnection, "id">;
type LineBinding = Pick<ReferenceBinding, "id" | "nodeId" | "role">;
type InputKind = "text" | "image" | "video" | "audio" | "character";
type LineId = (line: Line) => string;

/** Same refusals as the fork editor: the line is drawn by another field, so deleting it here would only redraw it. */
export const SHARED_REFERENCE_MESSAGE = "这是表级共享引用或人物引用：请删除连到分镜表输入区的那条连线，或在人物字段中修改";
export const FIRST_FRAME_REFERENCE_MESSAGE = "首帧引用由镜头的首帧选择管理";
export class ReferenceLineOwnedError extends Error {
    constructor(message: string) {
        super(message);
        this.name = "ReferenceLineOwnedError";
    }
}

// Mirrors backend capability.BuiltinRegistry: InputKind, CanSource / CanReference and the target policy.
// generation-contract.generated.ts carries no per-node slot table, and web/ cannot import backend files at build time.
const SOURCE_KINDS: Record<string, { kind: InputKind; canSource: boolean; canReference: boolean }> = {
    text: { kind: "text", canSource: true, canReference: false },
    markdown: { kind: "text", canSource: true, canReference: false },
    skill: { kind: "text", canSource: true, canReference: false },
    svg: { kind: "text", canSource: true, canReference: false },
    html: { kind: "text", canSource: true, canReference: false },
    image: { kind: "image", canSource: true, canReference: true },
    video: { kind: "video", canSource: true, canReference: true },
    audio: { kind: "audio", canSource: true, canReference: true },
    character: { kind: "character", canSource: true, canReference: true },
};
// backend InputKind after completeBuiltinEditableFields; storyboard scopes bind any node that has one.
const INPUT_KINDS: Record<string, InputKind> = { ...Object.fromEntries(Object.entries(SOURCE_KINDS).map(([type, item]) => [type, item.kind])), chart: "text", script: "text", "batch-table": "text", drawing: "image", panorama: "image", compare: "image", colorgrade: "image", "media-conversion": "image" };
const TARGETS: Record<string, { field: "generation" | "node"; accepts: InputKind[]; max?: number }> = {
    image: { field: "generation", accepts: ["text", "image", "character"] },
    video: { field: "generation", accepts: ["text", "image", "video", "audio", "character"] },
    audio: { field: "generation", accepts: ["text", "character"], max: 1 },
    config: { field: "generation", accepts: ["text", "image", "video", "audio", "character"] },
    svg: { field: "node", accepts: ["text"] },
    html: { field: "node", accepts: ["text"] },
    chart: { field: "node", accepts: ["text"] },
    panorama: { field: "node", accepts: ["image"], max: 1 },
    compare: { field: "node", accepts: ["image"], max: 2 },
    colorgrade: { field: "node", accepts: ["image"], max: 1 },
    "media-conversion": { field: "node", accepts: ["image", "video"], max: 1 },
};

export function referenceFieldsEnabled(version: unknown) {
    return version === REFERENCE_FIELDS_VERSION;
}

function nodeKey(node: CanvasNodeData) {
    return node.type === "text" && node.metadata?.workflowKind === "character" ? "character" : node.type;
}

function sourceKind(node: CanvasNodeData) {
    return SOURCE_KINDS[nodeKey(node)];
}

/** Same tuple as backend references.edgeKey. */
function lineKey(edge: Line) {
    return [edge.referenceOwnerNodeId, edge.storyboardRowId, edge.referenceBindingId, edge.fromNodeId, edge.toNodeId, edge.fromHandleId, edge.toHandleId, edge.relation].map((value) => value || "").join("\u0000");
}

// Only row, storyboard context and batch handles move a line's anchor; other handle ids draw the same path.
function anchorHandle(handle?: string) {
    return handle && (handle.startsWith("row:") || handle === "storyboard:context" || handle.startsWith("batch-")) ? handle : "";
}

// FNV-1a over the backend edge key: every window derives the same id for the same projected line,
// so two editors opening one canvas converge instead of saving duplicate managed lines.
function hash32(value: string, seed: number) {
    let hash = seed >>> 0;
    for (let index = 0; index < value.length; index++) {
        hash ^= value.charCodeAt(index);
        hash = Math.imul(hash, 0x01000193) >>> 0;
    }
    return hash.toString(16).padStart(8, "0");
}

export function referenceLineId(line: Line) {
    const key = lineKey(line);
    return `ref-${hash32(key, 0x811c9dc5)}${hash32(key, 0x9747b28c)}`;
}

/** Lines with the same endpoints and anchors are the same visible reference, whoever owns them. */
function twinKey(edge: Pick<CanvasConnection, "fromNodeId" | "toNodeId" | "fromHandleId" | "toHandleId">) {
    return [edge.fromNodeId, edge.toNodeId, anchorHandle(edge.fromHandleId), anchorHandle(edge.toHandleId)].join("\u0000");
}

/** Mirrors backend references.scopesFor; batch rows keep their own cell projection. */
function ownerScoped(node: CanvasNodeData) {
    if (node.type === "batch-table") return false;
    return node.type === "script" || node.metadata?.generationSpec != null || node.metadata?.inputBindings !== undefined;
}

function declaredBindings(node: CanvasNodeData): ReferenceBinding[] | undefined {
    const metadata = node.metadata;
    if (metadata?.generationSpec != null) return Array.isArray(metadata.generationSpec.referenceBindings) ? metadata.generationSpec.referenceBindings : [];
    return metadata?.inputBindings;
}

// backend bindNode for storyboard scopes: text-kind sources are source text, characters and media are references.
function storyboardBinding(node: CanvasNodeData | undefined): LineBinding | undefined {
    const kind = node && INPUT_KINDS[nodeKey(node)];
    if (!node || !kind) return undefined;
    const role = kind === "text" ? "source-text" : "reference";
    return { id: `node:${node.id}:${role}`, nodeId: node.id, role };
}

function characterNodeId(character: { characterImageNodeId?: string; characterAssetId?: string }, nodes: CanvasNodeData[]) {
    if (character.characterImageNodeId) return character.characterImageNodeId;
    if (!character.characterAssetId) return undefined;
    const matches = nodes.filter((node) => node.metadata?.characterAssetId === character.characterAssetId);
    // The server rejects an ambiguous asset match instead of choosing one.
    return matches.length === 1 ? matches[0].id : undefined;
}

// backend resolveIndexed: a frame or mask role hides the plain reference to the same node, and each source draws one line.
function scopeLines(owner: CanvasNodeData, bindings: LineBinding[], nodeById: Map<string, CanvasNodeData>, anchor: Pick<Line, "storyboardRowId" | "toHandleId">): Line[] {
    const special = new Set(bindings.filter((binding) => SPECIAL_ROLES.has(binding.role)).map((binding) => binding.nodeId || ""));
    const seen = new Set<string>();
    const sources = new Set<string>();
    const lines: Line[] = [];
    for (const binding of bindings) {
        if (binding.role === "reference" && special.has(binding.nodeId || "")) continue;
        const key = binding.nodeId ? `${binding.nodeId}\u0000${binding.role}` : binding.id;
        if (seen.has(key)) continue;
        seen.add(key);
        if (!binding.nodeId || binding.nodeId === owner.id || !nodeById.has(binding.nodeId) || sources.has(binding.nodeId)) continue;
        sources.add(binding.nodeId);
        lines.push({ fromNodeId: binding.nodeId, toNodeId: owner.id, ...anchor, managedBy: MANAGED_BY, referenceOwnerNodeId: owner.id, referenceBindingId: binding.id });
    }
    return lines;
}

/** Managed lines one owner's fields project, in the order backend references.Reconcile appends them. */
function ownerLines(owner: CanvasNodeData, nodes: CanvasNodeData[], nodeById: Map<string, CanvasNodeData>): Line[] {
    if (!ownerScoped(owner)) return [];
    if (owner.type !== "script") return scopeLines(owner, declaredBindings(owner) || [], nodeById, {});
    const storyboard = owner.metadata?.storyboard;
    const shared = (storyboard?.referenceNodeIds || []).flatMap((id) => storyboardBinding(nodeById.get(id)) || []);
    const lines = scopeLines(owner, shared, nodeById, { toHandleId: "storyboard:context" });
    for (const row of storyboard?.rows || []) {
        const assets = [...(row.assetBindings || [])].sort((a, b) => (Number(b.priority) || 0) - (Number(a.priority) || 0)).flatMap((asset) => storyboardBinding(nodeById.get(asset.nodeId)) || []);
        const characters = (row.characters || []).flatMap((character) => storyboardBinding(nodeById.get(characterNodeId(character, nodes) || "")) || []);
        lines.push(...scopeLines(owner, [...assets, ...characters, ...shared], nodeById, { storyboardRowId: row.id, toHandleId: `row:${row.id}` }));
        for (const kind of ["image", "video"] as const) {
            const outputId = kind === "image" ? row.imageNodeId : row.videoNodeId;
            if (!outputId || nodeById.get(outputId)?.type !== kind) continue;
            lines.push({ fromNodeId: owner.id, toNodeId: outputId, fromHandleId: `row:${row.id}`, relation: "storyboard-output", storyboardRowId: row.id, managedBy: MANAGED_BY, referenceOwnerNodeId: owner.id, referenceBindingId: `output:${kind}` });
        }
    }
    return lines;
}

/**
 * Redraws the managed lines of the given owners like backend references.Reconcile: lines whose key the
 * fields still project keep their id, stale ones go, and free lines that duplicate one are dropped.
 * `adopt` lets a freshly drawn line become the managed line so its id and anchors survive.
 */
function reconcileOwners(nodes: CanvasNodeData[], connections: CanvasConnection[], ownerIds: Set<string>, createId: LineId, adopt: CanvasConnection[] = []): CanvasConnection[] {
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    const owners = new Set<string>();
    const desired = new Map<string, Line>();
    for (const node of nodes) {
        if (!ownerIds.has(node.id) || node.type === "batch-table") continue;
        owners.add(node.id);
        for (const line of ownerLines(node, nodes, nodeById)) {
            const key = lineKey(line);
            if (!desired.has(key)) desired.set(key, line);
        }
    }
    if (!owners.size) return connections;
    const twins = new Set([...desired.values()].map(twinKey));
    const found = new Set<string>();
    const next: CanvasConnection[] = [];
    let changed = false;
    for (const edge of connections as ReferenceConnection[]) {
        if (edge.managedBy === MANAGED_BY && owners.has(edge.referenceOwnerNodeId || "")) {
            const key = lineKey(edge);
            if (desired.has(key) && !found.has(key)) {
                found.add(key);
                next.push(edge);
            } else changed = true;
            continue;
        }
        if (edge.managedBy !== MANAGED_BY && twins.has(twinKey(edge))) {
            changed = true;
            continue;
        }
        next.push(edge);
    }
    const usedIds = new Set(next.map((edge) => edge.id));
    for (const [key, line] of desired) {
        if (found.has(key)) continue;
        const drawn = adopt.find((edge) => !usedIds.has(edge.id) && twinKey(edge) === twinKey(line));
        const anchors = drawn ? { ...(drawn.fromAnchorRatio !== undefined ? { fromAnchorRatio: drawn.fromAnchorRatio } : {}), ...(drawn.toAnchorRatio !== undefined ? { toAnchorRatio: drawn.toAnchorRatio } : {}) } : {};
        const derived = drawn?.id || createId(line);
        const id = usedIds.has(derived) ? nanoid() : derived;
        usedIds.add(id);
        next.push({ id, ...line, ...anchors });
        changed = true;
    }
    return changed ? next : connections;
}

/** Draws every line the reference fields declare; documents without referenceFieldsVersion are left as they are. */
export function projectReferenceConnections(nodes: CanvasNodeData[], connections: CanvasConnection[], version: unknown, createId: LineId = referenceLineId): CanvasConnection[] {
    if (!referenceFieldsEnabled(version)) return connections;
    return reconcileOwners(nodes, connections, new Set(nodes.filter(ownerScoped).map((node) => node.id)), createId);
}

function withBindings(node: CanvasNodeData, bindings: ReferenceBinding[], spec?: GenerationSpec): CanvasNodeData {
    const ordered = bindings.map((binding, order) => ({ ...binding, order }));
    if (spec) return { ...node, metadata: { ...node.metadata, generationSpec: validateGenerationSpec({ ...spec, referenceBindings: ordered }) } };
    return { ...node, metadata: { ...node.metadata, inputBindings: ordered } };
}

// A generation target without a stored contract gets one derived from its current fields, as the editor does on prompt edits.
function targetSpec(node: CanvasNodeData): GenerationSpec | undefined {
    if (node.metadata?.generationSpec != null) return validateGenerationSpec(node.metadata.generationSpec);
    if (node.type !== "config") return readNodeGenerationSpec(node);
    const mode = canvasGenerationMode(node.metadata?.generationMode || "image");
    if (!mode) return undefined;
    return validateGenerationSpec({ version: 1, mode, prompt: node.metadata?.composerContent || node.metadata?.prompt || "", options: {}, referenceBindings: [], textInputMode: "prompt-only" });
}

// Declared input edges must stay acyclic, otherwise the next server reconcile rejects the whole document.
function createsCycle(nodes: CanvasNodeData[], sourceId: string, targetId: string) {
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    const inputs = new Map<string, string[]>();
    for (const node of nodes) {
        for (const line of ownerLines(node, nodes, nodeById)) {
            if (line.relation !== "storyboard-output") inputs.set(line.toNodeId, [...(inputs.get(line.toNodeId) || []), line.fromNodeId]);
        }
    }
    // source -> target closes a loop when the source already depends on the target.
    const stack = [sourceId];
    const seen = new Set<string>();
    while (stack.length) {
        const id = stack.pop()!;
        if (id === targetId) return true;
        if (seen.has(id)) continue;
        seen.add(id);
        stack.push(...(inputs.get(id) || []));
    }
    return false;
}

function addNodeInput(nodes: CanvasNodeData[], source: CanvasNodeData, target: CanvasNodeData): CanvasNodeData[] {
    const policy = TARGETS[target.type];
    const kind = sourceKind(source);
    if (!policy || !kind || !policy.accepts.includes(kind.kind)) return nodes;
    const role = kind.kind === "text" ? "source-text" : "reference";
    if (role === "reference" ? !kind.canReference : !kind.canSource) return nodes;
    let spec: GenerationSpec | undefined;
    try {
        spec = target.metadata?.inputBindings !== undefined && target.metadata?.generationSpec == null ? undefined : policy.field === "generation" ? targetSpec(target) : undefined;
    } catch {
        return nodes;
    }
    if (policy.field === "generation" && !spec && target.metadata?.inputBindings === undefined) return nodes;
    const current = spec ? spec.referenceBindings : target.metadata?.inputBindings || [];
    if (current.some((binding) => binding.nodeId === source.id)) return nodes;
    const sources = new Set([...current.flatMap((binding) => (binding.nodeId ? [binding.nodeId] : [])), source.id]);
    if (policy.max && sources.size > policy.max) return nodes;
    if (createsCycle(nodes, source.id, target.id)) return nodes;
    const mediaType = kind.kind === "character" ? "image" : kind.kind;
    const binding: ReferenceBinding = { id: `node:${source.id}`, nodeId: source.id, mediaType, role, order: current.length, resolution: "latest" };
    return nodes.map((node) => (node.id === target.id ? withBindings(node, [...current, binding], spec) : node));
}

function storyboardRowId(handle?: string) {
    return handle?.startsWith("row:") ? handle.slice(4) : undefined;
}

function removeInput(nodes: CanvasNodeData[], edge: CanvasConnection): CanvasNodeData[] {
    const source = nodes.find((node) => node.id === edge.fromNodeId);
    const target = nodes.find((node) => node.id === edge.toNodeId);
    if (!source || !target) return nodes;
    const outputRow = source.type === "script" ? storyboardRowId(edge.fromHandleId) : undefined;
    if (outputRow && (target.type === "image" || target.type === "video")) {
        const key = target.type === "image" ? "imageNodeId" : "videoNodeId";
        return nodes.map((node) => {
            const storyboard = node.metadata?.storyboard;
            if (node.id !== source.id || !storyboard?.rows.some((row) => row.id === outputRow && row[key] === target.id)) return node;
            return { ...node, metadata: { ...node.metadata, storyboard: { ...storyboard, rows: storyboard.rows.map((row) => (row.id === outputRow ? { ...row, [key]: undefined } : row)) } } };
        });
    }
    if (target.type === "script") {
        const storyboard = target.metadata?.storyboard;
        if (!storyboard) return nodes;
        const rowId = storyboardRowId(edge.toHandleId);
        // Only a row's own assets are edited here; shared referenceNodeIds and cast characters redraw the line and are refused by the caller.
        const next = rowId
            ? { ...storyboard, rows: storyboard.rows.map((row) => (row.id !== rowId ? row : { ...row, assetBindings: (row.assetBindings || []).filter((binding) => binding.nodeId !== source.id) })) }
            : edge.toHandleId === "storyboard:context" ? { ...storyboard, referenceNodeIds: storyboard.referenceNodeIds.filter((id) => id !== source.id) } : storyboard;
        return next === storyboard ? nodes : nodes.map((node) => (node.id === target.id ? { ...node, metadata: { ...node.metadata, storyboard: next } } : node));
    }
    const current = declaredBindings(target);
    if (!current?.some((binding) => binding.nodeId === source.id)) return nodes;
    const spec = target.metadata?.generationSpec != null ? validateGenerationSpec(target.metadata.generationSpec) : undefined;
    return nodes.map((node) => (node.id === target.id ? withBindings(node, current.filter((binding) => binding.nodeId !== source.id), spec) : node));
}

/** Applies one drawn or removed line to the owning reference field; documents without referenceFieldsVersion are returned unchanged. */
export function applyConnectionToReferenceFields(nodes: CanvasNodeData[], change: ReferenceConnectionChange, version: unknown): CanvasNodeData[] {
    if (!referenceFieldsEnabled(version)) return nodes;
    const { connection } = change;
    if (change.kind === "remove") return removeInput(nodes, connection);
    const source = nodes.find((node) => node.id === connection.fromNodeId);
    const target = nodes.find((node) => node.id === connection.toNodeId);
    if (!source || !target || source.id === target.id) return nodes;
    const storyboardHandle = (handle?: string) => Boolean(storyboardRowId(handle)) || handle === "storyboard:context";
    if (storyboardHandle(connection.fromHandleId) || storyboardHandle(connection.toHandleId)) return attachNodeToStoryboardRow(nodes, connection);
    return addNodeInput(nodes, source, target);
}

/**
 * Adds drawn lines through the fields that own them. A line the fields now project becomes the managed
 * line (same id); lines no field accepts stay free, so behaviour on undeclared targets is unchanged.
 */
export function addReferenceConnections(graph: ReferenceGraph, candidates: CanvasConnection[], version: unknown, createId: LineId = referenceLineId): ReferenceGraph {
    if (!referenceFieldsEnabled(version)) return { nodes: graph.nodes, connections: [...graph.connections, ...candidates] };
    let nodes = graph.nodes;
    for (const candidate of candidates) nodes = applyConnectionToReferenceFields(nodes, { kind: "add", connection: candidate }, version);
    const owners = new Set(candidates.flatMap((edge) => [edge.toNodeId, edge.fromNodeId]).filter((id) => nodes.some((node) => node.id === id && ownerScoped(node))));
    const projected = reconcileOwners(nodes, graph.connections, owners, createId, candidates);
    const projectedTwins = new Set(projected.filter((edge) => (edge as ReferenceConnection).managedBy === MANAGED_BY).map(twinKey));
    const free = candidates.filter((edge) => !projected.some((item) => item.id === edge.id) && !projectedTwins.has(twinKey(edge)));
    return { nodes, connections: [...projected, ...free] };
}

// A keyframe storyboard's video takes its first frame from the row's image selection, not from the line.
function keyframeFirstFrame(nodes: CanvasNodeData[], edge: CanvasConnection) {
    return nodes.some((board) => board.metadata?.storyboardVideoInputMode === "keyframe" && board.metadata.storyboard?.rows.some((row) => row.videoNodeId === edge.toNodeId && row.imageNodeId === edge.fromNodeId));
}

/**
 * Removes a line with the field that would draw it again, and its exact twins; owner lines are then redrawn.
 * Throws ReferenceLineOwnedError, with the graph untouched, when another field (shared board reference,
 * cast character, keyframe first frame) would draw the line again.
 */
export function removeReferenceConnection(graph: ReferenceGraph, connectionId: string, version: unknown, createId: LineId = referenceLineId): ReferenceGraph {
    const edge = graph.connections.find((item) => item.id === connectionId);
    if (!edge) return graph;
    if (!referenceFieldsEnabled(version)) return { nodes: graph.nodes, connections: graph.connections.filter((item) => item.id !== connectionId) };
    if (keyframeFirstFrame(graph.nodes, edge)) throw new ReferenceLineOwnedError(FIRST_FRAME_REFERENCE_MESSAGE);
    const nodes = applyConnectionToReferenceFields(graph.nodes, { kind: "remove", connection: edge }, version);
    const twin = twinKey(edge);
    const remaining = graph.connections.filter((item) => item.id !== connectionId && twinKey(item) !== twin);
    const owners = new Set([edge.toNodeId, edge.fromNodeId, (edge as ReferenceConnection).referenceOwnerNodeId || ""]);
    const connections = reconcileOwners(nodes, remaining, owners, createId);
    if (connections.some((item) => twinKey(item) === twin)) throw new ReferenceLineOwnedError(SHARED_REFERENCE_MESSAGE);
    return { nodes, connections };
}

/** Drops declared inputs that point at deleted nodes; a dangling reference fails the next server reconcile. */
export function pruneReferenceFields(nodes: CanvasNodeData[], removedIds: Set<string>, version: unknown): CanvasNodeData[] {
    if (!referenceFieldsEnabled(version) || !removedIds.size) return nodes;
    let changed = false;
    const next = nodes.map((node) => {
        const current = node.type === "script" ? undefined : declaredBindings(node);
        if (!current?.some((binding) => binding.nodeId && removedIds.has(binding.nodeId))) return node;
        changed = true;
        const spec = node.metadata?.generationSpec != null ? validateGenerationSpec(node.metadata.generationSpec) : undefined;
        return withBindings(node, current.filter((binding) => !binding.nodeId || !removedIds.has(binding.nodeId)), spec);
    });
    return changed ? next : nodes;
}
