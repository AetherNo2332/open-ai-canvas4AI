import { SPLIT_MAX_CHARACTERS, SPLIT_MAX_CHOICES, SPLIT_MAX_ROWS, type SplitIssue, type SplitRow } from "./split-types";

const JSON_ONLY = "只输出一个 JSON 对象，不要 Markdown 代码块，不要解释。";

export interface SplitPromptInput {
    title?: string;
    requirements?: string;
    script: string;
    maxRows?: number;
}

/**
 * 拆分提示词：固定表头 + 拆分规则 + 输出模板。
 * 只拆不改；原文已有分支逐字保留，新增分支一律 suggested=true。
 */
export function buildSplitPrompt({ title, requirements, script, maxRows = SPLIT_MAX_ROWS }: SplitPromptInput): string {
    const works = (title ?? "").trim() || "（未命名作品）";
    const asks = (requirements ?? "").trim() || "无特殊改编要求，按原文结构拆分";
    const lines = [
        `作品：${works}`,
        `改编要求：${asks}`,
        "",
        "【固定表头】一行一个可制作的剧情段落：",
        "- rowKey：行键，N01 起连续编号，全表唯一；N01 是入口。",
        "- title：标题，不超过 12 字，用于列表定位。",
        "- body：正文，本段落可拍摄的内容，保留原文台词与动作顺序；必须自包含。",
        "- characters：出镜角色名称数组，使用原文称呼，没有则空数组。",
        "- scenes：场景名称数组，使用原文称呼，不带内景/外景前缀。",
        '- choices：选项数组，每项 {"text":"选项文案","target":"目标行键","suggested":false}；没有选择时为空数组。',
        "- next：线性唯一后继的行键；有 choices 时留空字符串。",
        "- ending：布尔值，true 表示剧终；剧终行 choices 与 next 都必须为空。",
        "- suggested：布尔值，true 表示这是拆分新增、待编剧确认的段落。",
        "- note：拆分批注或原文注释，不进画面，没有则空字符串。",
        "",
        "【拆分规则】",
        "1. 场景、出场角色或拍摄意图发生变化时另起一行。",
        "2. 原文已有的分支必须保留：target 指向原文对应段落，选项文案逐字保留，suggested=false。",
        "3. 原文是单线故事时，可在自然停顿处新增分支：新增段落或新增选项的 suggested 标为 true，并写好选项文案；不得改动原文段落的内容与顺序。",
        "4. 全剧至少一个结局；非结局段落必须有 choices 或 next，不能断头。",
        "5. choices 与 next 互斥；choices 里每个 target 必须是本表已存在的行键。",
        "6. 分支汇合时多个分支指向同一行，不复制节点。",
        `7. 单表不超过 ${maxRows} 行；单行角色不超过 ${SPLIT_MAX_CHARACTERS} 个、选项不超过 ${SPLIT_MAX_CHOICES} 个。`,
        "8. body 不得改写、缩写或编造原文台词；不在 body 里写「待定」「同上」。",
        "",
        "输出（顶层 title 为作品标题）：",
        `{"title":"作品标题","rows":[{"rowKey":"N01","title":"段落标题","body":"正文","characters":["角色名"],"scenes":["场景名"],"choices":[{"text":"选项文案","target":"N02","suggested":false}],"next":"","ending":false,"suggested":false,"note":""}]}`,
        "",
        JSON_ONLY,
        "",
        "【剧本原文】",
        script,
    ];
    return lines.join("\n");
}

/** 修复提示词：只带出错的行与问题，要求模型返回完整表。 */
export function buildRepairPrompt(rows: SplitRow[], issues: SplitIssue[]): string {
    const errors = issues.filter((issue) => issue.severity === "error");
    const byRow = new Map<string, string[]>();
    for (const issue of errors) {
        const key = issue.rowKey || "(全表)";
        const bucket = byRow.get(key) ?? [];
        bucket.push(issue.message);
        byRow.set(key, bucket);
    }
    const problemLines = Array.from(byRow, ([key, messages]) => `- ${key}：${messages.join("；")}`);
    return ["上一版拆分表没有通过校验。请只修正下列问题，其余行保持原样，返回完整表格。", "【问题】", ...problemLines, "", "【上一版 JSON】", JSON.stringify({ title: "", rows }, null, 2), "", JSON_ONLY].join("\n");
}
