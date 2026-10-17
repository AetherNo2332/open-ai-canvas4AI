import { applyCanvasDraftChanges, canvasDocumentChanges, type CanvasDocument } from "../lib/canvas/canvas-event-projection";
import { canvasEditorCommands, type CanvasCapabilities } from "../lib/canvas/canvas-editor-commands";
import type { ChangeSet } from "../lib/canvas/command-contract.generated";
import type { CanvasCommandSession, CommandSessionState } from "./canvas-command-session";
import { canvasPersistentEditorDraft,carryCanvasTaskDisplay } from "../lib/canvas/canvas-editor-transient";
type Entity = Record<string, unknown>;
function bases(document: CanvasDocument, changes: ChangeSet) {
    const entities: Record<string, Entity> = {},
        fields: Record<string, unknown> = {};
    for (const change of changes.entities) {
        const kind = change.key.startsWith("node:") ? "nodes" : "connections";
        const value = ((document[kind] as Entity[]) || []).find((e) => e.id === change.key.slice(change.key.indexOf(":") + 1));
        if (value) entities[change.key] = value;
    }
    for (const key of Object.keys(changes.fields || {})) if (key in document) Object.defineProperty(fields, key, { value: document[key], enumerable: true });
    return { entities, fields };
}
function keys(changes: ChangeSet) {
    return [...changes.entities.map((e) => e.key), ...Object.keys(changes.fields || {}).map((key) => "project:" + key), ...(changes.nodeOrder ? ["project:nodeOrder"] : []), ...(changes.connectionOrder ? ["project:connectionOrder"] : [])];
}
function sharedDraft(base: CanvasDocument, live: CanvasDocument) {
    const target = canvasPersistentEditorDraft(base, { ...base, ...live });
    for (const key of ["viewport", "selection", "panelState", "presence", "id", "revision", "createdAt", "updatedAt", "remoteContentHash"]) {
        if (key in base) target[key] = base[key];
        else delete target[key];
    }
    return target;
}
export function bindCanvasCommandEditor<N, E>(
    session: CanvasCommandSession,
    capabilities: CanvasCapabilities,
    editor: { refs: { nodes: { current: N[] }; connections: { current: E[] } }; read: () => CanvasDocument; present: (document: CanvasDocument, state: CommandSessionState) => void },
) {
    let previous: CanvasDocument | null = null;
    let versions: Record<string, number> = {};
    let draftExpected: Record<string, number> = {};
    let disposed = false;
    const unsubscribe = session.subscribe((state) => {
        if (disposed || !state.acknowledged) return;
        let projected = structuredClone(state.projected);
        if (previous) {
            const display=editor.read();
            const live = sharedDraft(previous, display);
            const changes = canvasDocumentChanges(previous, live);
            const base = bases(previous, changes);
            for (const key of keys(changes)) draftExpected[key] ??= versions[key] ?? 0;
            projected = applyCanvasDraftChanges(projected, changes, base.entities, base.fields);
            carryCanvasTaskDisplay(previous,display,projected);
        }
        previous = structuredClone(state.projected);
        versions = { ...state.acknowledged.entityVersions };
        const remaining = new Set(keys(canvasDocumentChanges(state.projected, sharedDraft(state.projected, projected))));
        for (const key of Object.keys(draftExpected)) if (!remaining.has(key)) delete draftExpected[key];
        editor.refs.nodes.current = (projected.nodes || []) as N[];
        editor.refs.connections.current = (projected.connections || []) as E[];
        editor.present(projected, state);
    });
    return {
        commit() {
            if (disposed) return Promise.reject(new DOMException("画布编辑器已关闭", "AbortError"));
            const target = sharedDraft(session.state.projected, editor.read());
            const operations = canvasEditorCommands(session.state.projected, target, capabilities);
            if (!operations.length) return Promise.resolve(undefined);
            return session.submit(operations, { preview: canvasDocumentChanges(session.state.projected, target), expectedEntityVersions: { ...draftExpected } });
        },
        dispose() {
            disposed = true;
            unsubscribe();
        },
    };
}
