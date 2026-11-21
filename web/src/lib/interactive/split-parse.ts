import { emptySplitRow, rowKeyFromIndex, type SplitChoice, type SplitPayload, type SplitRow } from "./split-types";

function asText(value: unknown): string {
    if (typeof value === "string") return value.trim();
    if (typeof value === "number" || typeof value === "boolean") return String(value);
    return "";
}

function asTextList(value: unknown): string[] {
    if (typeof value === "string") {
        return value
            .split(/[,，、;；]/)
            .map((item) => item.trim())
            .filter(Boolean);
    }
    if (!Array.isArray(value)) return [];
    return value.map(asText).filter(Boolean);
}

/** 目标写成数字、数字字符串或 N02 都接受，统一成行键。 */
function asRowKey(value: unknown): string {
    if (typeof value === "number" && Number.isFinite(value)) return rowKeyFromIndex(value);
    const text = asText(value);
    if (/^N\d{2,}$/.test(text)) return text;
    if (/^\d+$/.test(text)) return rowKeyFromIndex(Number.parseInt(text, 10));
    return text;
}

function asChoice(raw: unknown): SplitChoice | null {
    if (!raw || typeof raw !== "object") return null;
    const record = raw as Record<string, unknown>;
    const text = asText(record.text) || asText(record.label) || asText(record.option);
    const target = asRowKey(record.target ?? record.to);
    if (!text && !target) return null;
    return { text, target, suggested: record.suggested === true };
}

function readChoices(record: Record<string, unknown>): SplitChoice[] {
    const source = Array.isArray(record.choices) ? record.choices : Array.isArray(record.next) ? record.next : [];
    return source.map(asChoice).filter((choice): choice is SplitChoice => Boolean(choice));
}

/** 单行归一：兼容 #146 的 index/text/scene/next[] 与本表的 rowKey/body/scenes/choices。 */
export function normalizeSplitRow(raw: unknown, index: number): SplitRow {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return emptySplitRow(rowKeyFromIndex(index + 1));
    const record = raw as Record<string, unknown>;
    const row = emptySplitRow(asRowKey(record.rowKey ?? record.row_key ?? record.key) || rowKeyFromIndex(record.index != null ? Number(record.index) : index + 1));
    row.title = asText(record.title);
    row.body = asText(record.body) || asText(record.text) || asText(record.content);
    row.characters = asTextList(record.characters);
    row.scenes = asTextList(record.scenes ?? record.scene);
    row.choices = readChoices(record);
    row.next = typeof record.next === "string" ? asRowKey(record.next) : "";
    row.ending = record.ending === true;
    row.suggested = record.suggested === true;
    row.note = asText(record.note);
    return row;
}

/** 从模型输出里抠出所有平衡的 JSON 对象，供逐候选解析。 */
export function balancedJsonObjects(text: string): string[] {
    const results: string[] = [];
    let start = -1;
    let depth = 0;
    let inString = false;
    let escaped = false;
    for (let index = 0; index < text.length; index += 1) {
        const char = text[index];
        if (inString) {
            if (escaped) escaped = false;
            else if (char === "\\") escaped = true;
            else if (char === '"') inString = false;
            continue;
        }
        if (char === '"') {
            inString = true;
            continue;
        }
        if (char === "{") {
            if (depth === 0) start = index;
            depth += 1;
            continue;
        }
        if (char !== "}") continue;
        if (depth === 0) continue;
        depth -= 1;
        if (depth === 0 && start >= 0) {
            results.push(text.slice(start, index + 1));
            start = -1;
        }
    }
    return results;
}

function fencedBlocks(text: string): string[] {
    const blocks: string[] = [];
    const pattern = /```(?:json)?\s*([\s\S]*?)```/gi;
    for (const match of text.matchAll(pattern)) {
        const block = match[1]?.trim();
        if (block) blocks.push(block);
    }
    return blocks;
}

/**
 * 容错解析模型输出：容忍代码围栏、前后说明文字与多个候选对象。
 * 结构读不懂返回 null；能读懂但字段有问题时留给 validateSplitRows 逐行报错。
 */
export function parseSplitPayload(raw: string): SplitPayload | null {
    const trimmed = raw.trim();
    if (!trimmed) return null;
    const candidates = [trimmed, ...fencedBlocks(trimmed), ...balancedJsonObjects(trimmed)];
    for (const candidate of candidates) {
        const payload = parseCandidate(candidate);
        if (payload) return payload;
    }
    return null;
}

function parseCandidate(candidate: string): SplitPayload | null {
    let parsed: unknown;
    try {
        parsed = JSON.parse(candidate);
    } catch {
        return null;
    }
    const record = Array.isArray(parsed) ? { rows: parsed } : parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : null;
    if (!record) return null;
    const rawRows = Array.isArray(record.rows) ? record.rows : Array.isArray(record.scenes) ? record.scenes : null;
    if (!rawRows || rawRows.length === 0) return null;
    const rows = rawRows.map((row, index) => normalizeSplitRow(row, index));
    const title = asText(record.title);
    return { title, rows };
}
