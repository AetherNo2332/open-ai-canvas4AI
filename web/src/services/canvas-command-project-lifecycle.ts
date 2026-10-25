import { bindCanvasCommandEditor } from "./canvas-command-editor-binding";
import type { CanvasCommandSession, CommandSessionState } from "./canvas-command-session";
import type { CanvasCapabilities } from "../lib/canvas/canvas-editor-commands";
import type { CanvasDocument } from "../lib/canvas/canvas-event-projection";
import { applyCanvasDraftChanges, canvasDocumentChanges } from "../lib/canvas/canvas-event-projection";
import { sameCanvasJsonValue } from "../lib/canvas/canvas-json-value";
import type { SyncProjectProgress } from "../stores/use-sync-progress-store";
type Viewport = { x: number; y: number; k: number };
/** A local edit that cannot become host commands. Nothing was queued for it,
 * so the session alone would still read as saved. */
export const CANVAS_DRAFT_UNSUPPORTED = "editor_draft_unsupported";
export type CanvasDraftFailure = { detail: string; backup?: "saved" | "failed" };
export function canvasCommandSyncProgress(state: CommandSessionState, draftFailure?: CanvasDraftFailure): Partial<SyncProjectProgress> {
    if (draftFailure && state.saveState !== "conflict") {
        const backup = draftFailure.backup === "saved" ? "。当前内容已备份为本地草稿" : draftFailure.backup === "failed" ? "。本地草稿备份失败，请先下载当前内容，不要关闭页面" : "";
        return { phase: "error", reason: CANVAS_DRAFT_UNSUPPORTED, message: `修改未保存，尚未提交到云端：${draftFailure.detail}${backup}` };
    }
    return {
        phase: state.saveState === "conflict" ? "conflict" : state.saveState === "offline" || state.saveState === "error" ? "error" : state.saveState === "saved" ? "done" : "saving",
        reason: state.rejections[0]?.reason,
        draftCount: state.pending.length,
        message: state.saveState === "conflict" ? "编辑草稿已保留，请处理冲突" : state.saveState === "error" ? state.rejections[0]?.message : state.saveState === "offline" ? "主机暂不可用，未确认编辑已保留" : undefined,
    };
}
/** The real editor uses the same adapter as reconnect/close acceptance checks.
 * Personal view storage is independent of the shared document and its history. */
export async function attachCanvasCommandProject<N, E>(deps: {
    canvasId: string;
    session: CanvasCommandSession;
    capabilities: CanvasCapabilities;
    refs: { nodes: { current: N[] }; connections: { current: E[] } };
    read: () => CanvasDocument;
    present: (document: CanvasDocument, state: CommandSessionState) => void;
    onDraftConversion?: (error?: unknown) => void;
    preserveDraft?: (document: CanvasDocument) => Promise<unknown>;
    view: { read: (id: string) => Promise<Viewport | null>; write: (id: string, value: Viewport) => Promise<unknown> };
    readViewport: () => Viewport;
    presentViewport: (value: Viewport) => void;
    signal?: AbortSignal;
}) {
    deps.signal?.throwIfAborted();
    const viewport = await deps.view.read(deps.canvasId);
    deps.signal?.throwIfAborted();
    if (viewport) deps.presentViewport(viewport);
    let binding = bindCanvasCommandEditor(deps.session, deps.capabilities, deps);
    return {
        commit: () => binding.commit(),
        hasLocalChanges: () => binding.hasLocalChanges(),
        async preserveLocalDraft() {
            if (binding.hasLocalChanges()) {
                if (!deps.preserveDraft) throw new Error("本地编辑尚未备份，无法关闭画布会话");
                const local = structuredClone(deps.read());
                await deps.preserveDraft(local);
                if (binding.hasLocalChanges() && !sameCanvasJsonValue(local, deps.read())) throw new Error("画布仍在更新，请稍后再离开");
            }
        },
        needsLeaveProtection: () => binding.hasLocalChanges() || deps.session.state.pending.length > 0,
        async prepareLeave(preserveDraft: (document: CanvasDocument) => Promise<unknown>) {
            let retained: CanvasDocument | undefined;
            if (binding.hasLocalChanges()) {
                const local = structuredClone(deps.read());
                await preserveDraft(local);
                retained = local;
                deps.signal?.throwIfAborted();
                if (!sameCanvasJsonValue(local, deps.read())) throw new Error("画布仍在更新，请稍后再离开");
                // Queuing is synchronous. Do not wait for a remote ACK when offline;
                // the raw draft and command journal must be durable before leaving.
                void binding.commit().catch(() => {});
            }
            await deps.session.whenJournalDurable();
            if (binding.hasLocalChanges() && (!retained || !sameCanvasJsonValue(retained, deps.read()))) throw new Error("画布仍在更新，请稍后再离开");
        },
        saveViewport: () => deps.view.write(deps.canvasId, structuredClone(deps.readViewport())),
        async flush() {
            let failed = false;
            const commit = binding.commit().catch(() => {
                failed = true;
            });
            await deps.view.write(deps.canvasId, structuredClone(deps.readViewport()));
            // Unconfirmed drafts are durable; a close check must not wait forever for
            // a promise intentionally retained until reconnect provides its receipt.
            if (!(await deps.session.flush())) return false;
            await commit;
            return !failed;
        },
        async loadLatest(preserveDraft: (document: CanvasDocument) => Promise<void>) {
            deps.signal?.throwIfAborted();
            const local = structuredClone(deps.read());
            const projected = structuredClone(deps.session.state.projected);
            const entities = Object.fromEntries((["nodes", "connections"] as const).flatMap((field) => ((projected[field] || []) as Array<Record<string, unknown>>).map((entity) => [`${field === "nodes" ? "node" : "connection"}:${entity.id}`, entity])));
            await deps.view.write(deps.canvasId, structuredClone(deps.readViewport()));
            const validateLocalDraft = () => {
                deps.signal?.throwIfAborted();
                if (!sameCanvasJsonValue(local, deps.read())) throw new Error("画布仍在更新，本地修改已保留，请稍后再加载云端版本");
            };
            await deps.session.loadLatest(async (draft) => {
                await preserveDraft(applyCanvasDraftChanges(draft, canvasDocumentChanges(projected, local), entities, projected));
            }, validateLocalDraft);
            // Rebind against the accepted host snapshot so old local field
            // observations cannot be submitted again by the next autosave.
            binding.dispose();
            binding = bindCanvasCommandEditor(deps.session, deps.capabilities, deps);
        },
        async retryRejected(preserveDraft: (document: CanvasDocument) => Promise<void>) {
            deps.signal?.throwIfAborted();
            if (!deps.session.state.rejections.length) throw new Error("没有可直接重试的保存，请先处理冲突或确认提交结果");
            await deps.session.reconnect();
            const state = deps.session.state;
            if (!state.acknowledged || !state.rejections.length) throw new Error("没有可直接重试的保存，请先处理冲突或确认提交结果");
            const draft = binding.prepareRetry();
            const visible = structuredClone(deps.read());
            await deps.session.replaceRejectedDraft(
                draft.operations,
                draft.preconditions,
                () => preserveDraft(visible),
                () => {
                    deps.signal?.throwIfAborted();
                    if (!sameCanvasJsonValue(draft.document, binding.readDraft())) throw new Error("画布仍在更新，本地修改已保留，请稍后再重试");
                },
            );
        },
        dispose: () => binding.dispose(),
    };
}
