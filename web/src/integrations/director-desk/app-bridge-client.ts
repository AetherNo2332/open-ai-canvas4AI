import { createHostBridge, type BridgeEndpoint } from "@desk/host/bridge";
import type { ExportResultPublisher } from "./export-handler";

/**
 * 宿主(工作台)侧的导演台 bridge 客户端。
 *
 * 职责边界:
 * - 只在 host bridge 上交换消息,不直接读写 iframe 内部状态;
 * - 应用就绪(`desk.ready`)后下发当前场景文档;应用文档变更(`desk.scene.changed`)
 *   交给调用方提交——工作台把它送进既有的 previs 保存协调器(草稿/CAS/flush);
 * - 应用的保存请求(`desk.save.request`)按契约映射应答 `host.save.result`。
 */
export type DirectorDeskSaveResult = { ok: boolean; message?: string };

export type DirectorDeskHostBridgeOptions = {
    canvasId?: string;
    /** 宿主窗口:监听本窗口消息、向 iframe 投递(由 frame 组件构造,测试可注入)。 */
    endpoint: BridgeEndpoint;
    /** 只接受该来源的消息(同源部署下即工作台来源)。 */
    origin?: string;
    /** 读取当前应下发给应用的场景文档(适配后的 DirectorDesk 文档)。 */
    readDocument: () => unknown;
    /** 应用侧文档变更:交给宿主的保存协调器路径,不得绕过 CAS。 */
    commitDocument: (document: unknown) => void | Promise<void>;
    /** 应用显式保存请求:返回是否真正落盘。 */
    requestSave: () => Promise<DirectorDeskSaveResult>;
    /** 应用导出请求:由宿主提交后台任务或更新画布,不得在 iframe 内落本地文件。 */
    onExportRequest?: (payload: unknown, publish: ExportResultPublisher) => unknown | Promise<unknown>;
    onReady?: () => void;
};

export type DirectorDeskHostBridge = {
    /** 主动把当前场景文档推给应用(如 Agent 更新场景后)。 */
    pushScene: (document?: unknown, options?: { preserveViewState?: boolean; viewState?: unknown; revision?: string; source?: string }) => void;
    publishExportResult: (result: unknown) => void;
    dispose: () => void;
};

export function createDirectorDeskHostBridge(options: DirectorDeskHostBridgeOptions): DirectorDeskHostBridge {
    const bridge = createHostBridge(options.endpoint, { origin: options.origin, targetOrigin: options.origin });

    const pushScene = (document = options.readDocument(), metadata: { preserveViewState?: boolean; viewState?: unknown; revision?: string; source?: string } = {}) => {
        bridge.notify("host.scene.load", { canvasId: options.canvasId, document, ...metadata });
    };

    bridge.on("desk.ready", () => {
        options.onReady?.();
        pushScene();
    });
    bridge.on("desk.scene.changed", (payload) => options.commitDocument(payload));
    bridge.on("desk.save.request", () => options.requestSave());
    bridge.on("desk.export.request", (payload) => {
        if (!options.onExportRequest) throw new Error("director_export_unavailable");
        return options.onExportRequest(payload, (result) => bridge.notify("host.export.result", result));
    });

    return {
        pushScene,
        publishExportResult: (result) => bridge.notify("host.export.result", result),
        dispose: () => bridge.dispose(),
    };
}
