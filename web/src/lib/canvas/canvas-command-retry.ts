import type { Operation } from "./command-contract.generated";

/** Keep authored aliases and their first semantic observation. Projection
 * bases may already contain remote edits and cannot replace these baselines. */
export function mergeRejectedCanvasEdits(operations: Operation[]): Operation[] {
    const updates = new Map<string, { index: number; operation: Operation }>();
    for (const [index, input] of operations.entries()) {
        if (input.type !== "update_node") {
            if (["add_node", "delete_node", "duplicate_node"].includes(input.type) && updates.has(input.id)) throw new Error("节点结构已变化，请保留草稿并重新同步后处理");
            continue;
        }
        const previous = updates.get(input.id)?.operation;
        const next = structuredClone(input);
        if (previous) {
            next.patch = { ...previous.patch, ...next.patch };
            const before = previous.fieldBaselines,
                after = next.fieldBaselines;
            if (before && after) {
                if (before.nodeType !== after.nodeType || before.workflowKind !== after.workflowKind || before.generation !== after.generation) throw new Error("节点已发生变化，请保留草稿并重新同步后处理");
                next.fieldBaselines = { ...before, fields: { ...after.fields, ...before.fields } };
            } else delete next.fieldBaselines;
        }
        updates.set(input.id, { index, operation: next });
    }
    const mixed = operations.some((op) => op.type !== "update_node");
    return operations.flatMap((op, index) => {
        if (op.type !== "update_node") return [structuredClone(op)];
        const entry = updates.get(op.id)!;
        if (entry.index !== index) return [];
        if (mixed) delete entry.operation.fieldBaselines;
        return [entry.operation];
    });
}
