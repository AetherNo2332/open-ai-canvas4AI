import type { CanvasBatchTableData } from "@/types/canvas";

function sameIds(before: string[] | undefined, after: string[] | undefined) {
    return (before?.length || 0) === (after?.length || 0) && (before || []).every((id, index) => id === after?.[index]);
}

/** Preserve completed results while marking changes to the inputs they were generated from. */
export function patchCanvasBatchTableData(previous: CanvasBatchTableData | undefined, patch: Partial<CanvasBatchTableData>): CanvasBatchTableData {
    const previousRows = new Map(previous?.rows.map((row) => [row.id, row]));
    const table = { operation: "try_on" as const, concurrency: 10, rows: [], ...previous, ...patch };
    const rows = table.rows.map((row) => {
        const old = previousRows.get(row.id);
        const changed = old?.prompt !== row.prompt || previous?.globalPrompt !== table.globalPrompt || previous?.operation !== table.operation || !sameIds(old?.inputNodeIds, row.inputNodeIds) || !sameIds(old?.textNodeIds, row.textNodeIds);
        return row.outputNodeId && changed ? { ...row, inputChanged: true } : row;
    });
    return { ...table, rows };
}
