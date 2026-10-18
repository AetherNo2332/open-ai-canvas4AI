import { useCallback, useEffect, useMemo, useState } from "react";

import { canvasCommandSession, useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";
import {
    buildCanvasReferenceMigrationOperation,
    referenceMigrationConflicts,
    referenceMigrationErrorText,
    referenceMigrationPendingScopeKeys,
    referenceMigrationReasonMessage,
    referenceMigrationScopeChoices,
    referenceMigrationRequired,
    type CanvasReferenceMigrationChoice,
} from "@/lib/canvas/canvas-reference-migration";

/**
 * 历史画布只做一次规范化：预览来自命令会话快照，应用走真实 migrate_references 命令。
 * 编辑或生成被主机以 reference_migration_* 拒绝时，这里会重新打开同一入口。
 */
export function useCanvasReferenceMigration(projectId: string) {
    const commandState = useCanvasSessionStore((state) => (projectId ? state.states[projectId] : undefined));
    const preview = commandState?.acknowledged?.referenceMigration;
    const conflicts = useMemo(() => referenceMigrationConflicts(preview), [preview]);
    const blockedReason = useMemo(() => (commandState?.conflicts || []).map((conflict) => conflict.reason).find((reason) => reason.startsWith("reference_migration_")), [commandState?.conflicts]);
    const [dismissed, setDismissed] = useState(false);
    const [applying, setApplying] = useState(false);
    const [error, setError] = useState<string>();
    const signature = `${referenceMigrationRequired(preview) ? "required" : "normalized"}|${conflicts.map((conflict) => conflict.scopeKey).join(",")}|${blockedReason || ""}`;
    // 预览变化（首次读取、撤销后重新出现、主机再次拒绝）时重新进入该入口并丢弃旧选择。
    useEffect(() => {
        setDismissed(false);
        setApplying(false);
        setError(blockedReason ? referenceMigrationReasonMessage(blockedReason) : undefined);
    }, [signature, blockedReason]);
    const apply = useCallback(
        async (choices: Record<string, CanvasReferenceMigrationChoice>) => {
            const pending = referenceMigrationPendingScopeKeys(preview, choices);
            if (pending.length) {
                setError(referenceMigrationReasonMessage("reference_migration_conflict"));
                return;
            }
            const session = canvasCommandSession(projectId);
            if (!session) {
                setError("画布主机尚未就绪，请稍后再试");
                return;
            }
            setApplying(true);
            setError(undefined);
            try {
                await session.submit([buildCanvasReferenceMigrationOperation(projectId, referenceMigrationScopeChoices(preview, choices))]);
                setDismissed(true);
            } catch (reason) {
                setError(referenceMigrationErrorText(reason));
            } finally {
                setApplying(false);
            }
        },
        [preview, projectId],
    );
    return {
        open: referenceMigrationRequired(preview) && !dismissed,
        required: referenceMigrationRequired(preview),
        conflicts,
        applying,
        error,
        dismiss: useCallback(() => setDismissed(true), []),
        apply,
    };
}
