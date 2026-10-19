import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";

import { CanvasReferenceMigrationContent } from "@/components/canvas/canvas-reference-migration-panel";
import {
    assertCanvasReferenceMigrationReady,
    buildCanvasReferenceMigrationOperation,
    referenceMigrationErrorText,
    referenceMigrationPendingScopeKeys,
    referenceMigrationReasonMessage,
    referenceMigrationRequired,
    referenceMigrationScope,
    referenceMigrationScopeChoices,
} from "@/lib/canvas/canvas-reference-migration";
import type { MigrationConflict, MigrationPreview } from "@/lib/canvas/command-contract.generated";

const conflicts: MigrationConflict[] = [
    { scopeKey: "storyboard-row:board-1:row-1", fieldNodeIds: ["asset-1"], edgeNodeIds: ["asset-2"] },
    { scopeKey: "node:compare-1", fieldNodeIds: [], edgeNodeIds: ["asset-3"] },
];
const preview: MigrationPreview = { required: true, conflicts };

test("legacy documents need migration and normalized ones do not", () => {
    expect(referenceMigrationRequired(undefined)).toBe(false);
    expect(referenceMigrationRequired({ required: false, conflicts: [] })).toBe(false);
    expect(referenceMigrationRequired(preview)).toBe(true);
    expect(() => assertCanvasReferenceMigrationReady(undefined)).not.toThrow();
    expect(() => assertCanvasReferenceMigrationReady(preview)).toThrow("素材关联");
});

test("only scopes with a real choice are submitted", () => {
    expect(referenceMigrationPendingScopeKeys(preview, {})).toEqual(["storyboard-row:board-1:row-1", "node:compare-1"]);
    expect(referenceMigrationPendingScopeKeys(preview, { "storyboard-row:board-1:row-1": "fields" })).toEqual(["node:compare-1"]);
    const choices = referenceMigrationScopeChoices(preview, { "storyboard-row:board-1:row-1": "fields", "node:compare-1": "connections", "node:unknown": "fields" });
    expect(choices).toEqual({ "storyboard-row:board-1:row-1": "fields", "node:compare-1": "connections" });
});

test("migration operation carries the canvas id and scope choices only", () => {
    expect(buildCanvasReferenceMigrationOperation("canvas-9", { "node:compare-1": "connections" })).toEqual({ type: "migrate_references", id: "canvas-9", patch: { scopeChoices: { "node:compare-1": "connections" } } });
    expect(buildCanvasReferenceMigrationOperation("canvas-9", {})).toEqual({ type: "migrate_references", id: "canvas-9", patch: { scopeChoices: {} } });
    expect(() => buildCanvasReferenceMigrationOperation("", {})).toThrow("画布标识缺失");
    expect(() => buildCanvasReferenceMigrationOperation("canvas-9", { "node:compare-1": "merge" as never })).toThrow("引用来源只能是字段或旧连线");
});

test("scope keys are split for display without rewriting them", () => {
    expect(referenceMigrationScope("storyboard-row:board-1:row-1")).toMatchObject({ kind: "storyboard-row", kindLabel: "分镜行", nodeId: "board-1", rowId: "row-1" });
    expect(referenceMigrationScope("storyboard-context:board-1")).toMatchObject({ kindLabel: "分镜表共享素材", nodeId: "board-1", rowId: undefined });
    expect(referenceMigrationScope("batch-row:table-1:batch-row-2")).toMatchObject({ kindLabel: "批量表行", nodeId: "table-1", rowId: "batch-row-2" });
    expect(referenceMigrationScope("node:compare-1").kindLabel).toBe("节点输入");
});

test("migration failures map to actionable copy", () => {
    expect(referenceMigrationReasonMessage("reference_migration_required")).toContain("确认并升级");
    expect(referenceMigrationReasonMessage("reference_migration_conflict")).toContain("未选择要保留的素材");
    expect(referenceMigrationReasonMessage("reference_migration_not_required")).toContain("无需重复操作");
    expect(referenceMigrationReasonMessage("something_else")).toBeUndefined();
    expect(referenceMigrationErrorText({ reason: "reference_migration_conflict" })).toContain("未选择要保留的素材");
    expect(referenceMigrationErrorText(new Error("网络中断"))).toBe("网络中断");
});

test("panel shows both declared sides per scope and both source choices", () => {
    const html = renderToStaticMarkup(
        createElement(CanvasReferenceMigrationContent, {
            conflicts,
            nodeTitles: { "board-1": "雨夜分镜", "asset-1": "女主角定妆", "asset-2": "旧参考图", "asset-3": "道具图" },
            choices: {},
            onChoose: () => {},
            onCancel: () => {},
            onApply: () => {},
        }),
    );
    expect(html).not.toContain("完成一次引用规范化"); // 标题属于弹窗外壳，内容只负责作用域选择
    expect(html).toContain("分镜行");
    expect(html).toContain("雨夜分镜");
    expect(html).not.toContain("行 row-1");
    expect(html).toContain("仅素材列表中有");
    expect(html).toContain("仅旧连线中有");
    expect(html).toContain("女主角定妆");
    expect(html).toContain("旧参考图");
    expect(html).toContain("保留素材列表");
    expect(html).toContain("沿用旧连线");
    expect(html).toContain("确认并升级");
    // 字段侧为空的冲突显示“无”，不显示成空列表。
    expect(html).toContain(">无<");
    // 没有标题时不把内部编号当成名称。
    expect(html).toContain("未命名节点");
});

test("panel reports unresolved scopes, progress and errors without merging silently", () => {
    const pending = renderToStaticMarkup(createElement(CanvasReferenceMigrationContent, { conflicts, choices: {}, onChoose: () => {}, onCancel: () => {}, onApply: () => {}, applying: true }));
    expect(pending).toContain("disabled");
    const ready = renderToStaticMarkup(
        createElement(CanvasReferenceMigrationContent, {
            conflicts,
            choices: { "storyboard-row:board-1:row-1": "fields", "node:compare-1": "connections" },
            onChoose: () => {},
            onCancel: () => {},
            onApply: () => {},
            error: "还有作用域没有选择引用来源，请先为每个作用域选择字段或旧连线",
        }),
    );
    expect(ready).toContain('role="alert"');
    expect(ready).toContain("没有选择引用来源");
    const empty = renderToStaticMarkup(createElement(CanvasReferenceMigrationContent, { conflicts: [], choices: {}, onChoose: () => {}, onCancel: () => {}, onApply: () => {} }));
    expect(empty).toContain("无需选择保留哪组素材");
    expect(empty).toContain("确认并升级");
});
