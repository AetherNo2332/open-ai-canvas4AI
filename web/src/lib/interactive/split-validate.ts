import { compareRowKeys, isRowKey, SPLIT_MAX_CHARACTERS, SPLIT_MAX_CHOICES, SPLIT_MAX_ROWS, SPLIT_MIN_BODY_LENGTH, rowKeyFromIndex, type SplitIssue, type SplitRow } from "./split-types";

function issue(code: string, rowIndex: number, rowKey: string, field: string, message: string, severity: "error" | "warning" = "error"): SplitIssue {
    return { code, rowKey, rowIndex, field, severity, message };
}

function targetsOf(row: SplitRow): string[] {
    const targets = row.choices.map((choice) => choice.target);
    if (row.next) targets.push(row.next);
    return targets;
}

/** 行级校验：错误阻断导入，警告只提示。问题一律定位到行。 */
export function validateSplitRows(rows: SplitRow[]): SplitIssue[] {
    if (rows.length === 0) return [issue("missing_rows", -1, "", "rows", "拆分结果没有任何剧情行")];
    if (rows.length > SPLIT_MAX_ROWS) {
        return [issue("too_many_rows", -1, "", "rows", `表格共 ${rows.length} 行，超过单表上限 ${SPLIT_MAX_ROWS} 行，请分段拆分`)];
    }
    const issues: SplitIssue[] = [];
    const seen = new Map<string, number>();
    rows.forEach((row, index) => {
        if (!isRowKey(row.rowKey)) {
            issues.push(issue("bad_row_key", index, row.rowKey, "rowKey", `第 ${index + 1} 行行键「${row.rowKey || "空"}」不是 N01 格式`));
        }
        const previous = seen.get(row.rowKey);
        if (previous !== undefined) {
            issues.push(issue("duplicate_index", index, row.rowKey, "rowKey", `行键 ${row.rowKey} 与第 ${previous + 1} 行重复`));
        } else {
            seen.set(row.rowKey, index);
        }
        if (!row.title.trim()) issues.push(issue("missing_title", index, row.rowKey, "title", `第 ${row.rowKey} 行缺少标题`, "warning"));
        if (!row.body.trim()) issues.push(issue("missing_text", index, row.rowKey, "body", `第 ${row.rowKey} 行缺少正文`));
        else if (row.body.trim().length < SPLIT_MIN_BODY_LENGTH) issues.push(issue("short_body", index, row.rowKey, "body", `第 ${row.rowKey} 行正文过短，可能无法单独制作`, "warning"));
        if (row.scenes.length === 0) issues.push(issue("missing_scene", index, row.rowKey, "scenes", `第 ${row.rowKey} 行缺少场景`));
        if (row.characters.length > SPLIT_MAX_CHARACTERS) issues.push(issue("too_many_characters", index, row.rowKey, "characters", `第 ${row.rowKey} 行角色 ${row.characters.length} 个，超过上限 ${SPLIT_MAX_CHARACTERS} 个`));
        if (row.choices.length > SPLIT_MAX_CHOICES) issues.push(issue("too_many_choices", index, row.rowKey, "choices", `第 ${row.rowKey} 行选项 ${row.choices.length} 个，超过上限 ${SPLIT_MAX_CHOICES} 个`));
        if (row.suggested) issues.push(issue("suggested_row", index, row.rowKey, "suggested", `第 ${row.rowKey} 行是 AI 新增建议，导入前请确认`, "warning"));
    });

    rows.forEach((row, index) => {
        if (row.ending) {
            if (targetsOf(row).length > 0) issues.push(issue("ending_has_next", index, row.rowKey, "ending", `第 ${row.rowKey} 行已标记结局，不能再有去向`));
            return;
        }
        if (row.choices.length === 0 && !row.next) {
            issues.push(issue("dead_end", index, row.rowKey, "choices", `第 ${row.rowKey} 行既不是结局也没有去向`));
            return;
        }
        if (row.choices.length > 0 && row.next) issues.push(issue("choices_and_next", index, row.rowKey, "next", `第 ${row.rowKey} 行同时写了选项和线性后继，只能留一个`));
        row.choices.forEach((choice, choiceIndex) => {
            if (!choice.text.trim()) issues.push(issue("missing_option", index, row.rowKey, `choices[${choiceIndex}].text`, `第 ${row.rowKey} 行第 ${choiceIndex + 1} 个选项缺少文案`));
        });
        const labels = new Set<string>();
        for (const choice of row.choices) {
            if (!choice.text.trim()) continue;
            if (labels.has(choice.text)) issues.push(issue("duplicate_choice", index, row.rowKey, "choices", `第 ${row.rowKey} 行有两个相同文案的选项：${choice.text}`, "warning"));
            labels.add(choice.text);
        }
    });

    const known = new Set(rows.map((row) => row.rowKey));
    rows.forEach((row, index) => {
        for (const target of targetsOf(row)) {
            if (!known.has(target)) issues.push(issue("target_missing", index, row.rowKey, "choices.target", `第 ${row.rowKey} 行的去向 ${target || "(空)"} 不在本表`));
        }
    });

    if (!rows.some((row) => row.ending)) issues.push(issue("missing_ending", -1, "", "ending", "全表没有结局（ending=true）"));

    issues.push(...validateReachability(rows, known));
    return issues;
}

/** 可达性与环：从入口出发到不了的行、绕不出去的环都要报出来。 */
function validateReachability(rows: SplitRow[], known: Set<string>): SplitIssue[] {
    const issues: SplitIssue[] = [];
    const indexByKey = new Map(rows.map((row, index) => [row.rowKey, index]));
    const entry = rows[0]?.rowKey;
    if (!entry) return issues;
    const adjacency = new Map<string, string[]>();
    for (const row of rows)
        adjacency.set(
            row.rowKey,
            targetsOf(row).filter((target) => known.has(target)),
        );
    const reached = new Set<string>([entry]);
    const queue = [entry];
    while (queue.length) {
        const current = queue.shift() as string;
        for (const next of adjacency.get(current) ?? []) {
            if (reached.has(next)) continue;
            reached.add(next);
            queue.push(next);
        }
    }
    rows.forEach((row, index) => {
        if (!reached.has(row.rowKey)) issues.push(issue("unreachable_row", index, row.rowKey, "rowKey", `第 ${row.rowKey} 行从入口 ${entry} 不可达`));
    });

    const cycleNodes = findCycleNodes(adjacency);
    const endingKeys = new Set(rows.filter((row) => row.ending).map((row) => row.rowKey));
    for (const key of cycleNodes) {
        if (reachesAny(adjacency, key, endingKeys)) continue;
        const index = indexByKey.get(key) ?? -1;
        issues.push(issue("cycle_without_ending", index, key, "choices", `第 ${key} 行在环上，且走不到任何结局`));
        break;
    }
    return issues;
}

function findCycleNodes(adjacency: Map<string, string[]>): string[] {
    const state = new Map<string, number>();
    const stack: string[] = [];
    const onCycle = new Set<string>();
    const visit = (node: string) => {
        state.set(node, 1);
        stack.push(node);
        for (const next of adjacency.get(node) ?? []) {
            const status = state.get(next) ?? 0;
            if (status === 0) visit(next);
            else if (status === 1) {
                const from = stack.lastIndexOf(next);
                for (const item of stack.slice(from)) onCycle.add(item);
            }
        }
        stack.pop();
        state.set(node, 2);
    };
    for (const node of adjacency.keys()) if ((state.get(node) ?? 0) === 0) visit(node);
    return Array.from(onCycle);
}

function reachesAny(adjacency: Map<string, string[]>, from: string, targets: Set<string>): boolean {
    const seen = new Set<string>([from]);
    const queue = [from];
    while (queue.length) {
        const current = queue.shift() as string;
        if (targets.has(current)) return true;
        for (const next of adjacency.get(current) ?? []) {
            if (seen.has(next)) continue;
            seen.add(next);
            queue.push(next);
        }
    }
    return false;
}

/** 只有 error 阻断导入；warning 允许带病导入，由编剧确认。 */
export function isImportable(issues: SplitIssue[]): boolean {
    return !issues.some((issue) => issue.severity === "error");
}

export function issuesByRow(issues: SplitIssue[]): Map<string, SplitIssue[]> {
    const map = new Map<string, SplitIssue[]>();
    for (const issue of issues) {
        const key = issue.rowKey;
        const bucket = map.get(key) ?? [];
        bucket.push(issue);
        map.set(key, bucket);
    }
    return map;
}

/** 行键排序（仅用于展示排序，不改数组顺序）。 */
export function sortedRowKeys(rows: SplitRow[]): string[] {
    return rows.map((row) => row.rowKey).sort(compareRowKeys);
}

/** 入口行：第一条；没有合法行键时用 N01。 */
export function entryRowKey(rows: SplitRow[]): string {
    return rows[0]?.rowKey && isRowKey(rows[0].rowKey) ? rows[0].rowKey : rowKeyFromIndex(1);
}
