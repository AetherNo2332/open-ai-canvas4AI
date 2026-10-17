import { create } from "zustand";
import localforage from "localforage";
import { createCanvasCommandTransport, readCanvasCapabilities } from "../../services/api/canvas-commands";
import { captureHTTP } from "../../services/api/request";
import type { CanvasCapabilities } from "../../lib/canvas/canvas-editor-commands";
import { createCanvasCommandJournal, createCanvasPersonalViewStorage, readCanvasClientId, nativeCommandJournalStorage, type NativeCommandJournal } from "../../services/canvas-command-journal";
import { createCanvasCommandWorkspace, type CanvasCommandWorkspace } from "../../services/canvas-command-workspace";
import type { CanvasCommandSession, CommandSessionState } from "../../services/canvas-command-session";
import { createCanvasCommandCreator } from "../../services/canvas-command-create";
import { canvasEditorCommands } from "../../lib/canvas/canvas-editor-commands";
import { useCanvasStore, withCanvasStorePersistenceSuppressed, type CanvasProject } from "./use-canvas-store";
import type { Snapshot } from "../../lib/canvas/command-contract.generated";

type SessionStore = { scope: string | null; capabilities: CanvasCapabilities | null; states: Record<string, CommandSessionState> };
export const useCanvasSessionStore = create<SessionStore>(() => ({ scope: null, capabilities: null, states: {} }));
let workspace: CanvasCommandWorkspace | null = null;
let creator: ReturnType<typeof createCanvasCommandCreator> | null = null;
let personalView: ReturnType<typeof createCanvasPersonalViewStorage> | null = null;
export function canvasPersonalViewStorage() {
    if (!personalView) throw new Error("canvas_command_workspace_unavailable");
    return personalView;
}
let generation = 0;
const bindings = new Map<string, () => void>();
const observers = new Map<string, Set<(state: CommandSessionState) => void>>();
const editors = new Map<string, { commit: () => Promise<unknown>; flush?: () => Promise<boolean>; dispose: () => void }>();
export function registerCanvasCommandEditor(canvasId: string, editor: { commit: () => Promise<unknown>; flush?: () => Promise<boolean>; dispose: () => void }) {
    editors.get(canvasId)?.dispose();
    editors.set(canvasId, editor);
    return () => {
        if (editors.get(canvasId) === editor) editors.delete(canvasId);
        editor.dispose();
    };
}
export function commitCanvasCommandEditor(canvasId: string) {
    return editors.get(canvasId)?.commit() || Promise.resolve();
}

/** Preparation only; the desktop bootstrap activates this after all editor
 * callbacks and task consumers have moved to commands in the same change. */
export async function connectCanvasCommandWorkspace(scope: string, boundClientId?: string) {
    const current = ++generation;
    const previous = workspace;
    creator?.close();
    creator = null;
    workspace = null;
    personalView = null;
    for (const editor of editors.values()) editor.dispose();
    editors.clear();
    for (const dispose of bindings.values()) dispose();
    bindings.clear();
    observers.clear();
    useCanvasSessionStore.setState({ scope: null, capabilities: null, states: {} });
    await previous?.close();
    const api = captureHTTP();
    const transport = createCanvasCommandTransport(api);
    const capabilities = await readCanvasCapabilities(api);
    if (capabilities.protocolVersion !== 1 || !Array.isArray(capabilities.nodes) || !capabilities.structuredFields) throw new Error("canvas_capability_contract_invalid");
    const native = typeof window !== "undefined" ? (window as Window & { infiniteCanvasDesktop?: { commandJournal?: NativeCommandJournal } }).infiniteCanvasDesktop?.commandJournal : undefined;
    const journalStorage = native ? nativeCommandJournalStorage(native) : localforage;
    const clientId = boundClientId || (await readCanvasClientId(journalStorage, scope));
    if (current !== generation) throw new DOMException("画布主机已切换", "AbortError");
    workspace = createCanvasCommandWorkspace({ transport, clientId, journal: createCanvasCommandJournal(journalStorage, scope) });
    personalView = createCanvasPersonalViewStorage(journalStorage, scope);
    creator = createCanvasCommandCreator({
        scope,
        clientId,
        storage: journalStorage,
        create: (request) => api.post<Snapshot>("/desktop/canvases", request),
        onCreated: (snapshot) => {
            if (current !== generation) return;
            const document = snapshot.document;
            const project: CanvasProject = {
                id: snapshot.canvasId,
                title: "未命名画布",
                createdAt: new Date().toISOString(),
                updatedAt: new Date().toISOString(),
                nodes: [],
                connections: [],
                chatSessions: [],
                activeChatId: null,
                backgroundMode: "blank",
                showImageInfo: false,
                viewport: { x: 0, y: 0, k: 1 },
                previsScenes: [],
                ...(document as Partial<CanvasProject>),
                revision: snapshot.revision,
            };
            withCanvasStorePersistenceSuppressed(() => useCanvasStore.setState((state) => ({ projects: [project, ...state.projects.filter((item) => item.id !== project.id)] })));
        },
    });
    await creator.recover();
    if (current !== generation) throw new DOMException("画布主机已切换", "AbortError");
    useCanvasSessionStore.setState({ scope, capabilities, states: {} });
}
export async function createCanvasCommandProject(title: string, projectId?: string, initialContent?: Partial<Pick<CanvasProject, "nodes" | "connections" | "chatSessions" | "activeChatId">>) {
    const current = creator,
        capabilities = useCanvasSessionStore.getState().capabilities;
    if (!current || !capabilities) throw new Error("canvas_command_workspace_unavailable");
    const base = { id: "initial", nodes: [], connections: [] };
    const initialOperations = initialContent ? canvasEditorCommands(base, { ...base, nodes: initialContent.nodes || [], connections: initialContent.connections || [] }, capabilities) : [];
    const result = await current.create({ title, ...(projectId ? { projectId } : {}), ...(initialOperations.length ? { initialOperations } : {}) });
    if (current !== creator) throw new DOMException("画布主机已切换", "AbortError");
    return { id: result.canvasId };
}
export async function openCanvasCommandSession(canvasId: string): Promise<CanvasCommandSession> {
    const current = workspace;
    if (!current) throw new Error("canvas_command_workspace_unavailable");
    const session = await current.open(canvasId);
    if (current !== workspace) throw new DOMException("画布主机已切换", "AbortError");
    if (!bindings.has(canvasId))
        bindings.set(
            canvasId,
            session.subscribe((state) => {
                if (current !== workspace) return;
                // Editor observers first update live refs and then their React state.
                for (const observer of observers.get(canvasId) || []) observer(state);
                useCanvasSessionStore.setState((previous) => ({ states: { ...previous.states, [canvasId]: state } }));
            }),
        );
    return session;
}
export function canvasCommandSession(canvasId: string) {
    return workspace?.get(canvasId);
}
export function subscribeCanvasCommandSession(canvasId: string, listener: (state: CommandSessionState) => void) {
    const listeners = observers.get(canvasId) || new Set();
    observers.set(canvasId, listeners);
    listeners.add(listener);
    const current = workspace?.get(canvasId);
    if (current) listener(current.state);
    return () => {
        listeners.delete(listener);
        if (!listeners.size) observers.delete(canvasId);
    };
}
export async function flushCanvasCommandWorkspace() {
    const current = workspace;
    if (!current) return false;
    const editorsSaved = await Promise.all([...editors.values()].map((editor) => (editor.flush ? editor.flush() : editor.commit().then(() => true))));
    return current === workspace && editorsSaved.every(Boolean) && (await current.flush());
}
export async function disconnectCanvasCommandWorkspace() {
    generation++;
    creator?.close();
    creator = null;
    const previous = workspace;
    workspace = null;
    personalView = null;
    for (const editor of editors.values()) editor.dispose();
    editors.clear();
    for (const dispose of bindings.values()) dispose();
    bindings.clear();
    observers.clear();
    await previous?.close();
    useCanvasSessionStore.setState({ scope: null, capabilities: null, states: {} });
}
