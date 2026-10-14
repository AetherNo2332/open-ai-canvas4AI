import { PROTOCOL_VERSION, REQUEST_RESPONSE, REQUEST_TIMEOUT_MS, isBridgeMessage, type BridgeMessage } from "./contract";

/**
 * host bridge 的收发实现，两端(宿主 side / 应用 side)共用。
 *
 * - `notify` 通知(无 id)；
 * - `request` 请求(带 id)，按 `REQUEST_RESPONSE` 映射等待应答，超时以错误拒绝；
 * - `on` 注册处理器；带 id 的入站消息处理后按映射回发应答；
 * - 来源不匹配、协议版本不符、未知类型一律忽略，不抛异常。
 */
export interface BridgeEndpoint {
    postMessage(message: unknown, targetOrigin?: string): void;
    addEventListener(type: "message", listener: (event: { data: unknown; origin: string }) => void): void;
    removeEventListener(type: "message", listener: (event: { data: unknown; origin: string }) => void): void;
}

export interface HostBridgeOptions {
    /** 只接受该来源的消息(同源部署时即工作台来源)；缺省不校验来源。 */
    origin?: string;
    /** postMessage 的目标来源。 */
    targetOrigin?: string;
}

export type BridgeHandler = (payload: unknown) => unknown | Promise<unknown>;

export interface HostBridge {
    notify(type: string, payload?: unknown): void;
    request(type: string, payload?: unknown, timeoutMs?: number): Promise<unknown>;
    on(type: string, handler: BridgeHandler): () => void;
    dispose(): void;
}

interface PendingRequest {
    resolve: (value: unknown) => void;
    reject: (error: Error) => void;
    timer: ReturnType<typeof setTimeout>;
}

let sequence = 0;

export function createHostBridge(endpoint: BridgeEndpoint, options: HostBridgeOptions = {}): HostBridge {
    const handlers = new Map<string, Set<BridgeHandler>>();
    const pending = new Map<string, PendingRequest>();
    let disposed = false;

    const post = (message: BridgeMessage) => {
        if (disposed) return;
        endpoint.postMessage(message, options.targetOrigin);
    };

    const listener = (event: { data: unknown; origin: string }) => {
        if (disposed) return;
        if (options.origin && event.origin !== options.origin) return;
        if (!isBridgeMessage(event.data)) return;
        const message = event.data;
        const waiting = message.id ? pending.get(message.id) : undefined;
        if (waiting && message.id) {
            clearTimeout(waiting.timer);
            pending.delete(message.id);
            if (message.error) waiting.reject(new Error(message.error));
            else waiting.resolve(message.payload);
            return;
        }
        const registered = handlers.get(message.type);
        if (!registered || registered.size === 0) return;
        void (async () => {
            try {
                let result: unknown;
                for (const handler of [...registered]) result = await handler(message.payload);
                if (message.id && !disposed) {
                    post({ protocol: PROTOCOL_VERSION, id: message.id, type: REQUEST_RESPONSE[message.type] ?? message.type, payload: result });
                }
            } catch (error) {
                if (message.id && !disposed) post({ protocol: PROTOCOL_VERSION, id: message.id, type: REQUEST_RESPONSE[message.type] ?? message.type, error: error instanceof Error ? error.message : "bridge_request_failed" });
            }
        })();
    };

    endpoint.addEventListener("message", listener);

    if (disposed) throw new Error("bridge_disposed");

    return {
        notify(type, payload) {
            post({ protocol: PROTOCOL_VERSION, type, payload });
        },
        request(type, payload, timeoutMs = REQUEST_TIMEOUT_MS) {
            if (disposed) return Promise.reject(new Error("bridge_disposed"));
            const id = `bridge-${++sequence}`;
            return new Promise<unknown>((resolve, reject) => {
                const timer = setTimeout(() => {
                    pending.delete(id);
                    reject(new Error(`bridge_timeout:${type}`));
                }, timeoutMs);
                pending.set(id, { resolve, reject, timer });
                post({ protocol: PROTOCOL_VERSION, id, type, payload });
            });
        },
        on(type, handler) {
            const set = handlers.get(type) ?? new Set<BridgeHandler>();
            set.add(handler);
            handlers.set(type, set);
            return () => {
                set.delete(handler);
                if (set.size === 0) handlers.delete(type);
            };
        },
        dispose() {
            if (disposed) return;
            disposed = true;
            endpoint.removeEventListener("message", listener);
            for (const [id, entry] of pending) {
                clearTimeout(entry.timer);
                entry.reject(new Error("bridge_disposed"));
                pending.delete(id);
            }
            handlers.clear();
        },
    };
}
