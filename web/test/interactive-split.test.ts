import { describe, expect, test } from "bun:test";
import { buildRepairPrompt, buildSplitPrompt } from "../src/lib/interactive/split-prompt";
import { parseSplitPayload } from "../src/lib/interactive/split-parse";
import { isImportable, validateSplitRows } from "../src/lib/interactive/split-validate";
import { collectCast } from "../src/lib/interactive/split-cast";
import { mapSplitToNodes } from "../src/pages/interactive/split-import";
import { emptySplitRow, type SplitRow } from "../src/lib/interactive/split-types";

const BODY = "船员看见岸边有人挥手，必须决定是否停船。海面起了雾，远处传来汽笛声。";

function row(rowKey: string, patch: Partial<SplitRow> = {}): SplitRow {
    return { ...emptySplitRow(rowKey), title: `${rowKey} 标题`, body: BODY, scenes: ["渡口"], characters: ["船员"], ...patch };
}

function validRows(): SplitRow[] {
    return [
        row("N01", {
            title: "末班渡船",
            characters: ["船员"],
            scenes: ["渡口"],
            choices: [
                { text: "靠岸", target: "N02" },
                { text: "继续航行", target: "N03" },
            ],
        }),
        row("N02", { title: "岸边重逢", characters: ["船员", "旧友"], scenes: ["码头"], next: "N04" }),
        row("N03", { title: "对岸来信", characters: ["船员"], scenes: ["值班室"], next: "N04" }),
        row("N04", { title: "渡口终局", ending: true }),
    ];
}

describe("拆分提示词", () => {
    test("内嵌固定表头、上限与合法 JSON 模板", () => {
        const prompt = buildSplitPrompt({ title: "末班渡船", requirements: "保留两个结局", script: "船员看见岸边有人挥手。" });
        expect(prompt).toContain("rowKey");
        expect(prompt).toContain("choices");
        expect(prompt).toContain("单表不超过 500 行");
        expect(prompt).toContain("只输出一个 JSON 对象");
        expect(prompt).toContain("船员看见岸边有人挥手。");
        expect(prompt).toContain("保留两个结局");
        const start = prompt.indexOf('{"title":"作品标题"');
        expect(start).toBeGreaterThan(-1);
        const template = prompt.slice(start, prompt.indexOf("\n", start));
        expect(() => JSON.parse(template)).not.toThrow();
    });

    test("改编要求为空时给默认值", () => {
        const prompt = buildSplitPrompt({ script: "第一幕" });
        expect(prompt).toContain("无特殊改编要求");
        expect(prompt).toContain("（未命名作品）");
    });

    test("修复提示词只列出错行", () => {
        const issues = validateSplitRows([row("N01", { body: "" })]);
        const prompt = buildRepairPrompt([row("N01", { body: "" })], issues);
        expect(prompt).toContain("N01");
        expect(prompt).toContain("缺少正文");
    });
});

describe("容错解析", () => {
    test("直接 JSON", () => {
        const payload = parseSplitPayload(JSON.stringify({ title: "甲", rows: [{ rowKey: "N01", title: "开场", body: "正文正文正文正文正文正文正文正文正文", scenes: ["渡口"], ending: true }] }));
        expect(payload?.title).toBe("甲");
        expect(payload?.rows[0].rowKey).toBe("N01");
    });

    test("代码围栏与前后说明", () => {
        const raw = '好的，结果如下：\n```json\n{"title":"乙","rows":[{"rowKey":"N01","body":"正文","scenes":["码头"],"ending":true}]}\n```\n以上。';
        const payload = parseSplitPayload(raw);
        expect(payload?.title).toBe("乙");
        expect(payload?.rows).toHaveLength(1);
    });

    test("兼容 #146 的 index/text/scene/next 结构", () => {
        const raw = JSON.stringify({
            title: "丙",
            rows: [
                { index: 1, text: "正文一", characters: ["船员"], scene: "渡口", next: [{ target: 2, option: "靠岸", suggested: false }], ending: false },
                { index: 2, text: "正文二", characters: [], scene: "码头", next: [], ending: true },
            ],
        });
        const payload = parseSplitPayload(raw);
        expect(payload?.rows[0].rowKey).toBe("N01");
        expect(payload?.rows[0].body).toBe("正文一");
        expect(payload?.rows[0].scenes).toEqual(["渡口"]);
        expect(payload?.rows[0].choices[0]).toEqual({ text: "靠岸", target: "N02", suggested: false });
        expect(payload?.rows[1].rowKey).toBe("N02");
    });

    test("根数组与字符串目标", () => {
        const raw = JSON.stringify([{ rowKey: "N01", body: "正文", scenes: "渡口，码头", next: "N02", ending: false }]);
        const payload = parseSplitPayload(raw);
        expect(payload?.rows[0].scenes).toEqual(["渡口", "码头"]);
        expect(payload?.rows[0].next).toBe("N02");
    });

    test("读不懂返回 null", () => {
        expect(parseSplitPayload("这里没有 JSON")).toBeNull();
        expect(parseSplitPayload(`{"title":"x"}`)).toBeNull();
    });
});

describe("行级校验", () => {
    test("合法双去向表无错误", () => {
        const issues = validateSplitRows(validRows());
        expect(issues.filter((issue) => issue.severity === "error")).toHaveLength(0);
        expect(isImportable(issues)).toBe(true);
    });

    test("缺正文定位到行", () => {
        const rows = validRows();
        rows[1] = { ...rows[1], body: "   " };
        const issues = validateSplitRows(rows);
        const missing = issues.find((issue) => issue.code === "missing_text");
        expect(missing?.rowKey).toBe("N02");
        expect(missing?.rowIndex).toBe(1);
    });

    test("行号重复与格式错误", () => {
        const rows = validRows();
        rows[2] = { ...rows[2], rowKey: "N01" };
        const codes = validateSplitRows(rows).map((issue) => issue.code);
        expect(codes).toContain("duplicate_index");
        expect(validateSplitRows([row("A1", { ending: true })]).map((issue) => issue.code)).toContain("bad_row_key");
    });

    test("目标不存在", () => {
        const rows = validRows();
        rows[0] = { ...rows[0], choices: [{ text: "靠岸", target: "N99" }] };
        const issue = validateSplitRows(rows).find((item) => item.code === "target_missing");
        expect(issue?.rowKey).toBe("N01");
        expect(issue?.message).toContain("N99");
    });

    test("结局约束与断头路", () => {
        const rows = validRows();
        rows[3] = { ...rows[3], ending: false, next: "" };
        rows[0] = { ...rows[0], choices: [], next: "" };
        const codes = validateSplitRows(rows).map((issue) => issue.code);
        expect(codes).toContain("dead_end");
        expect(codes).toContain("missing_ending");
    });

    test("空表与超行数整表拒绝", () => {
        expect(validateSplitRows([]).map((issue) => issue.code)).toEqual(["missing_rows"]);
        const many = Array.from({ length: 501 }, (_, index) => row(`N${String(index + 1).padStart(3, "0")}`, { ending: index === 500 }));
        const issues = validateSplitRows(many);
        expect(issues).toHaveLength(1);
        expect(issues[0].code).toBe("too_many_rows");
    });

    test("不可达行与无结局环", () => {
        const rows = [row("N01", { next: "N02" }), row("N02", { ending: true }), row("N03", { next: "N03" })];
        const codes = validateSplitRows(rows).map((issue) => issue.code);
        expect(codes).toContain("unreachable_row");
        expect(codes).toContain("cycle_without_ending");
    });

    test("选项与线性后继互斥", () => {
        const rows = validRows();
        rows[0] = { ...rows[0], next: "N04" };
        expect(validateSplitRows(rows).map((issue) => issue.code)).toContain("choices_and_next");
    });

    test("警告不阻断导入", () => {
        const rows = validRows();
        rows[1] = { ...rows[1], title: "", body: "短句", suggested: true };
        const issues = validateSplitRows(rows);
        expect(issues.some((issue) => issue.code === "missing_title" && issue.severity === "warning")).toBe(true);
        expect(issues.some((issue) => issue.code === "short_body")).toBe(true);
        expect(issues.some((issue) => issue.code === "suggested_row")).toBe(true);
        expect(isImportable(issues)).toBe(true);
    });
});

describe("导入映射", () => {
    test("行键映射节点、去向映射选项、线性后继只画线", () => {
        const rows = validRows();
        const mapped = mapSplitToNodes(rows);
        expect(mapped.nodes.map((node) => node.id)).toEqual(["E1", "E2", "E3", "E4"]);
        expect(mapped.nodeIdByRowKey).toEqual({ N01: "E1", N02: "E2", N03: "E3", N04: "E4" });
        expect(mapped.nodes[0].options.map((option) => option.to)).toEqual(["E2", "E3"]);
        expect(mapped.nodes[0].kind).toBe("branch");
        const linear = mapped.nodes[1].options.find((option) => option.linear);
        expect(linear?.to).toBe("E4");
        expect(mapped.nodes[3].isEnding).toBe(true);
        expect(mapped.nodes[1].body.startsWith("## ")).toBe(true);
        expect(mapped.issues).toHaveLength(0);
    });

    test("目标缺失时不静默丢弃", () => {
        const rows = validRows();
        rows[1] = { ...rows[1], next: "N77" };
        const mapped = mapSplitToNodes(rows);
        expect(mapped.issues.some((issue) => issue.code === "target_missing")).toBe(true);
    });
});

describe("角色场景清单", () => {
    test("按名称归一去重并保持首次出现顺序", () => {
        const rows = validRows();
        rows[2] = { ...rows[2], characters: ["船 员", "船员", "旧友"], scenes: ["值班室", "码 头"] };
        const cast = collectCast(rows);
        expect(cast.characters).toEqual(["船员", "旧友"]);
        expect(cast.scenes).toEqual(["渡口", "码头", "值班室"]);
    });
});
