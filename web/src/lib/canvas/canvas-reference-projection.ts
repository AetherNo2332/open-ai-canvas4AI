import { nanoid } from "nanoid";

import { connectionTraits } from "./connection-traits";
import type { ReferenceBinding } from "./generation-contract.generated";
import type { CanvasConnection, CanvasNodeData } from "@/types/canvas";

const MANAGED_BY = "reference-fields-v1";
const SPECIAL_ROLES = new Set(["first-frame", "last-frame", "mask"]);

type ReferenceLine = Omit<CanvasConnection, "id">;
type LineBinding = Pick<ReferenceBinding, "id" | "nodeId" | "role">;

/** Same tuple as backend references.edgeKey: the server keeps a managed line whose key it derives itself. */
export function canvasReferenceConnectionKey(edge: ReferenceLine) {
    return [edge.referenceOwnerNodeId, edge.storyboardRowId, edge.referenceBindingId, edge.fromNodeId, edge.toNodeId, edge.fromHandleId, edge.toHandleId, edge.relation].map((value) => value || "").join("\u0000");
}

// Only row, storyboard context and batch handles move a line's anchor; every other handle id draws the same path.
function anchorHandle(handle?: string) {
    return handle && (handle.startsWith("row:") || handle === "storyboard:context" || handle.startsWith("batch-")) ? handle : "";
}

/** Lines with the same endpoints and anchors are the same visible reference, whoever owns them. */
export function canvasReferenceTwinKey(edge: Pick<CanvasConnection, "fromNodeId" | "toNodeId" | "fromHandleId" | "toHandleId">) {
    return [edge.fromNodeId, edge.toNodeId, anchorHandle(edge.fromHandleId), anchorHandle(edge.toHandleId)].join("\u0000");
}

/** Mirrors backend references.scopesFor; batch rows keep their own cell projection. */
export function canvasReferenceOwnerScoped(node: CanvasNodeData) {
    if (node.type === "batch-table") return false;
    return node.type === "script" || node.metadata?.generationSpec != null || node.metadata?.inputBindings !== undefined;
}

// backend bindNode: text-kind sources are source text, characters and media are references.
function storyboardBinding(node: CanvasNodeData | undefined): LineBinding | undefined {
    if (!node) return undefined;
    const kind = node.type === "text" && node.metadata?.workflowKind === "character" ? "character" : connectionTraits[node.type]?.inputKind;
    if (!kind) return undefined;
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
function scopeLines(owner: CanvasNodeData, bindings: LineBinding[], nodeById: Map<string, CanvasNodeData>, anchor: Pick<CanvasConnection, "storyboardRowId" | "toHandleId">): ReferenceLine[] {
    const special = new Set(bindings.filter((binding) => SPECIAL_ROLES.has(binding.role)).map((binding) => binding.nodeId || ""));
    const seen = new Set<string>();
    const sources = new Set<string>();
    const lines: ReferenceLine[] = [];
    for (const binding of bindings) {
        if (binding.role === "reference" && special.has(binding.nodeId || "")) continue;
        const key = binding.nodeId ? `${binding.nodeId}\u0000${binding.role}` : binding.id;
        if (seen.has(key)) continue;
        seen.add(key);
        // Missing and self references fail server validation, so they never become lines.
        if (!binding.nodeId || binding.nodeId === owner.id || !nodeById.has(binding.nodeId) || sources.has(binding.nodeId)) continue;
        sources.add(binding.nodeId);
        lines.push({ fromNodeId: binding.nodeId, toNodeId: owner.id, ...anchor, managedBy: MANAGED_BY, referenceOwnerNodeId: owner.id, referenceBindingId: binding.id });
    }
    return lines;
}

/** Managed lines one owner's reference fields project, in the order backend references.Reconcile appends them. */
export function canvasReferenceOwnerConnections(owner: CanvasNodeData, nodes: CanvasNodeData[], nodeById = new Map(nodes.map((node) => [node.id, node]))): ReferenceLine[] {
    if (!canvasReferenceOwnerScoped(owner)) return [];
    const metadata = owner.metadata;
    if (owner.type !== "script") {
        const bindings = metadata?.generationSpec != null ? metadata.generationSpec.referenceBindings : metadata?.inputBindings;
        return scopeLines(owner, Array.isArray(bindings) ? bindings : [], nodeById, {});
    }
    const shared = (metadata?.storyboard?.referenceNodeIds || []).flatMap((id) => storyboardBinding(nodeById.get(id)) || []);
    const lines = scopeLines(owner, shared, nodeById, { toHandleId: "storyboard:context" });
    for (const row of metadata?.storyboard?.rows || []) {
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
 * Projects the managed lines of edited owners immediately, matching backend references.Reconcile.
 * The server stays authoritative; because the keys match, it keeps these ids instead of replacing them.
 * Free lines that duplicate a projected reference are dropped so a reference is never drawn twice.
 */
export function reconcileCanvasReferenceConnections(nodes: CanvasNodeData[], connections: CanvasConnection[], ownerNodeIds: Iterable<string>, createId: () => string = nanoid): CanvasConnection[] {
    const requested = new Set(ownerNodeIds);
    if (!requested.size) return connections;
    const nodeById = new Map(nodes.map((node) => [node.id, node]));
    const owners = new Set<string>();
    const desired = new Map<string, ReferenceLine>();
    for (const node of nodes) {
        if (!requested.has(node.id) || node.type === "batch-table") continue;
        owners.add(node.id);
        for (const line of canvasReferenceOwnerConnections(node, nodes, nodeById)) {
            const key = canvasReferenceConnectionKey(line);
            if (!desired.has(key)) desired.set(key, line);
        }
    }
    if (!owners.size) return connections;
    const twins = new Set([...desired.values()].map(canvasReferenceTwinKey));
    const found = new Set<string>();
    const next: CanvasConnection[] = [];
    let changed = false;
    for (const edge of connections) {
        if (edge.managedBy === MANAGED_BY && owners.has(edge.referenceOwnerNodeId || "")) {
            const key = canvasReferenceConnectionKey(edge);
            if (desired.has(key) && !found.has(key)) {
                found.add(key);
                next.push(edge);
            } else changed = true;
            continue;
        }
        if (edge.managedBy !== MANAGED_BY && twins.has(canvasReferenceTwinKey(edge))) {
            changed = true;
            continue;
        }
        next.push(edge);
    }
    for (const [key, line] of desired) {
        if (found.has(key)) continue;
        next.push({ id: createId(), ...line });
        changed = true;
    }
    return changed ? next : connections;
}
