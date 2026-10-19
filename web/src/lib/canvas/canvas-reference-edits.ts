import type { Operation } from "./command-contract.generated";
import type { CanvasDocument } from "./canvas-event-projection";
import type { ReferenceBinding } from "./generation-contract.generated";
import type { CanvasNodeData, CanvasConnection, StoryboardAssetBinding, StoryboardRow } from "@/types/canvas";
import { bindingForNode, readNodeGenerationSpec, validateGenerationSpec, synchronizeGenerationSpec } from "./generation-contract";
import { storyboardDeclaredNodeIds } from "./canvas-reference-inputs";
import { bindingForConnectedNode } from "./canvas-storyboard-materializer";
export type ReferenceEditIntent =
    | { kind: "set_row_assets"; nodeId: string; rowId: string; assetBindings: StoryboardAssetBinding[] }
    | { kind: "set_board_references"; nodeId: string; referenceNodeIds: string[] }
    | { kind: "set_node_inputs"; nodeId: string; bindings: ReferenceBinding[] }
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
export function applyCanvasReferenceEdit(nodes: CanvasNodeData[], intent: ReferenceEditIntent): CanvasNodeData[] {
    const [op] = buildCanvasReferenceEdit({ nodes }, intent);
    return nodes.map((node) => {
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
}
export function canvasReferenceIntentForConnection(nodes: CanvasNodeData[], edge: Pick<CanvasConnection, "fromNodeId" | "toNodeId" | "fromHandleId" | "toHandleId">, remove = false): ReferenceEditIntent | undefined {
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
            return canvasReferenceIntentForConnection(nodes, { fromNodeId: source.id, toNodeId: board.id, toHandleId: `row:${row.id}` }, remove);
        }
    }
    const spec = nodeSpec(target);
    if (!spec && !inputTypes.has(target.type)) return undefined;
    const bindings = spec?.referenceBindings || target.metadata?.inputBindings || [];
    const binding = bindingForNode(source, bindings.length);
    if (!binding) return undefined;
    return { kind: "set_node_inputs", nodeId: target.id, bindings: remove ? bindings.filter((b) => b.nodeId !== source.id) : bindings.some((b) => b.nodeId === source.id) ? bindings : [...bindings, binding] };
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
    return [...characters, ...(script.metadata?.storyboard?.referenceNodeIds || []).flatMap((id) => {
        const node = nodes.find((n) => n.id === id),
            binding = node && bindingForConnectedNode(node);
        return !own.has(id) && binding ? [{ ...binding, source: "表级共享" }] : [];
    })];
}
export function reorderCanvasReferenceInputs(nodes: CanvasNodeData[], nodeId: string, orderedIds: string[]): ReferenceEditIntent | undefined {
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
    const bindings = nodeSpec(node)?.referenceBindings ?? node.metadata?.inputBindings;
    if (bindings === undefined) return;
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
