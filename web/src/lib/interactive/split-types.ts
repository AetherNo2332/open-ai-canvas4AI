/** 互动剧拆分表的行级契约：一行 = 一个可制作的剧情段落。 */
export interface SplitChoice {
    /** 选项文案；原文已有的分支逐字保留。 */
    text: string;
    /** 目标行键，必须是本表已存在的 rowKey。 */
    target: string;
    /** true 表示这是 AI 新增、待编剧确认的建议分支。 */
    suggested?: boolean;
}

export interface SplitRow {
    /** 行键，N01 起；一次导入内的稳定引用，不是持久 ID。 */
    rowKey: string;
    /** 列表与卡片定位用，≤12 字。 */
    title: string;
    /** 自包含正文：单独读这一行能说清谁、在哪、发生了什么。 */
    body: string;
    /** 出镜角色名称，不是 assetId。 */
    characters: string[];
    /** 场景名称，不带"内景/外景"前缀。 */
    scenes: string[];
    /** 真选项；与 next 互斥。 */
    choices: SplitChoice[];
    /** 线性唯一后继；有 choices 时为空。 */
    next: string;
    /** 是否剧终。 */
    ending: boolean;
    /** 是否 AI 新增的建议段落。 */
    suggested: boolean;
    /** 拆分批注，不进画面。 */
    note: string;
}

export type SplitIssueSeverity = "error" | "warning";

export interface SplitIssue {
    code: string;
    /** 定位用行键；全局问题为空串。 */
    rowKey: string;
    /** 数组下标；-1 表示全局问题。 */
    rowIndex: number;
    field: string;
    severity: SplitIssueSeverity;
    message: string;
}

export interface SplitCast {
    characters: string[];
    scenes: string[];
}

export interface SplitPayload {
    title: string;
    rows: SplitRow[];
}

export const SPLIT_MAX_ROWS = 500;
export const SPLIT_MAX_CHARACTERS = 50;
export const SPLIT_MAX_CHOICES = 20;
export const SPLIT_MIN_BODY_LENGTH = 20;

const ROW_KEY_PATTERN = /^N\d{2,}$/;

export function isRowKey(value: string): boolean {
    return ROW_KEY_PATTERN.test(value);
}

/** 下标（1 起）转行键：1 -> N01。 */
export function rowKeyFromIndex(index: number): string {
    const safe = Number.isFinite(index) && index > 0 ? Math.floor(index) : 1;
    return `N${String(safe).padStart(2, "0")}`;
}

export function emptySplitRow(rowKey: string): SplitRow {
    return { rowKey, title: "", body: "", characters: [], scenes: [], choices: [], next: "", ending: false, suggested: false, note: "" };
}

/** 与后端 production.NormalizeName 同口径：小写、去空白与标点后比较名称。 */
export function normalizeSplitName(value: string): string {
    return value.toLowerCase().replace(/[\s\p{P}]/gu, "");
}

/** 行键排序比较：N02 在 N10 前面。 */
export function compareRowKeys(a: string, b: string): number {
    const na = Number.parseInt(a.replace(/^N/, ""), 10) || 0;
    const nb = Number.parseInt(b.replace(/^N/, ""), 10) || 0;
    return na - nb || a.localeCompare(b);
}
