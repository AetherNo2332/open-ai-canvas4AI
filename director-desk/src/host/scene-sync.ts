import { readSceneDocument, type SceneDocument } from "../scenes/sequence-project.ts";
import type { HostBridge } from "./bridge";

/**
 * 嵌入模式下的场景同步(应用侧):
 * - start():注册 host.scene.load 处理器后发 desk.ready(宿主据此下发场景);
 * - host.scene.load:用应用自己的 readSceneDocument 校验/升级为 v3 后载入;
 * - notifyChanged():编辑后节流回传 desk.scene.changed。
 *
 * 事实源在宿主(PrevisScene):宿主场景载入之前不回传任何文档,避免把应用自带的示例场景
 * 写回宿主;与上次已同步内容相同的文档不回传(宿主下发引起的刷新不回声)。
 */
export interface SceneSyncOptions {
    bridge: HostBridge;
    /** 当前可回传的文档;编辑/拖动/导出进行中返回 null(结束后再回传)。 */
    readDocument: () => unknown;
    applyDocument: (document: SceneDocument) => void;
    onLoadError?: (error: unknown) => void;
    /** 节流调度(测试注入);返回取消函数。 */
    schedule?: (callback: () => void) => () => void;
    delayMs?: number;
}

export interface SceneSync {
    start(): void;
    notifyChanged(): void;
    dispose(): void;
}

const DEFAULT_DELAY_MS = 300;

export function createSceneSync(options: SceneSyncOptions): SceneSync {
    const delayMs = options.delayMs ?? DEFAULT_DELAY_MS;
    const schedule = options.schedule ?? ((callback: () => void) => {
        const timer = setTimeout(callback, delayMs);
        return () => clearTimeout(timer);
    });
    let hostSceneLoaded = false;
    let lastSynced: string | undefined;
    let cancelPending: (() => void) | null = null;
    let unsubscribe: (() => void) | null = null;
    let disposed = false;

    const snapshot = (): string | undefined => {
        const document = options.readDocument();
        return document == null ? undefined : JSON.stringify(document);
    };

    const flush = () => {
        cancelPending = null;
        if (disposed || !hostSceneLoaded) return;
        const json = snapshot();
        if (json === undefined || json === lastSynced) return;
        lastSynced = json;
        options.bridge.notify("desk.scene.changed", JSON.parse(json));
    };

    const load = (payload: unknown) => {
        if (disposed) return;
        const document = payload && typeof payload === "object" ? (payload as { document?: unknown }).document : undefined;
        let parsed: SceneDocument;
        try {
            parsed = readSceneDocument(document);
            options.applyDocument(parsed);
        } catch (error) {
            options.onLoadError?.(error);
            return;
        }
        hostSceneLoaded = true;
        // 载入后的应用文档即与宿主一致的基线,随后的同内容 changed() 不回传。
        lastSynced = snapshot();
    };

    return {
        start() {
            if (disposed || unsubscribe) return;
            unsubscribe = options.bridge.on("host.scene.load", load);
            options.bridge.notify("desk.ready");
        },
        notifyChanged() {
            if (disposed || cancelPending) return;
            cancelPending = schedule(flush);
        },
        dispose() {
            if (disposed) return;
            disposed = true;
            cancelPending?.();
            cancelPending = null;
            unsubscribe?.();
            unsubscribe = null;
        },
    };
}
