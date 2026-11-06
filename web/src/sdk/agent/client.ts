/**
 * Agent 服务客户端：会话目录、消息、工作、审批与费用。只依赖注入的 Transport，
 * 不读 window、store 或配置；业务页面不在这里执行工具或拼装上下文。
 */
import type { Transport, TransportRequest } from "../creation-platform/transport";
import type { AgentError, AgentErrorCode, ApprovalView, BudgetView, DefinitionView, MessagePage, MessageReceipt, ReportView, Scope, SessionPage, SessionView, WorkDirectoryPage, WorkView } from "./types";

const AGENT_ERROR_CODES: readonly AgentErrorCode[] = [
    "invalid_request",
    "unauthorized",
    "forbidden",
    "not_found",
    "scope_mismatch",
    "session_conflict",
    "stale_lease",
    "idempotency_conflict",
    "work_stopped",
    "stale_assignment",
    "budget_exceeded",
    "version_conflict",
    "internal",
    "transport",
];

export class AgentRequestError extends Error {
    readonly error: AgentError;
    constructor(error: AgentError) {
        super(error.message);
        this.name = "AgentRequestError";
        this.error = error;
    }
}

export type AgentClientOptions = { transport: Transport; newMessageId?: () => string };

const defaultId = () => (globalThis.crypto?.randomUUID ? globalThis.crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`);

function scopeQuery(scope?: Scope) {
    const params = new URLSearchParams();
    if (scope) {
        params.set("application", scope.application);
        params.set("spaceType", scope.spaceType);
        params.set("spaceId", scope.spaceId);
    }
    return params;
}

export function createAgentClient(options: AgentClientOptions) {
    const { transport } = options;

    async function call<T>(request: TransportRequest): Promise<T> {
        let response;
        try {
            response = await transport.request(request);
        } catch (error) {
            if (error instanceof DOMException && error.name === "AbortError") throw error;
            throw new AgentRequestError({ code: "transport", message: error instanceof Error ? error.message : "网络请求失败", retryable: true });
        }
        const { status, body } = response;
        if (status >= 200 && status < 300 && body.code === 0) return body.data as T;
        const reason = String(body.reason ?? "");
        const code = (AGENT_ERROR_CODES as readonly string[]).includes(reason) ? (reason as AgentErrorCode) : status === 401 ? "unauthorized" : status === 404 ? "not_found" : "internal";
        throw new AgentRequestError({ code, message: body.msg || "请求失败", retryable: Boolean(body.details?.retryable), httpStatus: status });
    }

    const id = (value: string) => encodeURIComponent(value);

    return {
        definitions: async (application?: string, signal?: AbortSignal) => (await call<{ definitions: DefinitionView[] }>({ method: "GET", path: `/agent/v2/definitions${application ? `?application=${id(application)}` : ""}`, signal })).definitions,

        createSession: (request: { definitionId: string; scope: Scope; title?: string; idempotencyKey?: string }, signal?: AbortSignal) =>
            call<SessionView>({ method: "POST", path: "/agent/v2/sessions", body: { ...request, idempotencyKey: request.idempotencyKey || defaultId() }, signal }),

        listSessions(query: { scope?: Scope; cursor?: string; limit?: number } = {}, signal?: AbortSignal) {
            const params = scopeQuery(query.scope);
            if (query.cursor) params.set("cursor", query.cursor);
            if (query.limit) params.set("limit", String(query.limit));
            const suffix = params.toString();
            return call<SessionPage>({ method: "GET", path: `/agent/v2/sessions${suffix ? `?${suffix}` : ""}`, signal });
        },

        getSession: (sessionId: string, signal?: AbortSignal) => call<SessionView>({ method: "GET", path: `/agent/v2/sessions/${id(sessionId)}`, signal }),

        archiveSession: (sessionId: string, expectedRevision: number) => call<SessionView>({ method: "POST", path: `/agent/v2/sessions/${id(sessionId)}/archive`, body: { expectedRevision } }),

        /** 读取会话消息；after 是上次读到的最大序号，服务端只返回更新的消息。 */
        messages: (sessionId: string, after: number, signal?: AbortSignal) => call<MessagePage>({ method: "GET", path: `/agent/v2/sessions/${id(sessionId)}/messages?after=${after}`, signal }),

        /** 发送用户消息。消息 ID 由客户端生成以便重投去重；发送者身份由服务端写入。 */
        sendMessage: (sessionId: string, message: { text: string; messageId?: string }, signal?: AbortSignal) =>
            call<MessageReceipt>({ method: "POST", path: `/agent/v2/sessions/${id(sessionId)}/messages`, body: { messageId: message.messageId || (options.newMessageId ?? defaultId)(), text: message.text }, signal }),

        works: (limit?: number, signal?: AbortSignal) => call<WorkDirectoryPage>({ method: "GET", path: `/agent/v2/works?limit=${limit ?? 20}`, signal }),
        work: (workId: string, signal?: AbortSignal) => call<WorkView>({ method: "GET", path: `/agent/v2/work/${id(workId)}`, signal }),

        reports: async (workId: string, signal?: AbortSignal) => (await call<{ reports: ReportView[] }>({ method: "GET", path: `/agent/v2/work/${id(workId)}/reports`, signal })).reports,

        stopWork: (workId: string, expectedVersion: number, reason?: string) => call<WorkView>({ method: "POST", path: `/agent/v2/work/${id(workId)}/stop`, body: { expectedVersion, reason } }),

        /** 根工作返回整棵树的审批，子工作返回自己的；两个视图看到的是同一审批 ID。 */
        workApprovals: async (workId: string, signal?: AbortSignal) => (await call<{ approvals: ApprovalView[] }>({ method: "GET", path: `/agent/v2/work/${id(workId)}/approvals`, signal })).approvals,

        decideApproval: (approvalId: string, decision: "approved" | "rejected", expectedAuthorizationVersion: number) =>
            call<ApprovalView>({ method: "POST", path: `/agent/v2/approvals/${id(approvalId)}/decision`, body: { decision, expectedAuthorizationVersion } }),

        async budget(grantId: string, signal?: AbortSignal): Promise<BudgetView> {
            const grant = await call<{
                limitMicrocredits: number;
                availableMicrocredits: number;
                usage: { chargedMicrocredits: number; inFlightMicrocredits: number; pendingReviewMicrocredits: number; refundedMicrocredits: number };
                status: "open" | "closed";
            }>({
                method: "GET",
                path: `/platform/v1/budget-grants/${id(grantId)}`,
                signal,
            });
            return {
                limit: grant.limitMicrocredits,
                available: grant.availableMicrocredits,
                charged: grant.usage.chargedMicrocredits,
                inFlight: grant.usage.inFlightMicrocredits,
                pendingReview: grant.usage.pendingReviewMicrocredits,
                refunded: grant.usage.refundedMicrocredits,
                status: grant.status,
            };
        },
    };
}

export type AgentClient = ReturnType<typeof createAgentClient>;
