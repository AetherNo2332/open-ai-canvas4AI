import { useCallback, useEffect, useState } from "react";

import { getCanvasShotMigration, migrateCanvasShots, rollbackCanvasShots } from "@/services/api/user-data";
import { refreshCanvasAfterAgent } from "@/services/user-data-sync";
import { shotMigrationErrorText, type CanvasShotMigrationPreview } from "@/lib/canvas/canvas-shot-migration";

/**
 * 旧分镜行切换为镜头子画布。预览只读；执行成功后重新拉取画布，让 store 拿到
 * 新的 submap 节点、idCounters 与字段投影。引用冲突必须先由引用迁移处理。
 */
export function useCanvasShotMigration(projectId: string, options: { enabled: boolean; referenceFieldsVersion?: number; onOpenCanvas: (canvasId: string) => void }) {
    const [preview, setPreview] = useState<CanvasShotMigrationPreview | null>(null);
    const [open, setOpen] = useState(false);
    const [applying, setApplying] = useState(false);
    const [error, setError] = useState("");
    const onOpenCanvas = options.onOpenCanvas;
    const active = options.enabled && Boolean(projectId);

    const loadPreview = useCallback(
        async (signal?: AbortSignal) => {
            const next = await getCanvasShotMigration(projectId, signal);
            if (!signal?.aborted) setPreview(next.required || next.rollback ? next : null);
        },
        [projectId],
    );

    useEffect(() => {
        setPreview(null);
        setError("");
        if (!active) return;
        const controller = new AbortController();
        loadPreview(controller.signal).catch((cause) => {
            if (!controller.signal.aborted) console.warn("分镜迁移预览读取失败", cause);
        });
        return () => controller.abort();
    }, [active, loadPreview, options.referenceFieldsVersion]);

    // 打开面板时重新读取预览，避免升级后的编辑让回退选项停在旧 revision。
    useEffect(() => {
        if (!open || !active) return;
        const controller = new AbortController();
        loadPreview(controller.signal).catch((cause) => {
            if (!controller.signal.aborted) console.warn("分镜迁移预览刷新失败", cause);
        });
        return () => controller.abort();
    }, [active, loadPreview, open]);

    const apply = useCallback(async () => {
        if (!preview || preview.conflicts.length > 0) return;
        setApplying(true);
        setError("");
        try {
            await migrateCanvasShots(projectId, preview.revision);
            await refreshCanvasAfterAgent(projectId, "sync");
            const next = await getCanvasShotMigration(projectId);
            setPreview(next.required || next.rollback ? next : null);
            setOpen(false);
        } catch (cause) {
            setError(shotMigrationErrorText(cause));
            loadPreview().catch(() => undefined);
        } finally {
            setApplying(false);
        }
    }, [loadPreview, preview, projectId]);

    const rollback = useCallback(
        async (strategy: "restore" | "new_canvas") => {
            if (!preview) return null;
            setApplying(true);
            setError("");
            try {
                const result = await rollbackCanvasShots(projectId, preview.revision, strategy);
                await refreshCanvasAfterAgent(projectId, "sync");
                setOpen(false);
                if (result.newCanvasId) {
                    setPreview(null);
                    onOpenCanvas(result.newCanvasId);
                    return result;
                }
                await loadPreview();
                return result;
            } catch (cause) {
                setError(shotMigrationErrorText(cause));
                await loadPreview().catch(() => undefined);
                return null;
            } finally {
                setApplying(false);
            }
        },
        [loadPreview, onOpenCanvas, preview, projectId],
    );

    return {
        preview,
        required: Boolean(preview?.required),
        rollbackAvailable: Boolean(preview?.rollback),
        open,
        setOpen,
        applying,
        error,
        apply,
        rollback,
    };
}
