import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";

import { shotMigrationErrorText, shotMigrationReasonMessage } from "@/lib/canvas/canvas-shot-migration";

describe("canvas shot migration reason text", () => {
    test("在途任务、冲突和新编辑分别给出可操作提示", () => {
        expect(shotMigrationReasonMessage("shot_migration_tasks_active")).toContain("在途生成任务");
        expect(shotMigrationReasonMessage("shot_migration_conflict")).toContain("引用迁移");
        expect(shotMigrationReasonMessage("shot_migration_has_newer_edits")).toContain("迁移前副本");
    });

    test("未知 reason 回落到错误消息或默认提示", () => {
        expect(shotMigrationReasonMessage("unknown")).toBeUndefined();
        expect(shotMigrationErrorText(new Error("网络失败"))).toBe("网络失败");
        expect(shotMigrationErrorText({ reason: "shot_migration_not_required" })).toContain("没有待转换");
        expect(shotMigrationReasonMessage("shot_migration_rollback_not_applicable")).toContain("无需回退");
    });
});

describe("canvas migration notice surface", () => {
    test("使用独立不透明表面，且入场不改变整体透明度", () => {
        const component = readFileSync(new URL("../src/pages/canvas/canvas-migration-notice.tsx", import.meta.url), "utf8");
        const styles = readFileSync(new URL("../src/styles/globals.css", import.meta.url), "utf8");

        expect(component).toContain("bg-[var(--canvas-notice-surface)]");
        expect(component).not.toContain("motion-safe:fade-in");
        expect(styles).toContain("--canvas-notice-surface: oklch(1 0 0)");
        expect(styles).toContain("--canvas-notice-surface: #222226");
    });
});
