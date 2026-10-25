import { applyCanvasDraftChanges, canvasDocumentChanges, type CanvasDocument } from "../lib/canvas/canvas-event-projection";
import { canvasEditorCommands, type CanvasCapabilities } from "../lib/canvas/canvas-editor-commands";
import type { ChangeSet } from "../lib/canvas/command-contract.generated";
import type { CanvasCommandSession, CommandSessionState } from "./canvas-command-session";
import { canvasPersistentEditorDraft, carryCanvasMeasuredLayout, carryCanvasTaskDisplay } from "../lib/canvas/canvas-editor-transient";
import { canvasBaselineAliases, canvasBaselineDescriptor, canvasBaselineValue, restoreCanvasBaselineValue, withCanvasFieldBaselines } from "../lib/canvas/canvas-field-baselines";
import { sameCanvasJsonValue } from "../lib/canvas/canvas-json-value";
import { mergeRejectedCanvasEdits } from "../lib/canvas/canvas-command-retry";
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
    editor: {
        refs: { nodes: { current: N[] }; connections: { current: E[] } };
        read: () => CanvasDocument;
        present: (document: CanvasDocument, state: CommandSessionState) => void;
        /** Conversion runs before anything is queued, so a failure here never reaches
         * the session state; the error is undefined once the draft converts again. */
        onDraftConversion?: (error?: unknown) => void;
    },
) {
    let previous: CanvasDocument | null = null;
    let versions: Record<string, number> = {};
    let generations: Record<string, number> = {};
    let draftExpected: Record<string, number> = {};
    const draftEntities: Record<string, Entity> = {},
        draftFields: Record<string, unknown> = {},
        draftGenerations: Record<string, number> = {};
    const capturedAliases = new Map<string, Set<string>>(),
        dirtyAliases = new Map<string, Set<string>>();
    let disposed = false;
    function capture(before: CanvasDocument, live: CanvasDocument) {
        const changes = canvasDocumentChanges(before, live),
            base = bases(before, changes);
        for (const key of keys(changes)) draftExpected[key] ??= versions[key] ?? 0;
        for (const [key, entity] of Object.entries(base.entities)) {
            draftEntities[key] ||= structuredClone(entity);
            if (!capturedAliases.has(key) && Object.hasOwn(generations, key)) draftGenerations[key] = generations[key];
            const after = ((live.nodes as Entity[]) || []).find((node) => "node:" + node.id === key);
            if (!key.startsWith("node:") || !after) continue;
            const descriptor = canvasBaselineDescriptor(entity, capabilities);
            if (!descriptor) continue;
            const changed = Object.entries(descriptor.patchFields)
                .filter(([, field]) => !sameCanvasJsonValue(canvasBaselineValue(entity, field.path), canvasBaselineValue(after, field.path)))
                .map(([alias]) => alias);
            const captured = capturedAliases.get(key) || new Set<string>(),
                dirty = dirtyAliases.get(key) || new Set<string>();
            for (const alias of changed) dirty.add(alias);
            for (const alias of canvasBaselineAliases(entity, changed, capabilities))
                if (!captured.has(alias)) {
                    restoreCanvasBaselineValue(draftEntities[key], descriptor.patchFields[alias].path, canvasBaselineValue(entity, descriptor.patchFields[alias].path));
                    captured.add(alias);
                }
            capturedAliases.set(key, captured);
            dirtyAliases.set(key, dirty);
        }
        for (const [key, value] of Object.entries(base.fields)) if (!Object.hasOwn(draftFields, key)) Object.defineProperty(draftFields, key, { value: structuredClone(value), enumerable: true, configurable: true });
        return { changes, base };
    }
    const unsubscribe = session.subscribe((state) => {
        if (disposed || !state.acknowledged) return;
        let projected = structuredClone(state.projected);
        if (previous) {
            const display = editor.read();
            const live = sharedDraft(previous, display);
            const { changes, base } = capture(previous, live);
            projected = applyCanvasDraftChanges(projected, changes, base.entities, base.fields);
            carryCanvasTaskDisplay(previous, display, projected);
            carryCanvasMeasuredLayout(display, projected);
        }
        previous = structuredClone(state.projected);
        versions = { ...state.acknowledged.entityVersions };
        generations = { ...state.acknowledged.entityGenerations };
        const remaining = new Set(keys(canvasDocumentChanges(state.projected, sharedDraft(state.projected, projected))));
        for (const key of Object.keys(draftExpected))
            if (!remaining.has(key)) {
                delete draftExpected[key];
                delete draftEntities[key];
                delete draftGenerations[key];
                capturedAliases.delete(key);
                dirtyAliases.delete(key);
                if (key.startsWith("project:")) delete draftFields[key.slice(8)];
            }
        editor.refs.nodes.current = (projected.nodes || []) as N[];
        editor.refs.connections.current = (projected.connections || []) as E[];
        editor.present(projected, state);
    });
    function prepareCommit() {
        if (disposed) throw new DOMException("画布编辑器已关闭", "AbortError");
        const target = sharedDraft(session.state.projected, editor.read());
        capture(previous || session.state.projected, target);
        // Compare dirty aliases against their original observation even if
        // a remote event has since changed that same field (or deleted it).
        const comparison = structuredClone(session.state.projected);
        const nodes = (comparison.nodes ||= []) as Entity[];
        for (const [key, aliases] of dirtyAliases) {
            const original = draftEntities[key];
            let node = nodes.find((value) => "node:" + value.id === key);
            if (!node) {
                node = structuredClone(original);
                nodes.push(node);
            }
            const descriptor = canvasBaselineDescriptor(original, capabilities);
            if (descriptor) for (const alias of aliases) restoreCanvasBaselineValue(node, descriptor.patchFields[alias].path, canvasBaselineValue(original, descriptor.patchFields[alias].path));
        }
        const operations = withCanvasFieldBaselines(canvasEditorCommands(comparison, target, capabilities), draftEntities, draftGenerations, capabilities);
        return {
            document: target,
            operations,
            preconditions: {
                preview: canvasDocumentChanges(session.state.projected, target),
                expectedEntityVersions: { ...draftExpected },
                baseEntities: structuredClone(draftEntities),
                baseFields: structuredClone(draftFields),
                baseEntityGenerations: { ...draftGenerations },
            },
        };
    }
    function prepare() {
        let draft: ReturnType<typeof prepareCommit>;
        try {
            draft = prepareCommit();
        } catch (error) {
            if (!disposed) editor.onDraftConversion?.(error);
            throw error;
        }
        editor.onDraftConversion?.();
        return draft;
    }
    return {
        readDraft: () => sharedDraft(session.state.projected, editor.read()),
        hasLocalChanges() {
            const base = session.state.projected;
            const target = sharedDraft(base, editor.read());
            if (sameCanvasJsonValue(base, target)) return false;
            // Hydrated defaults (e.g. an absent scene list becoming []) may
            // differ structurally without producing an authored command.
            try {
                return canvasEditorCommands(base, target, capabilities).length > 0;
            } catch {
                // Unsupported edits still require a visible failure and backup.
                return true;
            }
        },
        prepareRetry() {
            const draft = prepare();
            return {
                ...draft,
                operations: mergeRejectedCanvasEdits([...session.state.pending.flatMap((p) => p.operations), ...draft.operations]),
                preconditions: {
                    expectedEntityVersions: { ...draft.preconditions.expectedEntityVersions, ...session.state.pending[0]?.expectedEntityVersions },
                    preview: canvasDocumentChanges(session.state.acknowledged!.document, draft.document),
                },
            };
        },
        async commit() {
            if (disposed) return Promise.reject(new DOMException("画布编辑器已关闭", "AbortError"));
            const draft = prepare();
            return draft.operations.length ? session.submit(draft.operations, draft.preconditions) : Promise.resolve(undefined);
        },
        dispose() {
            disposed = true;
            unsubscribe();
        },
    };
}
