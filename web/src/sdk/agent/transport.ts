import type { TransportRequest, TransportResponse } from "../creation-platform/transport";

export type AgentTransportRequest = Omit<TransportRequest, "method"> & { method: TransportRequest["method"] | "DELETE" | "PATCH" };
export type AgentTransport = {
    request(call: AgentTransportRequest): Promise<TransportResponse & { retryAfterMs?: number }>;
    openEvents(call: AgentTransportRequest): Promise<Response>;
};

/** Inject fetch and credentials for Node; browsers use the app HTTP adapter. */
export function createAgentFetchTransport(options: { baseUrl: string; fetch?: typeof fetch; headers?: () => Record<string, string> }): AgentTransport {
    const send = (call: AgentTransportRequest) =>
        (options.fetch ?? globalThis.fetch)(options.baseUrl.replace(/\/+$/, "") + call.path, {
            method: call.method,
            credentials: "include",
            signal: call.signal,
            headers: { ...(call.body === undefined ? {} : { "Content-Type": "application/json" }), ...options.headers?.(), ...call.headers },
            body: call.body === undefined ? undefined : JSON.stringify(call.body),
        });
    return {
        openEvents: send,
        async request(call) {
            const response = await send(call);
            const retryAfter = response.headers.get("retry-after");
            const retryAfterMs = retryAfter ? (/^\d+(\.\d+)?$/.test(retryAfter) ? Number(retryAfter) * 1000 : Math.max(0, Date.parse(retryAfter) - Date.now())) : undefined;
            return { status: response.status, body: (await response.json()) as TransportResponse["body"], retryAfterMs };
        },
    };
}
