import type { StoryNode, StoryOption } from "./story-data";
import type { SplitIssue, SplitRow } from "@/lib/interactive/split-types";

export interface SplitImportResult {
    nodes: StoryNode[];
    nodeIdByRowKey: Record<string, string>;
    issues: SplitIssue[];
}

const NODE_WIDTH = 288;
const COLUMN_GAP = 420;
const ROW_GAP = 260;

/** 行键在表内的深度（入口为 0），用于简单分层排布；环上节点回落到 0。 */
function layoutDepth(rows: SplitRow[], indexByKey: Map<string, number>): number[] {
    const depth = rows.map(() => -1);
    if (rows.length === 0) return depth;
    depth[0] = 0;
    const queue = [0];
    while (queue.length) {
        const current = queue.shift() as number;
        const row = rows[current];
        const targets = [...row.choices.map((choice) => choice.target), row.next].filter(Boolean);
        for (const target of targets) {
            const nextIndex = indexByKey.get(target);
            if (nextIndex === undefined || depth[nextIndex] >= 0) continue;
            depth[nextIndex] = depth[current] + 1;
            queue.push(nextIndex);
        }
    }
    return depth.map((value) => (value < 0 ? 0 : value));
}

/**
 * 拆分表 → 剧情节点。
 * 行键只在本表内有效，节点 ID 由行顺序生成 E1..En；映射回填到草稿，供重复导入对齐。
 */
export function mapSplitToNodes(rows: SplitRow[]): SplitImportResult {
    const issues: SplitIssue[] = [];
    const nodeIdByRowKey: Record<string, string> = {};
    const nodeIdByIndex: string[] = rows.map((row, index) => `E${index + 1}`);
    rows.forEach((row, index) => {
        nodeIdByRowKey[row.rowKey] = nodeIdByIndex[index];
    });
    const indexByKey = new Map(rows.map((row, index) => [row.rowKey, index]));
    const depth = layoutDepth(rows, indexByKey);
    const perDepth = new Map<number, number>();

    const nodes: StoryNode[] = rows.map((row, index) => {
        const column = depth[index];
        const offset = perDepth.get(column) ?? 0;
        perDepth.set(column, offset + 1);
        const options: StoryOption[] = [];
        row.choices.forEach((choice, choiceIndex) => {
            const to = nodeIdByRowKey[choice.target];
            if (!to) {
                issues.push({ code: "target_missing", rowKey: row.rowKey, rowIndex: index, field: "choices.target", severity: "error", message: `第 ${row.rowKey} 行的去向 ${choice.target || "(空)"} 不在本表` });
                return;
            }
            options.push({ id: `${nodeIdByIndex[index]}-${choiceIndex + 1}`, label: choice.text, to, ...(choice.suggested ? { suggested: true } : {}) });
        });
        if (row.next) {
            const to = nodeIdByRowKey[row.next];
            if (to) options.push({ id: `${nodeIdByIndex[index]}-next`, label: "继续", to, linear: true });
            else issues.push({ code: "target_missing", rowKey: row.rowKey, rowIndex: index, field: "next", severity: "error", message: `第 ${row.rowKey} 行的后继 ${row.next} 不在本表` });
        }
        return {
            id: nodeIdByIndex[index],
            kind: options.filter((option) => !option.linear).length > 1 ? "branch" : "story",
            title: row.title.trim() || `第 ${index + 1} 段`,
            status: row.suggested ? "待续写" : "已完成",
            body: withSceneHeading(row.body, row.scenes[0] ?? row.title),
            options,
            isEnding: row.ending || undefined,
            x: column * COLUMN_GAP,
            y: offset * ROW_GAP,
            rowKey: row.rowKey,
            characters: row.characters,
            scenes: row.scenes,
            suggested: row.suggested || undefined,
        };
    });
    return { nodes, nodeIdByRowKey, issues };
}

/** StoryBody 用 "## " 行渲染幕标题；正文自带的标题不重复加。 */
function withSceneHeading(body: string, heading: string): string {
    const trimmed = body.trim();
    if (!trimmed) return trimmed;
    if (trimmed.startsWith("## ")) return trimmed;
    const label = heading.trim() || "剧情";
    return `## ${label}\n${trimmed}`;
}

/** 卡片宽度常量导出给页面做居中计算。 */
export const SPLIT_NODE_WIDTH = NODE_WIDTH;
