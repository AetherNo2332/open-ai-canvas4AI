import { useCallback, useEffect, useMemo, useState } from "react";

import { canvasCommandSession, useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";
import { createCanvasCommandTransport } from "@/services/api/canvas-commands";
import { migrateCanvasReferences } from "@/services/canvas-reference-migration";
import { refreshCanvasAfterAgent } from "@/services/user-data-sync";
import type { Snapshot } from "@/lib/canvas/command-contract.generated";
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
 * 两种宿主共用迁移命令：桌面读取既有会话，普通网页直接读取主机快照。
 * 网页迁移前保存本地编辑，完成后通过既有云端刷新链路投影结果。
 */
export function useCanvasReferenceMigration(projectId: string, options: { enabled: boolean; scope: string; revision?: number; referenceFieldsVersion?: 1; save: () => Promise<boolean> }) {
    const commandScope = useCanvasSessionStore((state) => state.scope);
    const commandState = useCanvasSessionStore((state) => (projectId ? state.states[projectId] : undefined));
    const transport = useMemo(() => createCanvasCommandTransport(), [projectId, options.scope, commandScope]);
    const [webSnapshot, setWebSnapshot] = useState<Snapshot>();
    const preview = commandScope ? commandState?.acknowledged?.referenceMigration : options.referenceFieldsVersion !== 1 && webSnapshot?.canvasId === projectId ? webSnapshot.referenceMigration : undefined;
    const conflicts = useMemo(() => referenceMigrationConflicts(preview), [preview]);
    const blockedReason = useMemo(() => (commandState?.conflicts || []).map((conflict) => conflict.reason).find((reason) => reason.startsWith("reference_migration_")), [commandState?.conflicts]);
    const [dismissed, setDismissed] = useState(false);
    const [applying, setApplying] = useState(false);
    const [error, setError] = useState<string>();
    useEffect(() => {
        if (commandScope || !options.enabled || !projectId || options.referenceFieldsVersion === 1) return;
        const abort = new AbortController();
        void transport
            .snapshot(projectId, abort.signal)
            .then((snapshot) => {
                if (!abort.signal.aborted) setWebSnapshot(snapshot);
            })
            .catch((reason) => {
                if (!abort.signal.aborted) setError(referenceMigrationErrorText(reason));
            });
        return () => abort.abort();
    }, [commandScope, options.enabled, options.revision, options.referenceFieldsVersion, projectId, transport]);
    const signature = `${options.scope}|${projectId}|${referenceMigrationRequired(preview) ? "required" : "normalized"}|${conflicts.map((conflict) => conflict.scopeKey).join(",")}|${blockedReason || ""}`;
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
            setApplying(true);
            setError(undefined);
            try {
                if (commandScope) {
                    const session = canvasCommandSession(projectId);
                    if (!session) throw new Error("画布主机尚未就绪，请稍后再试");
                    await session.submit([buildCanvasReferenceMigrationOperation(projectId, referenceMigrationScopeChoices(preview, choices))]);
                } else {
                    const snapshot = webSnapshot?.canvasId === projectId ? webSnapshot : await transport.snapshot(projectId);
                    const migrated = await migrateCanvasReferences({
                        snapshot,
                        choices,
                        transport,
                        save: async () => {
                            if (!(await options.save())) throw new Error("本地编辑尚未保存到云端，请先处理同步问题");
                        },
                        refresh: () => refreshCanvasAfterAgent(projectId),
                    });
                    setWebSnapshot(migrated);
                }
                setDismissed(true);
            } catch (reason) {
                setError(referenceMigrationErrorText(reason));
                if (!commandScope) {
                    try {
                        setWebSnapshot(await transport.snapshot(projectId));
                    } catch (refreshError) {
                        setError(referenceMigrationErrorText(refreshError));
                    }
                }
            } finally {
                setApplying(false);
            }
        },
        [preview, projectId, commandScope, webSnapshot, transport, options.save],
    );
    return {
        open: options.enabled && (referenceMigrationRequired(preview) || Boolean(error)) && !dismissed,
        required: referenceMigrationRequired(preview),
        conflicts,
        applying,
        error,
        show: useCallback(() => setDismissed(false), []),
        dismiss: useCallback(() => setDismissed(true), []),
        apply,
    };
}
