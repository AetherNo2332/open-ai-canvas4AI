import { useCallback, useEffect, useState } from "react";

import { getCanvasReferenceMigration, migrateCanvasReferences } from "@/services/api/user-data";
import { refreshCanvasAfterAgent } from "@/services/user-data-sync";
import { referenceMigrationErrorText, referenceMigrationScopeChoices, type CanvasReferenceMigrationChoice, type CanvasReferenceMigrationPreview } from "@/lib/canvas/canvas-reference-migration";

/**
 * 旧版画布（缺 referenceFieldsVersion）的一次性引用规范化。只在画布加载完成且确实是旧版时
 * 读取预览；迁移成功后从服务端重新拉取画布，store 随之拿到声明式标记与托管连线。
 */
export function useCanvasReferenceMigration(projectId: string, options: { enabled: boolean; legacy: boolean }) {
    const [preview, setPreview] = useState<CanvasReferenceMigrationPreview | null>(null);
    const [open, setOpen] = useState(false);
    const [applying, setApplying] = useState(false);
    const [error, setError] = useState("");
    const active = options.enabled && options.legacy && Boolean(projectId);

    useEffect(() => {
        setPreview(null);
        setError("");
        if (!active) return;
        const controller = new AbortController();
        getCanvasReferenceMigration(projectId, controller.signal)
            .then((next) => {
                if (!controller.signal.aborted) setPreview(next.required ? next : null);
            })
            .catch((cause) => {
                if (!controller.signal.aborted) console.warn("引用迁移预览读取失败", cause);
            });
        return () => controller.abort();
    }, [active, projectId]);

    const apply = useCallback(
        async (choices: Record<string, CanvasReferenceMigrationChoice>) => {
            if (!preview) return;
            setApplying(true);
            setError("");
            try {
                await migrateCanvasReferences(projectId, preview.revision, referenceMigrationScopeChoices(preview.conflicts, choices));
                await refreshCanvasAfterAgent(projectId, "sync");
                setPreview(null);
                setOpen(false);
            } catch (cause) {
                setError(referenceMigrationErrorText(cause));
                // 画布被别处改过或已迁移：重新读一次预览，按最新状态继续。
                getCanvasReferenceMigration(projectId)
                    .then((next) => setPreview(next.required ? next : null))
                    .catch(() => undefined);
            } finally {
                setApplying(false);
            }
        },
        [preview, projectId],
    );

    return { required: Boolean(preview), conflicts: preview?.conflicts ?? [], open, setOpen, applying, error, apply };
}
