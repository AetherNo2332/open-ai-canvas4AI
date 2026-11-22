import { useCallback, useEffect, useState } from "react";

import { getCanvasShotMigration, migrateCanvasShots, rollbackCanvasShots } from "@/services/api/user-data";
import { refreshCanvasAfterAgent } from "@/services/user-data-sync";
import { shotMigrationErrorText, type CanvasShotMigrationPreview } from "@/lib/canvas/canvas-shot-migration";

/**
 * 旧分镜行切换为镜头子画布。预览只读；执行成功后重新拉取画布，让 store 拿到
 * 新的 submap 节点、idCounters 与字段投影。引用冲突必须先由引用迁移处理。
 */
export function useCanvasShotMigration(projectId: string, options: { enabled: boolean; referenceFieldsVersion?: number }) {
    const [preview, setPreview] = useState<CanvasShotMigrationPreview | null>(null);
    const [open, setOpen] = useState(false);
    const [applying, setApplying] = useState(false);
    const [error, setError] = useState("");
    const active = options.enabled && Boolean(projectId);

    useEffect(() => {
        setPreview(null);
        setError("");
        if (!active) return;
        const controller = new AbortController();
        getCanvasShotMigration(projectId, controller.signal)
            .then((next) => {
                if (!controller.signal.aborted) setPreview(next.required ? next : null);
            })
            .catch((cause) => {
                if (!controller.signal.aborted) console.warn("分镜迁移预览读取失败", cause);
            });
        return () => controller.abort();
    }, [active, projectId, options.referenceFieldsVersion]);

    const apply = useCallback(async () => {
        if (!preview || preview.conflicts.length > 0) return;
        setApplying(true);
        setError("");
        try {
            await migrateCanvasShots(projectId, preview.revision);
            await refreshCanvasAfterAgent(projectId, "sync");
            setPreview(null);
            setOpen(false);
        } catch (cause) {
            setError(shotMigrationErrorText(cause));
            getCanvasShotMigration(projectId)
                .then((next) => setPreview(next.required ? next : null))
                .catch(() => undefined);
        } finally {
            setApplying(false);
        }
    }, [preview, projectId]);

    const rollback = useCallback(
        async (strategy: "restore" | "new_canvas") => {
            if (!preview) return null;
            setApplying(true);
            setError("");
            try {
                const result = await rollbackCanvasShots(projectId, preview.revision, strategy);
                await refreshCanvasAfterAgent(projectId, "sync");
                return result;
            } catch (cause) {
                setError(shotMigrationErrorText(cause));
                return null;
            } finally {
                setApplying(false);
            }
        },
        [preview, projectId],
    );

    return { preview, required: Boolean(preview), open, setOpen, applying, error, apply, rollback };
}
