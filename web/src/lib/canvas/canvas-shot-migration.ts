import type { CanvasReferenceMigrationConflict } from "@/lib/canvas/canvas-reference-migration";

export type CanvasShotMigrationPreview = {
    revision: number;
    required: boolean;
    ready: boolean;
    conflicts: CanvasReferenceMigrationConflict[];
    shotSubmaps: number;
    rows: number;
    activeTasks: string[];
    blocking: Array<{ code: string; path?: string; message: string }>;
    warnings: Array<{ code: string; path?: string; message: string }>;
    rollback?: {
        snapshotId: string;
        snapshotRevision: number;
        migrationRevision: number;
        revision: number;
        newerEdits: boolean;
        canRestore: boolean;
        canCopy: boolean;
    };
};

export type CanvasShotMigrationResult = {
    project: { id: string; revision: number; updatedAt?: string };
    submaps: number;
    rows: number;
    existingImages: number;
    existingVideos: number;
    preservedAssets: number;
};

export type CanvasShotMigrationRollbackResult = {
    restored: boolean;
    newCanvasId?: string;
    snapshotId: string;
    migrationRevision: number;
    revision?: number;
};

export function shotMigrationReasonMessage(reason: string | undefined): string | undefined {
    switch (reason) {
        case "shot_migration_tasks_active":
            return "画布仍有在途生成任务，请等待任务结束或取消后再切换";
        case "shot_migration_conflict":
            return "还有引用位置需要先完成引用迁移";
        case "shot_migration_not_required":
            return "没有待转换的旧分镜行";
        case "shot_migration_has_newer_edits":
            return "切换后已有新编辑，不能直接覆盖；请选择“保留当前并生成迁移前副本”";
        case "shot_migration_rollback_missing":
            return "找不到这次格式切换前的历史快照";
        case "shot_migration_rollback_not_applicable":
            return "当前画布不是升级后的镜头子画布格式，无需回退";
        default:
            return undefined;
    }
}

export function shotMigrationErrorText(error: unknown): string {
    const reason = error && typeof error === "object" && "reason" in error ? String((error as { reason?: unknown }).reason || "") : "";
    return shotMigrationReasonMessage(reason) || (error instanceof Error && error.message ? error.message : "分镜迁移未完成，请重试");
}
