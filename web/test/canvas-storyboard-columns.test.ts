import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, test } from "bun:test";

import { applyCanvasOperations, verifyCanvasOperations, type CanvasOperation, type CanvasSnapshot } from "@/lib/canvas/canvas-operation-contract";
import { createCanvasNode, createStoryboardRow } from "@/lib/canvas/canvas-project-domain";
import { DEFAULT_STORYBOARD_COLUMNS, resolveStoryboardVisibleColumns, STORYBOARD_COLUMN_OPTIONS, storyboardColumnLabel } from "@/lib/canvas/canvas-storyboard-columns";
import { CanvasNodeType, type CanvasNodeData, type StoryboardColumn, type StoryboardRow } from "@/types/canvas";

// 用字面量固定期望值，避免测试直接拿共享常量当答案而失去回归意义。
const EXPECTED_DEFAULT_COLUMNS: StoryboardColumn[] = ["shotNumber", "durationSeconds", "plotDescription", "shotSize", "motion", "assets"];
const EXPECTED_DEFAULT_LABELS = ["序号", "时长", "画面描述", "景别", "运镜", "关联资产"];

const HISTORICAL_DEFAULT_COLUMN_SETS: Array<{ name: string; columns: StoryboardColumn[] }> = [
    { name: "旧五列默认", columns: ["shotNumber", "durationSeconds", "videoMotionPrompt", "dialogue", "assets"] },
    { name: "早期四列默认", columns: ["shotNumber", "durationSeconds", "plotDescription", "dialogue"] },
    { name: "上一版四列默认", columns: ["shotNumber", "plotDescription", "videoMotionPrompt", "dialogue"] },
];

// 生成流程曾自动补出的“电影感”十五列同样不是用户手选，读取时应迁移到当前默认。
const FIFTEEN_COLUMN_GENERATED_DEFAULT: StoryboardColumn[] = [
    "shotNumber",
    "durationSeconds",
    "videoMotionPrompt",
    "dialogue",
    "assets",
    "shotSize",
    "narrativeIntent",
    "viewerPOV",
    "performanceBlocking",
    "camera",
    "motion",
    "timeBeats",
    "lightingAndAtmosphere",
    "continuityOut",
    "negativePrompt",
];

function scriptNode(rows: StoryboardRow[], visibleColumns: StoryboardColumn[] = [...EXPECTED_DEFAULT_COLUMNS]): CanvasNodeData {
    return {
        id: "script-1",
        type: CanvasNodeType.Script,
        title: "分镜脚本",
        position: { x: 0, y: 0 },
        width: 920,
        height: 360,
        metadata: { status: "idle", workflowKind: "script", storyboard: { rows, visibleColumns, referenceNodeIds: ["ref-1"] } },
    };
}

function snapshot(nodes: CanvasNodeData[]): CanvasSnapshot {
    return { projectId: "project-1", title: "画布", nodes, connections: [], selectedNodeIds: [], viewport: { x: 0, y: 0, k: 1 } };
}

describe("分镜脚本默认展示列", () => {
    test("默认六列：序号、时长、画面描述、景别、运镜、关联资产", () => {
        expect([...DEFAULT_STORYBOARD_COLUMNS]).toEqual(EXPECTED_DEFAULT_COLUMNS);
        expect(EXPECTED_DEFAULT_COLUMNS.map(storyboardColumnLabel)).toEqual(EXPECTED_DEFAULT_LABELS);
        for (const column of EXPECTED_DEFAULT_COLUMNS) {
            expect(STORYBOARD_COLUMN_OPTIONS.some((option) => option.value === column)).toBe(true);
        }
    });

    test("缺省、null 与空数组回退到当前默认，且不共享常量数组", () => {
        for (const columns of [undefined, null, [] as StoryboardColumn[]]) {
            const resolved = resolveStoryboardVisibleColumns(columns);
            expect(resolved).toEqual(EXPECTED_DEFAULT_COLUMNS);
            expect(resolved).not.toBe(DEFAULT_STORYBOARD_COLUMNS);
        }
        const resolved = resolveStoryboardVisibleColumns();
        resolved.push("dialogue");
        expect([...DEFAULT_STORYBOARD_COLUMNS]).toEqual(EXPECTED_DEFAULT_COLUMNS);
    });

    test("历史默认集合迁移到当前默认", () => {
        for (const legacy of HISTORICAL_DEFAULT_COLUMN_SETS) {
            const resolved = resolveStoryboardVisibleColumns(legacy.columns);
            expect(resolved).toEqual(EXPECTED_DEFAULT_COLUMNS);
            expect(resolved).not.toBe(DEFAULT_STORYBOARD_COLUMNS);
            expect(resolved).not.toBe(legacy.columns);
        }
    });

    test("旧版十五列电影感默认迁移到当前默认", () => {
        expect(resolveStoryboardVisibleColumns(FIFTEEN_COLUMN_GENERATED_DEFAULT)).toEqual(EXPECTED_DEFAULT_COLUMNS);
        expect(resolveStoryboardVisibleColumns([...FIFTEEN_COLUMN_GENERATED_DEFAULT].reverse())).toEqual(EXPECTED_DEFAULT_COLUMNS);
    });

    test("用户手选列原样保留，返回新数组且顺序不变", () => {
        const custom: StoryboardColumn[] = ["assets", "shotNumber", "imageGenerationPrompt", "dialogue"];
        const resolved = resolveStoryboardVisibleColumns(custom);
        expect(resolved).toEqual(custom);
        expect(resolved).not.toBe(custom);

        // 顺序与默认不同即视为用户选择，不能因为集合相同被迁移。
        const reordered = [...EXPECTED_DEFAULT_COLUMNS].reverse();
        expect(resolveStoryboardVisibleColumns(reordered)).toEqual(reordered);

        // 默认列的子集同样代表用户主动隐藏了列。
        expect(resolveStoryboardVisibleColumns(["shotNumber", "assets"])).toEqual(["shotNumber", "assets"]);
    });

    test("createCanvasNode(分镜脚本) 使用共享模块的默认列", () => {
        const node = createCanvasNode(CanvasNodeType.Script, { x: 0, y: 0 });
        expect(node.metadata?.storyboard?.visibleColumns).toEqual(EXPECTED_DEFAULT_COLUMNS);
        expect(node.metadata?.storyboard?.rows).toHaveLength(3);
        node.metadata!.storyboard!.visibleColumns.push("dialogue");
        expect([...DEFAULT_STORYBOARD_COLUMNS]).toEqual(EXPECTED_DEFAULT_COLUMNS);
    });
});

describe("Agent update_node 的分镜 metadata 合并", () => {
    test("只提交 visibleColumns 时保留未提交的 rows 与 referenceNodeIds", () => {
        const rows = [createStoryboardRow(1), createStoryboardRow(2)];
        const before = snapshot([scriptNode(rows)]);
        const op = {
            type: "update_node",
            id: "script-1",
            metadata: { storyboard: { visibleColumns: ["shotNumber", "durationSeconds"] } },
        } as unknown as CanvasOperation;

        const after = applyCanvasOperations(before, [op]);
        const metadata = after.nodes[0].metadata;
        expect(metadata?.storyboard?.rows).toEqual(rows);
        expect(metadata?.storyboard?.referenceNodeIds).toEqual(["ref-1"]);
        expect(metadata?.storyboard?.visibleColumns).toEqual(["shotNumber", "durationSeconds"]);
        expect(metadata?.status).toBe("idle");
        expect(verifyCanvasOperations(before, after, [op]).ok).toBe(true);
        // 原画布快照不能被就地修改。
        expect(before.nodes[0].metadata?.storyboard?.visibleColumns).toEqual(EXPECTED_DEFAULT_COLUMNS);
    });

    test("同时修改 patch 与 metadata 时，事实复核按合并后的 storyboard 期望值比较", () => {
        const rows = [createStoryboardRow(1)];
        const before = snapshot([scriptNode(rows)]);
        const op = {
            type: "update_node",
            id: "script-1",
            patch: { title: "新分镜标题" },
            metadata: { storyboard: { visibleColumns: ["shotNumber"] } },
        } as unknown as CanvasOperation;

        const after = applyCanvasOperations(before, [op]);
        expect(after.nodes[0].title).toBe("新分镜标题");
        expect(after.nodes[0].metadata?.storyboard?.rows).toEqual(rows);
        const result = verifyCanvasOperations(before, after, [op]);
        expect(result.ok).toBe(true);
        expect(result.warnings).toEqual([]);
    });
});

// 后端 capability/storyboard_columns.go 是 Agent 与 editable 枚举的 Go 来源；前端列定义必须与之逐项一致。
function backendStringLists(name: string): string[][] {
    const source = readFileSync(resolve(import.meta.dir, "../../backend/internal/canvas/capability/storyboard_columns.go"), "utf8");
    const start = source.indexOf(`var ${name} = `);
    if (start < 0) throw new Error(`missing ${name} in storyboard_columns.go`);
    const open = source.indexOf("{", start);
    let depth = 0;
    let end = open;
    for (; end < source.length; end += 1) {
        if (source[end] === "{") depth += 1;
        if (source[end] === "}") depth -= 1;
        if (depth === 0) break;
    }
    const body = source.slice(open + 1, end);
    const groups = body.includes("{") ? [...body.matchAll(/\{([^{}]*)\}/g)].map((match) => match[1]) : [body];
    return groups.map((group) => [...group.matchAll(/"([^"]+)"/g)].map((match) => match[1]));
}

describe("分镜列与后端合同一致", () => {
    test("全部列 key 与顺序等于后端 storyboardColumnNames", () => {
        const [backendColumns] = backendStringLists("storyboardColumnNames");
        expect(backendColumns).toHaveLength(19);
        expect(STORYBOARD_COLUMN_OPTIONS.map((option) => option.value)).toEqual(backendColumns as StoryboardColumn[]);
    });

    test("默认列与历史默认集合等于后端定义", () => {
        const [backendDefault] = backendStringLists("storyboardDefaultColumnNames");
        expect([...DEFAULT_STORYBOARD_COLUMNS]).toEqual(backendDefault as StoryboardColumn[]);
        const legacySets = backendStringLists("storyboardLegacyDefaultColumnSets");
        expect(legacySets).toHaveLength(4);
        for (const legacy of legacySets) {
            expect(resolveStoryboardVisibleColumns(legacy as StoryboardColumn[])).toEqual(EXPECTED_DEFAULT_COLUMNS);
        }
    });

    test("上游关联资产列（#799）保留在列选择器与默认列中", () => {
        expect(STORYBOARD_COLUMN_OPTIONS.find((option) => option.value === "assets")?.label).toBe("关联资产");
        expect(DEFAULT_STORYBOARD_COLUMNS).toContain("assets");
    });
});

describe("分镜卡片接入共享列定义", () => {
    const scriptSource = readFileSync(resolve(import.meta.dir, "../src/components/canvas/canvas-script-node.tsx"), "utf8");

    test("画布卡片与全屏编辑器都按共享列定义渲染，不再写死五列", () => {
        expect(scriptSource).toContain('from "@/lib/canvas/canvas-storyboard-columns"');
        expect(scriptSource).not.toContain("SCRIPT_GRID_TEMPLATE");
        expect(scriptSource).not.toMatch(/const columnOptions\b/);
        expect(scriptSource).toContain("storyboardCardGridTemplate(visibleColumns)");
    });

    test("新建与回写路径使用共享默认列，不再散落旧五列默认", () => {
        for (const file of [
            "constant/canvas.ts",
            "lib/canvas/canvas-project-domain.ts",
            "lib/canvas/canvas-short-drama.ts",
            "lib/canvas/canvas-storyboard-operations.ts",
            "lib/canvas/project-chapter-storyboard.ts",
            "pages/canvas/use-canvas-storyboard.ts",
        ]) {
            const source = readFileSync(resolve(import.meta.dir, `../src/${file}`), "utf8");
            expect({ file, legacy: source.includes('"videoMotionPrompt", "dialogue", "assets"]') }).toEqual({ file, legacy: false });
        }
    });
});

describe("生成结果回写保留用户列选择", () => {
    test("分镜生成完成后按 resolveStoryboardVisibleColumns 归一，不再强制补出十五列", () => {
        for (const file of ["pages/canvas/use-canvas-storyboard.ts", "pages/canvas/use-canvas-generation.ts", "pages/canvas/project.tsx"]) {
            const source = readFileSync(resolve(import.meta.dir, `../src/${file}`), "utf8");
            expect({ file, cinematic: /visibleColumns:\s*cinematicStoryboardColumns\(/.test(source) }).toEqual({ file, cinematic: false });
        }
    });
});
