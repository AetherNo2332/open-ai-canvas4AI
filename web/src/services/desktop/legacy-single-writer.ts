// Temporary single-machine CAS adapter. B4 removes this module when commands
// become authoritative. It must never start for LAN or MCP sessions.
import { sameCanvasContent } from "@/lib/canvas/canvas-content";
import { getActiveUserScope } from "@/lib/user-scope";
import { getRemoteUserDataSnapshot } from "@/services/api/user-data";
import { initializeRemoteUserDataSession, saveRemoteUserDataNow } from "@/services/user-data-sync";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";
import { useAssetStore } from "@/stores/use-asset-store";
import { useSyncProgressStore } from "@/stores/use-sync-progress-store";

let disposers: (() => void)[] = [];
let timer: ReturnType<typeof setTimeout> | undefined;
let pending: Promise<void> | undefined;

declare global {
    interface Window {
        __infiniteCanvasFlush?: () => Promise<boolean>;
    }
}

export async function flushLegacySingleWriter(): Promise<boolean> {
    if (timer) clearTimeout(timer);
    timer = undefined;
    if (pending) await pending;
    for (let i = 0; i < 3; i++) {
        await saveRemoteUserDataNow();
        if (!useSyncProgressStore.getState().isAnySyncing()) return true;
    }
    return false;
}

export async function initializeLegacySingleWriter(userId: string) {
    const scope = getActiveUserScope();
    const snapshot = await getRemoteUserDataSnapshot();
    if (getActiveUserScope() !== scope) throw new DOMException("host changed", "AbortError");
    // DB is authoritative. IndexedDB only retains local presentation caches.
    const local = new Map(useCanvasStore.getState().projects.map((p) => [p.id, p]));
    useCanvasStore.setState({ projects: snapshot.projects.map((p) => ({ ...p, viewport: local.get(p.id)?.viewport || p.viewport || { x: 0, y: 0, k: 1 } })), hydrated: true });
    useAssetStore.setState({ assets: snapshot.assets, hydrated: true });
    await initializeRemoteUserDataSession(userId);
    window.__infiniteCanvasFlush = flushLegacySingleWriter;
    const schedule = () => {
        if (timer) clearTimeout(timer);
        timer = setTimeout(() => {
            timer = undefined;
            if (getActiveUserScope() !== scope) return;
            pending = saveRemoteUserDataNow();
            void pending
                .catch((error) => console.warn("本地画布保存失败", error))
                .finally(() => {
                    pending = undefined;
                });
        }, 400);
    };
    disposers.push(
        useCanvasStore.subscribe((state, previous) => {
            if (state.projects === previous.projects) return;
            const before = new Map(previous.projects.map((p) => [p.id, p]));
            const changed = state.projects.filter((p) => !sameCanvasContent(before.get(p.id), p));
            if (!changed.length) return;
            for (const project of changed) useSyncProgressStore.getState().setProjectProgress(project.id, { phase: "pending", message: "等待保存到本地主机" });
            schedule();
        }),
        useAssetStore.subscribe((state, previous) => {
            if (state.assets !== previous.assets) schedule();
        }),
    );
}

export async function stopLegacySingleWriter() {
    disposers.forEach((dispose) => dispose());
    disposers = [];
    if (timer) clearTimeout(timer);
    timer = undefined;
    if (pending) await pending.catch(() => {});
    delete window.__infiniteCanvasFlush;
}
