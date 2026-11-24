import type {
    AgentApprovalDecision,
    AgentBusinessScope,
    AgentCapabilities,
    AgentConversation,
    AgentConversationPage,
    AgentConversationQueue,
    AgentEvent,
    AgentMessageInput,
    AgentMessageReceipt,
    AgentPendingDirectorPreviewPage,
    AgentQueuedMessage,
    AgentRunQuery,
    AgentRunReceipt,
    AgentRunRequest,
    AgentRunRequestBase,
    AgentRunSnapshot,
    AgentSkillDefaultsSummary,
    AgentUISettings,
} from "./contract";
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
const sameScope = (left: AgentBusinessScope, right: AgentBusinessScope) => left.type === right.type && left.id === right.id;

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
    const scopeConflict = (message: string) => new AgentRequestError(0, 0, "agent_scope_invalid", message, false);
    const scopedPathFor = (path: string, selectedScope?: AgentBusinessScope, selectedDefinition?: string) => {
        if (!selectedScope) return path;
        const [pathname, search] = path.split("?", 2);
        const query = new URLSearchParams(search);
        query.set("scopeType", selectedScope.type);
        query.set("scopeId", selectedScope.id);
        if (selectedDefinition) query.set("agentDefinitionId", selectedDefinition);
        return `${pathname}?${query}`;
    };
    const scopedPath = (path: string) => scopedPathFor(path, scope, definitionId);
    const prepareRunRequest = (input: AgentRunRequest): AgentRunRequest => {
        if (!scope && !definitionId) return input;
        const prepared: AgentRunRequestBase & { scope?: AgentBusinessScope; canvasId?: string; dramaEpisodeId?: string } = { ...input };
        if (scope) {
            if (input.scope && !sameScope(input.scope, scope)) throw scopeConflict("请求范围与已绑定的 Agent client 不一致");
            prepared.scope = { ...scope };
            if (scope.type === "canvas") {
                if (input.canvasId && input.canvasId !== scope.id) throw scopeConflict("canvasId 与已绑定的画布范围不一致");
                prepared.canvasId = scope.id;
            } else if (input.canvasId) {
                throw scopeConflict("非画布业务不能携带 canvasId");
            }
        }
        if (definitionId) {
            if (input.agentDefinitionId && input.agentDefinitionId !== definitionId) throw scopeConflict("Agent 配置与已绑定的 client 不一致");
            prepared.agentDefinitionId = definitionId;
        }
        return prepared as AgentRunRequest;
    };
    const prepareMessage = (input: AgentMessageInput): AgentMessageInput => ({ ...input, request: prepareRunRequest(input.request) });
    const preparedRun = (prepare: () => AgentRunRequest, operate: (value: AgentRunRequest) => Promise<AgentRunReceipt<R>>): Promise<AgentRunReceipt<R>> => {
        try {
            return operate(prepare());
        } catch (error) {
            return Promise.reject(error);
        }
    };
    const preparedMessage = (prepare: () => AgentMessageInput, operate: (value: AgentMessageInput) => Promise<AgentMessageReceipt>): Promise<AgentMessageReceipt> => {
        try {
            return operate(prepare());
        } catch (error) {
            return Promise.reject(error);
        }
    };
    const runQuery = (value: string | AgentRunQuery = "") => {
        if (typeof value === "string") return value && value.startsWith("?") ? value : value ? `?${value}` : "";
        const query = new URLSearchParams();
        if (value.sinceSeq !== undefined) query.set("sinceSeq", String(value.sinceSeq));
        if (value.eventLimit !== undefined) query.set("eventLimit", String(value.eventLimit));
        if (value.fromStart !== undefined) query.set("fromStart", String(value.fromStart));
        const encoded = query.toString();
        return encoded ? `?${encoded}` : "";
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
        startRun: (input: AgentRunRequest, signal?: AbortSignal) =>
            preparedRun(
                () => prepareRunRequest(input),
                (body) => request<AgentRunReceipt<R>>({ method: "POST", path: "/agent/runs", body, signal }, true),
            ),
        /** Continue a completed run. Active conversations use sendMessage's existing queue. */
        continueRun: (id: string, input: AgentRunRequest, signal?: AbortSignal) =>
            preparedRun(
                () => prepareRunRequest(input),
                (body) => request<AgentRunReceipt<R>>({ method: "POST", path: `${runPath(id)}/messages`, body, signal }, true),
            ),
        sendMessage: (id: string, input: AgentMessageInput, signal?: AbortSignal) =>
            preparedMessage(
                () => prepareMessage(input),
                (body) => request<AgentMessageReceipt>({ method: "POST", path: `${conversationPath(id)}/messages`, body, signal }, true),
            ),
        listConversations: (page: { before?: string; limit?: number } = {}, signal?: AbortSignal) =>
            request<AgentConversationPage<AgentConversation>>(
                {
                    method: "GET",
                    path: `/agent/conversations?${new URLSearchParams({ before: page.before ?? "", limit: String(page.limit ?? 30) })}`,
                    signal,
                },
                true,
            ),
        listPendingDirectorPreviews: (signal?: AbortSignal) => request<AgentPendingDirectorPreviewPage>({ method: "GET", path: "/agent/director-previews/pending", signal }, true),
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
        updateQueuedMessage: (conversationId: string, messageId: string, input: { revision: number; prompt?: string; action?: "interject" }, signal?: AbortSignal) =>
            request<AgentMessageReceipt>({
                method: "PATCH",
                path: `${conversationPath(conversationId)}/queue/${encodeURIComponent(messageId)}`,
                body: input,
                signal,
            }),
        cancelQueuedMessage: (conversationId: string, messageId: string, revision: number, signal?: AbortSignal) => {
            const [pathname, search] = `${conversationPath(conversationId)}/queue/${encodeURIComponent(messageId)}`.split("?", 2);
            const query = new URLSearchParams(search);
            query.set("revision", String(revision));
            return request<AgentMessageReceipt>({ method: "DELETE", path: `${pathname}?${query}`, signal });
        },
        updateConversationQueue: (id: string, input: { revision: number; action: "pause" | "resume" }, signal?: AbortSignal) => request<AgentConversationQueue>({ method: "PATCH", path: `${conversationPath(id)}/queue`, body: input, signal }),
        archiveConversation: (id: string, signal?: AbortSignal) => request<{ archived: boolean }>({ method: "DELETE", path: conversationPath(id), signal }),
        getRun: (id: string, signal?: AbortSignal, query: string | AgentRunQuery = "") => request<AgentRunReceipt<R>>({ method: "GET", path: runPath(id) + runQuery(query), signal }, true),
        cancelRun: (id: string, signal?: AbortSignal) => request<{ accepted: boolean }>({ method: "POST", path: `${runPath(id)}/cancel`, signal }),
        changePermission: (id: string, input: { permissionMode: AgentRunSnapshot["permissionMode"]; requestId: string }, signal?: AbortSignal) =>
            request<{ permissionMode: AgentRunSnapshot["permissionMode"]; changed: boolean }>({ method: "POST", path: `${runPath(id)}/permission`, body: input, signal }),
        decideApproval: (id: string, approvalId: string, input: AgentApprovalDecision, signal?: AbortSignal) =>
            request<{ accepted: boolean }>({ method: "POST", path: `${runPath(id)}/approvals/${encodeURIComponent(approvalId)}/decision`, body: input, signal }),
        getCapabilities: (input: { scope?: AgentBusinessScope; agentDefinitionId?: string } = {}, signal?: AbortSignal) => {
            return Promise.resolve().then(() => {
                const selectedScope = input.scope ?? scope;
                const selectedDefinition = input.agentDefinitionId ?? definitionId;
                if (input.scope && scope && !sameScope(input.scope, scope)) throw scopeConflict("能力查询范围与已绑定的 client 不一致");
                return request<AgentCapabilities>({ method: "GET", path: scopedPathFor("/agent/capabilities", selectedScope, selectedDefinition), signal }, true);
            });
        },
        getSkillDefaults: (input: { scopeType: string; agentDefinitionId?: string }, signal?: AbortSignal) =>
            request<AgentSkillDefaultsSummary>(
                {
                    method: "GET",
                    path: `/agent/skill-defaults?${new URLSearchParams({ scopeType: input.scopeType, ...(input.agentDefinitionId ? { agentDefinitionId: input.agentDefinitionId } : {}) })}`,
                    signal,
                },
                true,
            ),
        getUISettings: (signal?: AbortSignal) => request<AgentUISettings>({ method: "GET", path: "/agent/ui-settings", signal }, true),
        updateUISettings: (input: AgentUISettings, signal?: AbortSignal) => request<AgentUISettings>({ method: "PATCH", path: "/agent/ui-settings", body: input, signal }),
        readEvents,
    };
}
