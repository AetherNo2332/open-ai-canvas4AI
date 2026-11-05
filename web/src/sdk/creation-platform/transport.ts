/**
 * 创作平台 SDK 的传输边界。SDK 核心只依赖这个接口：认证、Base URL 与拦截器由注入方负责，
 * 浏览器里用 http-transport.ts（基于既有 http.raw），Node 或测试里可直接传入 fetch 实现。
 */

export type TransportRequest = {
    method: "GET" | "POST";
    /** 相对后端 API 根的路径，例如 /platform/v1/generations?x=1。 */
    path: string;
    /** JSON 对象或 FormData；FormData 不设 Content-Type，由运行时生成 boundary。 */
    body?: unknown;
    headers?: Record<string, string>;
    signal?: AbortSignal;
};

/** 后端统一信封；HTTP 状态与业务 code 分别给出。 */
export type TransportResponse = {
    status: number;
    body: { code: number; data: unknown; msg?: string; reason?: string; details?: Record<string, unknown> };
};

export type Transport = {
    /** 网络层失败（未拿到 HTTP 响应）抛出任意错误；拿到响应时返回信封，不论状态码。取消时抛出 AbortError。 */
    request(call: TransportRequest): Promise<TransportResponse>;
};

/** 以 fetch 实现的传输，适用于 Node、Worker 与测试；baseUrl 例如 http://127.0.0.1:8080/api。 */
export function createFetchTransport(options: { baseUrl: string; fetch?: typeof fetch; headers?: () => Record<string, string> }): Transport {
    const fetcher = options.fetch ?? fetch;
    return {
        async request(call) {
            const headers: Record<string, string> = { ...(options.headers?.() ?? {}), ...(call.headers ?? {}) };
            let body: BodyInit | undefined;
            if (call.body instanceof FormData) body = call.body;
            else if (call.body !== undefined) {
                headers["Content-Type"] = "application/json";
                body = JSON.stringify(call.body);
            }
            const response = await fetcher(options.baseUrl.replace(/\/$/, "") + call.path, { method: call.method, headers, body, signal: call.signal, credentials: "include" });
            const text = await response.text();
            let parsed: TransportResponse["body"];
            try {
                parsed = JSON.parse(text) as TransportResponse["body"];
            } catch {
                parsed = { code: response.status || 500, data: null, msg: text.slice(0, 200) || response.statusText };
            }
            return { status: response.status, body: parsed };
        },
    };
}
