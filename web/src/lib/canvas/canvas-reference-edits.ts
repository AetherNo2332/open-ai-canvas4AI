import type { Operation } from "./command-contract.generated";
import type { CanvasDocument } from "./canvas-event-projection";
import type { ReferenceBinding } from "./generation-contract.generated";
import type { CanvasNodeData, CanvasConnection, StoryboardAssetBinding, StoryboardRow } from "@/types/canvas";
import { bindingForNode, readNodeGenerationSpec, validateGenerationSpec, synchronizeGenerationSpec } from "./generation-contract";
import { storyboardDeclaredNodeIds } from "./canvas-reference-inputs";
import { bindingForConnectedNode } from "./canvas-storyboard-materializer";
import { applyCanvasConnectionPromptSync, buildCanvasNodeMentionReferenceMap, replaceCanvasReferenceMentions } from "./canvas-resource-references";
import { writeCanvasNodePrompt } from "./canvas-node-prompt";
import { canvasReferenceOwnerConnections, canvasReferenceOwnerScoped, canvasReferenceTwinKey, reconcileCanvasReferenceConnections } from "./canvas-reference-projection";
import { storyboardRowVideoDraft } from "./canvas-project-domain";
export type ReferenceEditIntent =
    | { kind: "set_row_assets"; nodeId: string; rowId: string; assetBindings: StoryboardAssetBinding[]; replaceNodeId?: string }
    | { kind: "set_board_references"; nodeId: string; referenceNodeIds: string[]; replaceNodeId?: string }
    | { kind: "set_node_inputs"; nodeId: string; bindings: ReferenceBinding[]; replaceNodeId?: string }
    | { kind: "bind_output"; nodeId: string; rowId: string; outputMode: "image" | "video"; outputNodeId: string }
    | { kind: "unbind_output"; nodeId: string; rowId: string; outputMode: "image" | "video" };
const inputTypes = new Set(["compare", "panorama", "svg", "html", "chart", "colorgrade", "media-conversion"]);
function nodeSpec(node: CanvasNodeData) {
    if (node.metadata?.generationSpec) return validateGenerationSpec(node.metadata.generationSpec);
    if (node.type === "config")
        return validateGenerationSpec({
            version: 1,
            mode: node.metadata?.generationMode === "video" ? "video" : node.metadata?.generationMode === "audio" ? "audio" : "image",
            prompt: node.metadata?.prompt || "",
            options: {},
            referenceBindings: [],
            textInputMode: "prompt-only",
        });
    return readNodeGenerationSpec(node);
}
export function buildCanvasReferenceEdit(doc: CanvasDocument, intent: ReferenceEditIntent): Operation[] {
    const node = ((doc.nodes as CanvasNodeData[]) || []).find((n) => n.id === intent.nodeId);
    if (!node) throw new Error("引用所属节点不存在");
    switch (intent.kind) {
        case "set_row_assets":
            if (!node.metadata?.storyboard?.rows.some((row) => row.id === intent.rowId)) throw new Error("分镜行不存在");
            return [{ type: "edit_storyboard", id: node.id, rowId: intent.rowId, action: "update", patch: { assetBindings: structuredClone(intent.assetBindings) } }];
        case "set_board_references":
            return [{ type: "edit_storyboard", id: node.id, action: "set_references", patch: { referenceNodeIds: [...intent.referenceNodeIds] } }];
        case "set_node_inputs": {
            const spec = nodeSpec(node),
                bindings = structuredClone(intent.bindings).map((b, order) => ({ ...b, order }));
            return [{ type: "update_node", id: node.id, patch: spec ? { generationSpec: validateGenerationSpec({ ...spec, referenceBindings: bindings }) } : { inputBindings: bindings } }];
        }
        case "bind_output":
        case "unbind_output":
            return [{ type: "bind_storyboard_output", id: node.id, rowId: intent.rowId, outputMode: intent.outputMode, sourceNodeId: intent.kind === "bind_output" ? intent.outputNodeId : "" }];
    }
}
export function applyCanvasReferenceEdit(nodes: CanvasNodeData[], intent: ReferenceEditIntent, connections?: CanvasConnection[]): CanvasNodeData[] {
    const [op] = buildCanvasReferenceEdit({ nodes }, intent);
    const next = nodes.map((node) => {
        if (node.id !== intent.nodeId) return node;
        const metadata = { ...node.metadata };
        if (op.type === "update_node") Object.assign(metadata, op.patch);
        else if (metadata.storyboard) {
            metadata.storyboard = { ...metadata.storyboard };
            if (intent.kind === "set_board_references") metadata.storyboard.referenceNodeIds = [...intent.referenceNodeIds];
            else
                metadata.storyboard.rows = metadata.storyboard.rows.map((row) => {
                    if (!("rowId" in intent) || row.id !== intent.rowId) return row;
                    if (intent.kind === "set_row_assets") return { ...row, assetBindings: structuredClone(intent.assetBindings) };
                    if (intent.kind === "bind_output" || intent.kind === "unbind_output") return { ...row, [intent.outputMode + "NodeId"]: intent.kind === "bind_output" ? intent.outputNodeId : undefined };
                    return row;
                });
        }
        return { ...node, metadata };
    });
    if (!connections) return next;
    if (intent.kind === "set_node_inputs" && intent.replaceNodeId) {
        const beforeReferences = buildCanvasNodeMentionReferenceMap(nodes, connections, [nodes.find((node) => node.id === intent.nodeId)!]).get(intent.nodeId) || [];
        const afterTarget = next.find((node) => node.id === intent.nodeId)!;
        let prompt = afterTarget.metadata?.composerContent ?? afterTarget.metadata?.prompt ?? "";
        const previousReference = beforeReferences.find((reference) => reference.nodeId === intent.replaceNodeId);
        const previousBindings = editableInputBindings(nodes.find((node) => node.id === intent.nodeId)!, nodes, connections);
        const replacementId = intent.bindings[previousBindings.findIndex((binding) => binding.nodeId === intent.replaceNodeId)]?.nodeId;
        if (previousReference && replacementId) prompt = replaceCanvasReferenceMentions(prompt, previousReference, `@[node:${replacementId}]`);
        if (prompt !== (afterTarget.metadata?.composerContent ?? afterTarget.metadata?.prompt ?? "")) {
            const prompted = writeCanvasNodePrompt(afterTarget, prompt);
            const index = next.findIndex((node) => node.id === intent.nodeId);
            next[index] = prompted;
        }
    }
    let replaceNodeId = intent.kind === "set_node_inputs" ? intent.replaceNodeId : "";
    let replacementNodeId = intent.kind === "set_node_inputs" ? intent.bindings[editableInputBindings(nodes.find((node) => node.id === intent.nodeId)!, nodes, connections).findIndex((binding) => binding.nodeId === replaceNodeId)]?.nodeId : "";
    if (intent.kind === "set_board_references" && intent.replaceNodeId) {
        replaceNodeId = intent.replaceNodeId;
        const previous = nodes.find((node) => node.id === intent.nodeId)?.metadata?.storyboard?.referenceNodeIds || [];
        replacementNodeId = intent.referenceNodeIds[previous.indexOf(replaceNodeId)] || "";
    }
    if (intent.kind === "set_row_assets" && intent.replaceNodeId) {
        replaceNodeId = intent.replaceNodeId;
        const previous = nodes.find((node) => node.id === intent.nodeId)?.metadata?.storyboard?.rows.find((row) => row.id === intent.rowId)?.assetBindings || [];
        replacementNodeId = intent.assetBindings[previous.findIndex((binding) => binding.nodeId === replaceNodeId)]?.nodeId || "";
    }
    return applyCanvasConnectionPromptSync(nodes, connections, next, connections, replaceNodeId && replacementNodeId ? { replaceNodeIds: new Map([[replaceNodeId, replacementNodeId]]) } : undefined);
}
function editableInputBindings(node: CanvasNodeData, nodes: CanvasNodeData[], connections: CanvasConnection[]): ReferenceBinding[] {
    const declared = node.metadata?.generationSpec?.referenceBindings ?? node.metadata?.inputBindings;
    if (declared !== undefined) return declared;
    const inputs = buildCanvasNodeMentionReferenceMap(nodes, connections, [node]).get(node.id) || [];
    const nodeById = new Map(nodes.map((input) => [input.id, input]));
    return inputs.flatMap((input, order) => {
        const source = nodeById.get(input.nodeId);
        const binding = source && bindingForNode(source, order);
        return binding ? [binding] : [];
    });
}
export function canvasReferenceIntentForConnection(nodes: CanvasNodeData[], edge: Pick<CanvasConnection, "fromNodeId" | "toNodeId" | "fromHandleId" | "toHandleId">, remove = false, connections: CanvasConnection[] = []): ReferenceEditIntent | undefined {
    const source = nodes.find((n) => n.id === edge.fromNodeId),
        target = nodes.find((n) => n.id === edge.toNodeId);
    if (!source || !target) return undefined;
    if (source.type === "script" && edge.fromHandleId?.startsWith("row:") && (target.type === "image" || target.type === "video"))
        return remove
            ? { kind: "unbind_output", nodeId: source.id, rowId: edge.fromHandleId.slice(4), outputMode: target.type === "image" ? "image" : "video" }
            : { kind: "bind_output", nodeId: source.id, rowId: edge.fromHandleId.slice(4), outputMode: target.type === "image" ? "image" : "video", outputNodeId: target.id };
    if (target.type === "script") {
        const row = edge.toHandleId?.startsWith("row:") ? target.metadata?.storyboard?.rows.find((r) => r.id === edge.toHandleId!.slice(4)) : undefined;
        if (row) {
            const own = row.assetBindings || [],
                binding = bindingForConnectedNode(source);
            if (remove && !own.some((b) => b.nodeId === source.id)) throw new Error("这是共享引用或人物引用，请在所属表或人物字段中修改");
            if (!binding) return undefined;
            return { kind: "set_row_assets", nodeId: target.id, rowId: row.id, assetBindings: remove ? own.filter((b) => b.nodeId !== source.id) : own.some((b) => b.nodeId === source.id) ? own : [...own, binding] };
        }
        const ids = target.metadata?.storyboard?.referenceNodeIds || [];
        return { kind: "set_board_references", nodeId: target.id, referenceNodeIds: remove ? ids.filter((id) => id !== source.id) : Array.from(new Set([...ids, source.id])) };
    }
    for (const board of nodes) {
        const row = board.metadata?.storyboard?.rows.find((row) => row.imageNodeId === target.id || row.videoNodeId === target.id);
        if (row) {
            if (remove && target.type === "video" && board.metadata?.storyboardVideoInputMode === "keyframe" && row.imageNodeId === source.id) throw new Error("首帧引用由镜头的首帧选择管理");
            return canvasReferenceIntentForConnection(nodes, { fromNodeId: source.id, toNodeId: board.id, toHandleId: `row:${row.id}` }, remove, connections);
        }
    }
    const spec = nodeSpec(target);
    if (!spec && !inputTypes.has(target.type)) return undefined;
    const bindings = editableInputBindings(target, nodes, connections);
    const binding = bindingForNode(source, bindings.length);
    if (!binding) return undefined;
    return { kind: "set_node_inputs", nodeId: target.id, bindings: remove ? bindings.filter((b) => b.nodeId !== source.id) : bindings.some((b) => b.nodeId === source.id) ? bindings : [...bindings, binding] };
}
export type CanvasReferenceGraph = { nodes: CanvasNodeData[]; connections: CanvasConnection[] };
// Undeclared legacy inputs are still drawn by their free lines.
function referenceLineKeys(graph: CanvasReferenceGraph, ownerId: string) {
    const owner = graph.nodes.find((node) => node.id === ownerId);
    if (!owner) return new Set<string>();
    if (canvasReferenceOwnerScoped(owner)) return new Set(canvasReferenceOwnerConnections(owner, graph.nodes).map(canvasReferenceTwinKey));
    return new Set(editableInputBindings(owner, graph.nodes, graph.connections).flatMap((binding) => (binding.nodeId ? [canvasReferenceTwinKey({ fromNodeId: binding.nodeId, toNodeId: owner.id })] : [])));
}
// Redraws one owner's lines after its fields changed: free lines of references it no longer declares go too.
function redrawReferenceLines(before: CanvasReferenceGraph, after: CanvasReferenceGraph, ownerId: string, removedConnectionIds: Iterable<string> = []): CanvasReferenceGraph {
    const previous = referenceLineKeys(before, ownerId);
    const next = referenceLineKeys(after, ownerId);
    const removed = new Set(removedConnectionIds);
    const retained = after.connections.filter((edge) => {
        if (removed.has(edge.id)) return false;
        const key = canvasReferenceTwinKey(edge);
        return edge.managedBy === "reference-fields-v1" || !previous.has(key) || next.has(key);
    });
    return { nodes: after.nodes, connections: reconcileCanvasReferenceConnections(after.nodes, retained.length === after.connections.length ? after.connections : retained, [ownerId]) };
}
/**
 * Applies a reference field edit and redraws the owner's lines in the same update; waiting for the
 * save round trip left lines late or, for free lines the server never manages, impossible to delete.
 * Free lines of references the edit removed go with them.
 */
export function applyCanvasReferenceGraphEdit(graph: CanvasReferenceGraph, intent: ReferenceEditIntent, removedConnectionIds: Iterable<string> = []): CanvasReferenceGraph {
    return redrawReferenceLines(graph, { nodes: applyCanvasReferenceEdit(graph.nodes, intent, graph.connections), connections: graph.connections }, intent.nodeId, removedConnectionIds);
}
/**
 * A video newly bound as a row's output is drafted from that row, like the storyboard's own shot videos:
 * shot title, prompt, template and duration, with its inputs declared from the row. `before` is the graph
 * the bind was applied to. Binding the same video again keeps a prompt the user has edited since.
 */
export function draftStoryboardVideoOutput(before: CanvasReferenceGraph, bound: CanvasReferenceGraph, intent: ReferenceEditIntent): CanvasReferenceGraph {
    if (intent.kind !== "bind_output" || intent.outputMode !== "video") return bound;
    const previous = before.nodes.find((node) => node.id === intent.nodeId)?.metadata?.storyboard?.rows.find((row) => row.id === intent.rowId);
    if (previous?.videoNodeId === intent.outputNodeId) return bound;
    const script = bound.nodes.find((node) => node.id === intent.nodeId);
    const row = script?.metadata?.storyboard?.rows.find((item) => item.id === intent.rowId);
    const video = bound.nodes.find((node) => node.id === intent.outputNodeId && node.type === "video");
    if (!script || !row || !video) return bound;
    const drafted = bound.nodes.map((node) => (node.id === video.id ? storyboardRowVideoDraft(script, row, video, bound.nodes) : node));
    // Inputs the video declared before binding are compared on the pre-bind graph, where they were its own.
    return redrawReferenceLines(before, { nodes: withStoryboardOutputDraft(drafted, script, row, video.id), connections: bound.connections }, video.id);
}
/**
 * Deletes a line together with every field that would draw it again. Undefined means the line is
 * not a reference; throws when another field owns the reference (shared, character, first frame).
 */
export function deleteCanvasReferenceLine(graph: CanvasReferenceGraph, edge: Pick<CanvasConnection, "fromNodeId" | "toNodeId" | "fromHandleId" | "toHandleId">): CanvasReferenceGraph | undefined {
    // Exact twins draw the same line; leaving one would look like the delete failed.
    const twin = canvasReferenceTwinKey(edge);
    const twinIds = graph.connections.filter((item) => canvasReferenceTwinKey(item) === twin);
    let intent: ReferenceEditIntent | undefined;
    try {
        intent = canvasReferenceIntentForConnection(graph.nodes, edge, true, graph.connections);
    } catch (error) {
        // The owning field redraws a managed line; a free duplicate can still be removed.
        if (twinIds.some((item) => item.managedBy === "reference-fields-v1")) throw error;
        return undefined;
    }
    if (!intent) return undefined;
    let next = applyCanvasReferenceGraphEdit(graph, intent, twinIds.map((item) => item.id));
    // A shot output keeps a copy of its row's references in its own spec, which draws the same line.
    const target = intent.nodeId === edge.toNodeId ? undefined : next.nodes.find((node) => node.id === edge.toNodeId);
    const declared = target?.metadata?.generationSpec?.referenceBindings ?? target?.metadata?.inputBindings;
    if (target && declared?.some((binding) => binding.nodeId === edge.fromNodeId)) next = applyCanvasReferenceGraphEdit(next, { kind: "set_node_inputs", nodeId: target.id, bindings: declared.filter((binding) => binding.nodeId !== edge.fromNodeId) });
    return next;
}
/** Connects candidates through the reference field that owns them, or as free lines when none does. */
export function connectCanvasReferences(graph: CanvasReferenceGraph, candidates: CanvasConnection[]): CanvasReferenceGraph {
    let next = graph;
    const free: CanvasConnection[] = [];
    for (const candidate of candidates) {
        const intent = canvasReferenceIntentForConnection(next.nodes, candidate, false, next.connections);
        if (intent) next = applyCanvasReferenceGraphEdit(next, intent);
        else free.push(candidate);
    }
    return free.length ? { nodes: next.nodes, connections: [...next.connections, ...free] } : next;
}
export function replaceCanvasReferenceInput(nodes: CanvasNodeData[], targetNodeId: string, oldNodeId: string, sourceNodeId: string, connections: CanvasConnection[] = []): ReferenceEditIntent | undefined {
    const target = nodes.find((node) => node.id === targetNodeId);
    const source = nodes.find((node) => node.id === sourceNodeId);
    if (!target || !source) return;
    if (target.type === "script") {
        const row = target.metadata?.storyboard?.rows.find((item) => item.assetBindings?.some((binding) => binding.nodeId === oldNodeId));
        if (row) {
            const replacement = bindingForConnectedNode(source);
            if (!replacement) return;
            return { kind: "set_row_assets", nodeId: target.id, rowId: row.id, assetBindings: row.assetBindings!.map((binding) => binding.nodeId === oldNodeId ? { ...replacement, ...binding, nodeId: replacement.nodeId } : binding), replaceNodeId: oldNodeId };
        }
        const index = target.metadata?.storyboard?.referenceNodeIds?.indexOf(oldNodeId) ?? -1;
        if (index >= 0) return { kind: "set_board_references", nodeId: target.id, referenceNodeIds: target.metadata!.storyboard!.referenceNodeIds.map((id, itemIndex) => itemIndex === index ? sourceNodeId : id), replaceNodeId: oldNodeId };
    }
    const spec = nodeSpec(target);
    if (!spec && !inputTypes.has(target.type)) return;
    const bindings = editableInputBindings(target, nodes, connections);
    const index = bindings.findIndex((binding) => binding.nodeId === oldNodeId);
    if (index < 0) return;
    const replacement = bindingForNode(source, index, bindings[index]);
    if (!replacement) return;
    return { kind: "set_node_inputs", nodeId: targetNodeId, bindings: bindings.map((binding, bindingIndex) => bindingIndex === index ? replacement : binding), replaceNodeId: oldNodeId };
}
export function storyboardInheritedBindings(script: CanvasNodeData, row: StoryboardRow, nodes: CanvasNodeData[]): Array<StoryboardAssetBinding & { source: string }> {
    const own = new Set((row.assetBindings || []).map((b) => b.nodeId));
    const characters = (row.characters || []).flatMap((character) => {
        const matches = character.characterImageNodeId
            ? nodes.filter((node) => node.id === character.characterImageNodeId)
            : character.characterAssetId
              ? nodes.filter((node) => node.metadata?.workflowKind === "character" && node.metadata.characterAssetId === character.characterAssetId)
              : [];
        const node = matches.length === 1 ? matches[0] : undefined;
        if (!node || own.has(node.id) || !bindingForConnectedNode(node)) return [];
        own.add(node.id);
        return [{ nodeId: node.id, role: "character" as const, priority: 100, source: "出镜角色" }];
    });
    return [
        ...characters,
        ...(script.metadata?.storyboard?.referenceNodeIds || []).flatMap((id) => {
            const node = nodes.find((n) => n.id === id),
                binding = node && bindingForConnectedNode(node);
            return !own.has(id) && binding ? [{ ...binding, source: "表级共享" }] : [];
        }),
    ];
}
export function reorderCanvasReferenceInputs(nodes: CanvasNodeData[], nodeId: string, orderedIds: string[], connections: CanvasConnection[] = []): ReferenceEditIntent | undefined {
    for (const board of nodes) {
        const row = board.metadata?.storyboard?.rows.find((row) => row.imageNodeId === nodeId || row.videoNodeId === nodeId);
        if (row) {
            const own = [...(row.assetBindings || [])];
            own.sort((a, b) => orderedIds.indexOf(a.nodeId) - orderedIds.indexOf(b.nodeId));
            return { kind: "set_row_assets", nodeId: board.id, rowId: row.id, assetBindings: own.map((binding, index) => ({ ...binding, priority: Math.max(0, 100 - index) })) };
        }
    }
    const node = nodes.find((n) => n.id === nodeId);
    if (!node) return;
    const bindings = editableInputBindings(node, nodes, connections);
    const order = new Map(orderedIds.map((id, index) => [id, index]));
    return { kind: "set_node_inputs", nodeId, bindings: [...bindings].sort((a, b) => (order.get(a.nodeId || "") ?? orderedIds.length + a.order) - (order.get(b.nodeId || "") ?? orderedIds.length + b.order)) };
}
export function assertStoryboardGenerationReady(script: CanvasNodeData, row: StoryboardRow, nodes: CanvasNodeData[], mode: "image" | "video"): void {
    if (mode !== "video" || script.metadata?.storyboardVideoInputMode !== "keyframe") return;
    const frame = nodes.find((n) => n.id === row.imageNodeId && n.type === "image");
    if (!frame || frame.metadata?.status === "loading" || frame.metadata?.status === "error" || (!frame.metadata?.storageKey && !frame.metadata?.content)) throw new Error("请先生成并检查该镜头的首帧");
}
export function withStoryboardOutputDraft(nodes: CanvasNodeData[], script: CanvasNodeData, row: StoryboardRow, outputId: string) {
    const output = nodes.find((n) => n.id === outputId);
    if (!output) return nodes;
    const firstFrame = output.type === "video" && script.metadata?.storyboardVideoInputMode === "keyframe" ? row.imageNodeId : undefined;
    const bindings = storyboardDeclaredNodeIds(script, row, nodes, Boolean(firstFrame))
        .flatMap((id) => {
            const source = nodes.find((n) => n.id === id),
                binding = source && bindingForNode(source, 0);
            return binding ? [{ ...binding, ...(id === firstFrame ? { id: `node:${id}:first-frame`, role: "first-frame" as const } : {}) }] : [];
        })
        .map((binding, order) => ({ ...binding, order }));
    const draft = synchronizeGenerationSpec(output, { composerContent: output.metadata?.composerContent || output.metadata?.prompt || "", ...(output.type === "video" ? { seconds: String(row.durationSeconds) } : {}) });
    const next = nodes.map((n) => (n.id === outputId ? { ...draft, metadata: { ...draft.metadata, videoStartFrameNodeId: firstFrame } } : n));
    return applyCanvasReferenceEdit(next, { kind: "set_node_inputs", nodeId: outputId, bindings });
}
