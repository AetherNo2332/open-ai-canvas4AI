import { apiBaseURL, http } from "@/services/api/request";
import type { AgentTransport } from "./transport";

export const agentWebTransport: AgentTransport = {
    async request(call) {
        const response = await http.raw({
            method: call.method,
            url: call.path,
            data: call.body,
            headers: call.headers,
            signal: call.signal,
            timeout: 15_000,
            validateStatus: () => true,
        });
        const header = response.headers["retry-after"];
        const retryAfterMs = header === undefined ? undefined : /^\d+(\.\d+)?$/.test(String(header)) ? Number(header) * 1000 : Math.max(0, Date.parse(String(header)) - Date.now());
        return { status: response.status, body: response.data as { code: number; data: unknown; msg?: string; reason?: string }, retryAfterMs };
    },
    openEvents: (call) =>
        fetch(`${String(apiBaseURL).replace(/\/+$/, "")}${call.path}`, {
            credentials: "include",
            signal: call.signal,
            headers: call.headers,
        }),
};
