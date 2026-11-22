import { describe, expect, test } from "bun:test";

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
