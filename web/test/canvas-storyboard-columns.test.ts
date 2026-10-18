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
