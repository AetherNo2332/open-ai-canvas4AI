import { bindCanvasCommandEditor } from "./canvas-command-editor-binding";
import type { CanvasCommandSession, CommandSessionState } from "./canvas-command-session";
import type { CanvasCapabilities } from "../lib/canvas/canvas-editor-commands";
import type { CanvasDocument } from "../lib/canvas/canvas-event-projection";
import { applyCanvasDraftChanges, canvasDocumentChanges } from "../lib/canvas/canvas-event-projection";
import { sameCanvasJsonValue } from "../lib/canvas/canvas-json-value";
type Viewport = { x: number; y: number; k: number };
/** The real editor uses the same adapter as reconnect/close acceptance checks.
 * Personal view storage is independent of the shared document and its history. */
export async function attachCanvasCommandProject<N, E>(deps: {
    canvasId: string;
    session: CanvasCommandSession;
    capabilities: CanvasCapabilities;
    refs: { nodes: { current: N[] }; connections: { current: E[] } };
    read: () => CanvasDocument;
    present: (document: CanvasDocument, state: CommandSessionState) => void;
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
            const entities = Object.fromEntries(
                (["nodes", "connections"] as const).flatMap((field) => ((projected[field] || []) as Array<Record<string, unknown>>).map((entity) => [`${field === "nodes" ? "node" : "connection"}:${entity.id}`, entity])),
            );
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
        dispose: () => binding.dispose(),
    };
}
