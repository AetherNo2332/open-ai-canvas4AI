import type { AgentApprovalDecision, AgentBusinessScope, AgentConversation, AgentConversationPage, AgentConversationQueue, AgentEvent, AgentMessageInput, AgentMessageReceipt, AgentRunReceipt, AgentRunRequest, AgentRunSnapshot } from "./contract";
import type { AgentTransport, AgentTransportRequest } from "./transport";

export class AgentRequestError extends Error {
    constructor(
        readonly status: number,
        readonly code: number,
        readonly reason: string,
        message: string,
        readonly retryable: boolean,
        readonly retryAfterMs?: number,
        readonly details?: Record<string, unknown>,
    ) {
        super(message);
        this.name = "AgentRequestError";
    }
}

const runPath = (id: string) => `/agent/runs/${encodeURIComponent(id)}`;
const conversationPath = (id: string) => `/agent/conversations/${encodeURIComponent(id)}`;
const isTerminal = (run: AgentRunSnapshot) => !run.cleanupPending && ["completed", "failed", "cancelled", "rejected"].includes(run.status);

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
    signal?.throwIfAborted();
    return new Promise((resolve, reject) => {
        const abort = () => {
            clearTimeout(timer);
            reject(signal?.reason);
        };
        const timer = setTimeout(() => {
            signal?.removeEventListener("abort", abort);
            resolve();
        }, ms);
        signal?.addEventListener("abort", abort, { once: true });
    });
}

export function createAgentClient<R extends AgentRunSnapshot = AgentRunSnapshot>(options: { transport: AgentTransport; maxRetries?: number; retryDelayMs?: (attempt: number) => number; scope?: AgentBusinessScope; agentDefinitionId?: string }) {
    const retryDelay = options.retryDelayMs ?? ((attempt: number) => Math.min(10_000, 500 * 2 ** attempt));
    const maxRetries = options.maxRetries ?? 2;
    const scope = options.scope && { ...options.scope };
    const definitionId = options.agentDefinitionId;
    const scopedPath = (path: string) => {
        if (!scope) return path;
        const [pathname, search] = path.split("?", 2);
        const query = new URLSearchParams(search);
        query.set("scopeType", scope.type);
        query.set("scopeId", scope.id);
        if (definitionId) query.set("agentDefinitionId", definitionId);
        return `${pathname}?${query}`;
    };
    async function request<T>(call: AgentTransportRequest, idempotent = false): Promise<T> {
        // The caller may edit its composer while a timed-out request is retried.
        const frozen = { ...call, path: scopedPath(call.path), body: call.body === undefined ? undefined : JSON.parse(JSON.stringify(call.body)) };
        for (let attempt = 0; ; attempt++) {
            call.signal?.throwIfAborted();
            try {
                const { status, body, retryAfterMs } = await options.transport.request(frozen);
                if (status >= 200 && status < 300 && body.code === 0) return body.data as T;
                const envelope = body as typeof body & { retryable?: boolean };
                throw new AgentRequestError(status, body.code, body.reason ?? `http_${status}`, body.msg ?? "Agent 请求失败", envelope.retryable ?? (status >= 500 || [408, 425, 429].includes(status)), retryAfterMs, body.details);
            } catch (cause) {
                call.signal?.throwIfAborted();
                if ((cause as Error)?.name === "AbortError") throw cause;
                const metadata = cause as Partial<AgentRequestError> | null;
                const error =
                    cause instanceof AgentRequestError
                        ? cause
                        : new AgentRequestError(metadata?.status ?? 0, metadata?.code ?? 0, metadata?.reason ?? "transport", "Agent 网络请求失败", metadata?.retryable ?? true, metadata?.retryAfterMs, metadata?.details);
                if (!idempotent || !error.retryable || attempt >= maxRetries) throw error;
                const delay = Math.max(retryDelay(attempt), error.retryAfterMs ?? 0);
                if (delay > 10_000) throw error;
                await sleep(delay, call.signal);
            }
        }
    }
    async function readEvents(
        runId: string,
        onEvent: (event: AgentEvent) => void,
        opts: {
            after?: number;
            signal?: AbortSignal;
            onSnapshot?: (run: R) => void;
            idleTimeoutMs?: number;
        } = {},
    ): Promise<void> {
        let cursor = opts.after ?? 0;
        if (!Number.isSafeInteger(cursor) || cursor < 0) throw new RangeError("Invalid Agent event cursor");
        let failures = 0;
        while (!opts.signal?.aborted) {
            let terminal = false;
            let response: Response | undefined;
            let retryAfterMs = 0;
            const attempt = new AbortController();
            const signal = opts.signal ? AbortSignal.any([opts.signal, attempt.signal]) : attempt.signal;
            let watchdog: ReturnType<typeof setTimeout>;
            const touch = () => {
                clearTimeout(watchdog);
                watchdog = setTimeout(() => attempt.abort(), opts.idleTimeoutMs ?? 45_000);
            };
            touch();
            try {
                response = await options.transport.openEvents({
                    method: "GET",
                    path: scopedPath(`${runPath(runId)}/events?after=${cursor}`),
                    signal,
                    headers: { Accept: "text/event-stream", "Last-Event-ID": String(cursor) },
                });
                if (!response.ok) {
                    const header = response.headers.get("retry-after");
                    retryAfterMs = header ? (/^\d+(\.\d+)?$/.test(header) ? Number(header) * 1000 : Math.max(0, Date.parse(header) - Date.now())) || 0 : 0;
                    throw new AgentRequestError(response.status, response.status, `http_${response.status}`, "Agent 事件流不可用", response.status >= 500 || [408, 429].includes(response.status), retryAfterMs);
                }
                if (!response.body || !response.headers.get("content-type")?.includes("text/event-stream")) {
                    throw new AgentRequestError(502, 502, "agent_stream_invalid", "Agent 事件流格式无效", false);
                }
                const reader = response.body.getReader();
                touch();
                const decoder = new TextDecoder();
                let buffer = "",
                    eventType = "",
                    data: string[] = [];
                const dispatch = () => {
                    signal.throwIfAborted();
                    if (!data.length) {
                        eventType = "";
                        return;
                    }
                    const value = JSON.parse(data.join("\n"));
                    if (eventType === "run_snapshot") {
                        const run = value as R;
                        if (run?.id !== runId) throw new Error("Agent snapshot identity mismatch");
                        opts.onSnapshot?.(run);
                        terminal = isTerminal(run);
                    } else if (eventType === "error") {
                        throw new Error("Agent event read failed");
                    } else if (value.runId === runId && Number.isSafeInteger(value.seq) && value.seq > cursor) {
                        onEvent(value as AgentEvent);
                        cursor = value.seq;
                        failures = 0;
                    }
                    data = [];
                    eventType = "";
                };
                const abort = () => {
                    void reader.cancel().catch(() => undefined);
                };
                signal.addEventListener("abort", abort, { once: true });
                try {
                    while (!signal.aborted) {
                        const { value, done } = await reader.read();
                        if (value?.length) touch();
                        buffer += decoder.decode(value, { stream: !done });
                        // Process lines instead of normalizing chunks: CRLF may straddle chunks.
                        let end: number;
                        while ((end = buffer.indexOf("\n")) >= 0) {
                            const line = buffer.slice(0, end).replace(/\r$/, "");
                            buffer = buffer.slice(end + 1);
                            if (!line) dispatch();
                            else if (line.startsWith("event:")) eventType = line.slice(6).trim();
                            else if (line.startsWith("data:")) data.push(line.slice(5).replace(/^ /, ""));
                        }
                        if (done) break;
                    }
                } finally {
                    signal.removeEventListener("abort", abort);
                    await reader.cancel().catch(() => undefined);
                    reader.releaseLock();
                }
                if (terminal || opts.signal?.aborted) return;
                throw new AgentRequestError(0, 0, attempt.signal.aborted ? "agent_stream_timeout" : "agent_stream_ended", "Agent 事件连接中断，可用当前游标继续读取", true);
            } catch (error) {
                if (opts.signal?.aborted) return;
                await response?.body?.cancel().catch(() => undefined);
                if (error instanceof AgentRequestError && (!error.retryable || (error.retryAfterMs ?? 0) > 10_000)) throw error;
                if (++failures > maxRetries) throw error;
            } finally {
                clearTimeout(watchdog!);
            }
            try {
                await sleep(Math.max(retryDelay(failures), retryAfterMs), opts.signal);
            } catch (error) {
                if (opts.signal?.aborted) return;
                throw error;
            }
        }
    }
    return {
        startRun: (input: AgentRunRequest, signal?: AbortSignal) => request<AgentRunReceipt<R>>({ method: "POST", path: "/agent/runs", body: input, signal }, true),
        /** Continue a completed run. Active conversations use sendMessage's existing queue. */
        continueRun: (id: string, input: AgentRunRequest, signal?: AbortSignal) => request<AgentRunReceipt<R>>({ method: "POST", path: `${runPath(id)}/messages`, body: input, signal }, true),
        sendMessage: (id: string, input: AgentMessageInput, signal?: AbortSignal) => request<AgentMessageReceipt>({ method: "POST", path: `${conversationPath(id)}/messages`, body: input, signal }, true),
        listConversations: (page: { before?: string; limit?: number } = {}, signal?: AbortSignal) =>
            request<AgentConversationPage<AgentConversation>>(
                {
                    method: "GET",
                    path: `/agent/conversations?${new URLSearchParams({ before: page.before ?? "", limit: String(page.limit ?? 30) })}`,
                    signal,
                },
                true,
            ),
        listConversationRuns: (id: string, page: { after?: string; limit?: number } = {}, signal?: AbortSignal) =>
            request<AgentConversationPage<R>>(
                {
                    method: "GET",
                    path: `${conversationPath(id)}/runs?${new URLSearchParams({ after: page.after ?? "", limit: String(page.limit ?? 30) })}`,
                    signal,
                },
                true,
            ),
        getConversationQueue: (id: string, signal?: AbortSignal) => request<AgentConversationQueue>({ method: "GET", path: `${conversationPath(id)}/queue`, signal }, true),
        archiveConversation: (id: string, signal?: AbortSignal) => request<{ archived: boolean }>({ method: "DELETE", path: conversationPath(id), signal }),
        getRun: (id: string, signal?: AbortSignal, query = "") => request<AgentRunReceipt<R>>({ method: "GET", path: runPath(id) + query, signal }, true),
        cancelRun: (id: string, signal?: AbortSignal) => request<{ accepted: boolean }>({ method: "POST", path: `${runPath(id)}/cancel`, signal }),
        decideApproval: (id: string, approvalId: string, input: AgentApprovalDecision, signal?: AbortSignal) =>
            request<{ accepted: boolean }>({ method: "POST", path: `${runPath(id)}/approvals/${encodeURIComponent(approvalId)}/decision`, body: input, signal }),
        readEvents,
    };
}
