import { getActiveUserScope } from "@/lib/user-scope";
import { canvasContentHash } from "@/lib/canvas/canvas-content";
import { canvasEditorCommands } from "@/lib/canvas/canvas-editor-commands";
import { canvasPersistentEditorDraft } from "@/lib/canvas/canvas-editor-transient";
import { preserveCanvasSyncDraft } from "@/services/canvas-sync-drafts";
import { hasRemoteUserDataSyncSession, subscribeRemoteUserDataReady } from "@/services/user-data-sync";
import { connectCanvasCommandWorkspace, useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";
import { flushCanvasStorePersistence, useCanvasStore, type CanvasProject } from "@/stores/canvas/use-canvas-store";
import { useSyncProgressStore } from "@/stores/use-sync-progress-store";
import type { CanvasCommandSession } from "./canvas-command-session";

let active: { scope: string; promise: Promise<void> } | undefined;
let installed = false;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
let generation = 0;

function assertScope(scope: string) {
    if (getActiveUserScope() !== scope) throw new DOMException("账号已切换", "AbortError");
    if (!hasRemoteUserDataSyncSession()) throw new Error("云端数据基线尚未建立，画布命令尚未启用");
}

/** Archive unverifiable legacy cache before a command snapshot can replace it. */
export async function ensureOrdinaryCanvasCommandWorkspace(scope = getActiveUserScope()) {
    assertScope(scope);
    if (useCanvasSessionStore.getState().scope === scope) return;
    if (active?.scope === scope) return active.promise;
    const current = generation;
    const check = () => {
        if (current !== generation) throw new DOMException("画布主机已切换", "AbortError");
        assertScope(scope);
    };
    useCanvasSessionStore.setState({ bootstrapState: "connecting", bootstrapError: undefined });
    const promise = (async () => {
        await flushCanvasStorePersistence();
        for (const project of [...useCanvasStore.getState().projects]) {
            check();
            if (!project.remoteContentHash || project.remoteContentHash !== (await canvasContentHash(project))) {
                const draftCount = await preserveCanvasSyncDraft(project, scope);
                useSyncProgressStore.getState().setProjectProgress(project.id, { draftCount });
            }
        }
        check();
        await connectCanvasCommandWorkspace(scope);
        check();
    })();
    const request = { scope, promise };
    active = request;
    try {
        await promise;
    } catch (error) {
        if (current === generation && scope === getActiveUserScope()) useCanvasSessionStore.setState({ bootstrapState: "failed", bootstrapError: error instanceof Error ? error.message : "画布命令暂不可用" });
        throw error;
    } finally {
        if (active === request) active = undefined;
    }
}

export function cancelCanvasCommandBootstrap() {
    generation += 1;
    if (retryTimer) clearTimeout(retryTimer);
    retryTimer = undefined;
    active = undefined;
}

export function installCanvasCommandBootstrap() {
    if (installed) return;
    installed = true;
    const connect = async () => {
        const scope = getActiveUserScope();
        try {
            await ensureOrdinaryCanvasCommandWorkspace(scope);
        } catch (error) {
            if (scope !== getActiveUserScope() || !hasRemoteUserDataSyncSession() || (error instanceof DOMException && error.name === "AbortError")) return;
            if (retryTimer) clearTimeout(retryTimer);
            retryTimer = setTimeout(() => {
                retryTimer = undefined;
                void connect();
            }, 2000);
        }
    };
    subscribeRemoteUserDataReady(connect);
    if (typeof window !== "undefined")
        window.addEventListener("online", () => {
            if (hasRemoteUserDataSyncSession()) void connect();
        });
}

/** Existing unknown wires take precedence over any legacy cache projection. */
export async function migrateCachedCanvasIntoSession(session: CanvasCommandSession, cached: CanvasProject | undefined, hasJournal: boolean, scope: string) {
    assertScope(scope);
    const snapshot = session.state.acknowledged;
    const capabilities = useCanvasSessionStore.getState().capabilities;
    if (!cached || !snapshot || !capabilities || hasJournal) return;
    const cachedEpoch = (cached as CanvasProject & { documentEpoch?: unknown }).documentEpoch;
    // Revision can restart at 1 when an ID is deleted and recreated. Never
    // attach a freshly read epoch/baseline to an old, unverified cache intent.
    const sameIncarnation = typeof cachedEpoch === "string" ? cachedEpoch === snapshot.documentEpoch : Boolean(cached.remoteContentHash && cached.remoteContentHash === (await canvasContentHash(snapshot.document as CanvasProject)));
    if (cached.revision !== snapshot.revision || !sameIncarnation) {
        const draftCount = await preserveCanvasSyncDraft(cached, scope);
        useSyncProgressStore.getState().setProjectProgress(cached.id, { draftCount, message: "已加载云端版本；旧缓存基线无法确认，已保留为草稿，可下载后恢复副本" });
        return;
    }
    // Legacy cache also contains chat/session/view state. Only shared document
    // fields enter commands; the complete cache remains recoverable in drafts.
    const shared = { ...snapshot.document };
    for (const key of ["title", "nodes", "connections", "appearance", "backgroundMode", "showImageInfo", "starterMode", "timeline", "previsScenes"] as const) if (Object.hasOwn(cached, key)) shared[key] = structuredClone(cached[key]);
    const operations = canvasEditorCommands(snapshot.document, canvasPersistentEditorDraft(snapshot.document, shared), capabilities);
    if (!operations.length) return;
    await preserveCanvasSyncDraft(cached, scope);
    assertScope(scope);
    for (let offset = 0; offset < operations.length; offset += 100) {
        void session.submit(operations.slice(offset, offset + 100)).catch((error) => {
            if (scope !== getActiveUserScope()) return;
            useSyncProgressStore.getState().setProjectProgress(cached.id, { phase: "conflict", message: error instanceof Error ? error.message : "旧本地编辑已保留，请处理提交冲突" });
        });
    }
}
