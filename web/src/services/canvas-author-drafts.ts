import { localForageStorageForScope } from "@/lib/localforage-storage";
import localforage from "localforage";
import { scopedStorageKey } from "@/lib/user-scope";
import { useCanvasSessionStore } from "@/stores/canvas/use-canvas-session-store";

export type CanvasAuthorIdentity = { documentEpoch: string; nodeGeneration: number };
export type CanvasAuthorDraft = { version: 1; kind: "text" | "prompt"; identity?: CanvasAuthorIdentity; base: Record<string, unknown>; value: Record<string, unknown>; savedAt: string };
export function captureCanvasAuthorIdentity(canvasId: string, nodeId: string): CanvasAuthorIdentity | undefined {
    const base = useCanvasSessionStore.getState().states[canvasId]?.acknowledged;
    const nodeGeneration = base?.entityGenerations?.[`node:${nodeId}`];
    return base?.documentEpoch && typeof nodeGeneration === "number" && Number.isSafeInteger(nodeGeneration) ? { documentEpoch: base.documentEpoch, nodeGeneration } : undefined;
}
export function sameCanvasAuthorIdentity(left: CanvasAuthorIdentity | undefined, right: CanvasAuthorIdentity | undefined) {
    return Boolean(left && right && left.documentEpoch === right.documentEpoch && left.nodeGeneration === right.nodeGeneration);
}
const key = (canvasId: string, nodeId: string, kind: CanvasAuthorDraft["kind"]) => `infinite-canvas:author-draft:${encodeURIComponent(canvasId)}:${encodeURIComponent(nodeId)}:${kind}`;
const tails = new Map<string, Promise<unknown>>();
function serial<T>(scope: string, draftKey: string, fn: () => Promise<T>) {
    const id = `${scope}:${draftKey}`;
    const pending = (tails.get(id) || Promise.resolve()).catch(() => {}).then(fn);
    tails.set(id, pending);
    void pending
        .finally(() => {
            if (tails.get(id) === pending) tails.delete(id);
        })
        .catch(() => {});
    return pending;
}
export async function readCanvasAuthorDraft(scope: string, canvasId: string, nodeId: string, kind: CanvasAuthorDraft["kind"]): Promise<CanvasAuthorDraft | null> {
    const raw = await localForageStorageForScope(scope).getItem(key(canvasId, nodeId, kind));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as CanvasAuthorDraft;
    if (parsed.version !== 1 || parsed.kind !== kind || !parsed.base || !parsed.value) throw new Error("本地编辑草稿格式无效，请先导出当前内容");
    return parsed;
}
export function saveCanvasAuthorDraft(scope: string, canvasId: string, nodeId: string, draft: Omit<CanvasAuthorDraft, "version" | "savedAt">) {
    const draftKey = key(canvasId, nodeId, draft.kind);
    return serial(scope, draftKey, async () => {
        const old = await readCanvasAuthorDraft(scope, canvasId, nodeId, draft.kind);
        const value: CanvasAuthorDraft = { ...draft, identity: old ? old.identity : draft.identity, base: old?.base || draft.base, version: 1, savedAt: new Date().toISOString() };
        await localforage.setItem(scopedStorageKey(draftKey, scope), JSON.stringify(value));
    });
}
export function clearCanvasAuthorDraft(scope: string, canvasId: string, nodeId: string, kind: CanvasAuthorDraft["kind"]) {
    const draftKey = key(canvasId, nodeId, kind);
    return serial(scope, draftKey, () => localforage.removeItem(scopedStorageKey(draftKey, scope)));
}
export async function flushCanvasAuthorDrafts() {
    await Promise.all([...tails.values()]);
}
