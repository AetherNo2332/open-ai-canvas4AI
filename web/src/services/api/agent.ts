import { http, apiBaseURL, compactApiParams, ApiError } from "@/services/api/request";
import { consumeTaskTextStream, createTaskTextStreamParser } from "@/services/api/task-text-stream";
import { createAgentClient, AgentRequestError } from "@/sdk/agent/client";
import { agentWebTransport } from "@/sdk/agent/http-transport";
import type {
    AgentBusinessScope,
    AgentCapabilities,
    AgentConversationQueue,
    AgentEvent as SDKAgentEvent,
    AgentQueuedMessage,
    AgentRunRequest,
    AgentRunSnapshot,
    AgentUISettings,
} from "@/sdk/agent/contract";

const agentClient = createAgentClient<AgentRun>({ transport: agentWebTransport });
const canvasAgentClient = (canvasId?: string) => (canvasId ? createAgentClient<AgentRun>({ transport: agentWebTransport, scope: { type: "canvas", id: canvasId } }) : agentClient);
const scopedAgentClient = (scope?: AgentBusinessScope, agentDefinitionId?: string) => (scope ? createAgentClient<AgentRun>({ transport: agentWebTransport, scope, agentDefinitionId }) : agentClient);
function rethrowAgentApiError(error: unknown): never {
    if (error instanceof AgentRequestError) {
        throw new ApiError(error.message, { status: error.status, code: error.code, reason: error.reason, retryable: error.retryable, retryAfterMs: error.retryAfterMs, details: error.details, cause: error });
    }
    throw error;
}

export type AgentPermissionMode = import("@/sdk/agent/contract").AgentPermissionMode;
export type AgentSystemPromptSettings = { systemPrompt: string; revision: number };
export function getAgentSystemPrompt() {
    return http.get<AgentSystemPromptSettings>("/agent/settings", { timeout: 15_000 });
}
export function updateAgentSystemPrompt(input: AgentSystemPromptSettings) {
    return http.patch<AgentSystemPromptSettings>("/agent/settings", input, { timeout: 15_000 });
}
export type AgentSkillDefaultsSummary = { count: number; skills: Array<{ skillId: string; skillName: string }> };
export function listAgentSkillDefaults(scopeType: "canvas" | "drama", signal?: AbortSignal) {
    return http.get<AgentSkillDefaultsSummary>("/agent/skill-defaults", { params: { scopeType }, signal });
}
export type AgentReasoningMode = import("@/lib/text-thinking").ThinkingRequest;
export type AgentMediaSettings = {
    logicalModelId?: string;
    channelId?: string;
    channelModelKey?: string;
    size: string;
    quality: string;
};
export type AgentApprovalPreviewOperation =
    | "add_node"
    | "update_node"
    | "delete_node"
    | "duplicate_node"
    | "connect_nodes"
    | "delete_connection"
    | "update_connection"
    | "set_parent"
    | "replace_text"
    | "reorder_nodes"
    | "reorder_rows"
    | "arrange_nodes"
    | "generate_media"
    | "create_storyboard"
    | "edit_storyboard"
    | "edit_drawing"
    | "bind_asset"
    | "canvas_undo"
    | "canvas_redo"
    | "create_character"
    | "plan_step"
    | "director_read"
    | "previs_scene_create"
    | "previs_apply_patch"
    | "director_preview";

export type AgentApprovalPreviewItem = {
    operation: AgentApprovalPreviewOperation;
    nodeId?: string;
    nodeTitle?: string;
    resultTitle?: string;
    nodeType?: string;
    nodeTypeLabel?: string;
    targetNodeId?: string;
    targetNodeTitle?: string;
    targetNodeType?: string;
    fields?: string[];
    details?: string[];
    summary: string;
};

export type AgentApprovalPreview = {
    kind: string;
    title: string;
    description: string;
    items: AgentApprovalPreviewItem[];
};

export type AgentApproval = {
    modelName?: string;
    approvalId: string;
    call: { id: string; function: { name: string; arguments: string } };
    callHash?: string;
    preview?: AgentApprovalPreview;
    decision?: "approve" | "reject";
    reason?: string;
};

export type AgentRunSkill = import("@/sdk/agent/contract").AgentRunSkill;

export type AgentRun = Omit<AgentRunSnapshot, "events" | "approval"> & {
    events?: AgentEvent[];
    approval?: AgentApproval;
};

export type AgentContextCompactionState = import("@/sdk/agent/contract").AgentContextCompactionState;

export type AgentEvent = SDKAgentEvent & {
    /** Local delivery order for synthetic snapshot events; never used as a resume cursor. */
    localSeq?: number;
};

/** 事件流本身不是 http 封装请求，单独保留状态码供 UI 区分旧后端/失效轮次。 */
export class AgentStreamError extends Error {
    readonly status: number;

    constructor(status: number) {
        super(`Agent 事件流不可用 (${status})`);
        this.name = "AgentStreamError";
        this.status = status;
    }
}

export type CreateAgentRunInput = AgentRunRequest;

export async function createAgentRun(input: CreateAgentRunInput, signal?: AbortSignal) {
    return agentClient.startRun(input, signal).catch(rethrowAgentApiError);
}

export async function sendAgentMessage(runId: string, input: CreateAgentRunInput, signal?: AbortSignal) {
    return agentClient.continueRun(runId, input, signal).catch(rethrowAgentApiError);
}

/** 导演台浏览器视口录制完成（或失败）后回传结果，恢复 Agent 挂起的 director preview。 */
export async function submitDirectorPreviewResult(runId: string, requestId: string, result: { videoResourceId: string } | { error: string }): Promise<void> {
    await http.post<{ accepted: boolean }>(`/agent/runs/${encodeURIComponent(runId)}/director-preview`, { requestId, ...result }, { timeout: 20_000 });
}

/** 运行中切换执行权限：下一次工具调用生效；正在等待的审批仍需用户决定。 */
export function changeAgentRunPermission(runId: string, input: { permissionMode: AgentPermissionMode; requestId: string }) {
    return http.post<{ permissionMode: AgentPermissionMode; changed: boolean }>(`/agent/runs/${encodeURIComponent(runId)}/permission`, input, { timeout: 20_000 });
}

export function sendAgentInterjection(runId: string, input: { text: string; messageId: string }) {
    return http.post<{ accepted: boolean; pending: number }>(`/agent/runs/${encodeURIComponent(runId)}/interjections`, input, { timeout: 20_000 });
}

export type AgentSendMode = "interject" | "queue";
export type AgentConversationMessageInput = import("@/sdk/agent/contract").AgentMessageInput;
export type AgentMessageReceipt = import("@/sdk/agent/contract").AgentMessageReceipt;
export type { AgentQueuedMessage, AgentUISettings };
export type AgentMessageQueue = AgentConversationQueue;

export function sendAgentConversationMessage(conversationId: string, input: AgentConversationMessageInput, signal?: AbortSignal) {
    const scope = input.request.scope ?? (input.request.canvasId ? { type: "canvas" as const, id: input.request.canvasId } : undefined);
    return scopedAgentClient(scope, input.request.agentDefinitionId).sendMessage(conversationId, input, signal).catch(rethrowAgentApiError);
}
export function getAgentMessageQueue(conversationId: string, canvasId: string, signal?: AbortSignal) {
    return getAgentMessageQueueForScope(conversationId, { type: "canvas", id: canvasId }, signal);
}
export function getAgentMessageQueueForScope(conversationId: string, scope: AgentBusinessScope, signal?: AbortSignal, agentDefinitionId?: string) {
    return scopedAgentClient(scope, agentDefinitionId).getConversationQueue(conversationId, signal).catch(rethrowAgentApiError);
}
export function updateAgentQueuedMessage(conversationId: string, messageId: string, input: { revision: number; prompt?: string; action?: "interject" }, canvasId: string) {
    return scopedAgentClient({ type: "canvas", id: canvasId }).updateQueuedMessage(conversationId, messageId, input).catch(rethrowAgentApiError);
}
export function cancelAgentQueuedMessage(conversationId: string, messageId: string, revision: number, canvasId: string) {
    return scopedAgentClient({ type: "canvas", id: canvasId }).cancelQueuedMessage(conversationId, messageId, revision).catch(rethrowAgentApiError);
}
export function updateAgentMessageQueue(conversationId: string, input: { revision: number; action: "pause" | "resume" }, canvasId: string) {
    return scopedAgentClient({ type: "canvas", id: canvasId }).updateConversationQueue(conversationId, input).catch(rethrowAgentApiError);
}
export function getAgentUISettings(signal?: AbortSignal) {
    return agentClient.getUISettings(signal).catch(rethrowAgentApiError);
}
export function updateAgentUISettings(input: AgentUISettings) {
    return agentClient.updateUISettings(input).catch(rethrowAgentApiError);
}

export function getAgentCapabilities(options: { scope?: AgentBusinessScope; agentDefinitionId?: string } = {}, signal?: AbortSignal) {
    return scopedAgentClient(options.scope, options.agentDefinitionId).getCapabilities(options, signal).catch(rethrowAgentApiError) as Promise<AgentCapabilities>;
}

export function getAgentRun(runId: string, signal?: AbortSignal, options?: { sinceSeq?: number; eventLimit?: number; fromStart?: boolean; canvasId?: string }) {
    return canvasAgentClient(options?.canvasId)
        .getRun(runId, signal, {
            sinceSeq: options?.sinceSeq,
            eventLimit: options?.eventLimit,
            fromStart: options?.fromStart,
        })
        .catch(rethrowAgentApiError);
}

export type AgentConversationSummary = import("@/sdk/agent/contract").AgentConversation;

export function listAgentConversations(options: { canvasId?: string; scope?: AgentBusinessScope; agentDefinitionId?: string; before?: string; limit?: number; signal?: AbortSignal }) {
    const scope = options.scope ?? (options.canvasId ? { type: "canvas" as const, id: options.canvasId } : undefined);
    return scopedAgentClient(scope, options.agentDefinitionId)
        .listConversations({ before: options.before, limit: options.limit }, options.signal)
        .catch(rethrowAgentApiError) as Promise<{ items: AgentConversationSummary[]; nextCursor?: string }>;
}

export function archiveAgentConversation(conversationId: string, canvasId?: string) {
    return canvasAgentClient(canvasId).archiveConversation(conversationId).catch(rethrowAgentApiError);
}

export function listAgentConversationRuns(conversationId: string, options: { after?: string; limit?: number; signal?: AbortSignal; canvasId?: string } = {}) {
    return http.get<{ items: AgentRun[]; nextCursor?: string }>(`/agent/conversations/${encodeURIComponent(conversationId)}/runs`, {
        params: compactApiParams({ after: options.after, limit: options.limit, scopeType: options.canvasId ? "canvas" : undefined, scopeId: options.canvasId }),
        signal: options.signal,
    });
}

export function cancelAgentRun(runId: string, canvasId?: string) {
    return canvasAgentClient(canvasId).cancelRun(runId).catch(rethrowAgentApiError);
}

export type AgentRunUndoResult = { revision: number; restoredNodeIds: string[] };

/** 撤销一次已结束运行的全部画布修改；冲突时抛出 reason 为 agent_undo_conflict 的 ApiError。 */
export function undoAgentRun(runId: string, input: { reason?: string } = {}, signal?: AbortSignal) {
    return http.post<AgentRunUndoResult>(`/agent/runs/${encodeURIComponent(runId)}/undo`, { reason: input.reason?.trim() || undefined }, { signal, timeout: 30_000 });
}

export async function decideAgentApproval(runId: string, approvalId: string, decision: "approve" | "reject", reason?: string, signal?: AbortSignal, mediaSettings?: AgentMediaSettings, canvasId?: string) {
    return canvasAgentClient(canvasId)
        .decideApproval(runId, approvalId, { decision, reason: reason?.trim() || undefined, ...(mediaSettings ? { mediaSettings } : {}) }, signal)
        .catch(rethrowAgentApiError);
}

export function subscribeAgentEvents(
    runId: string,
    onEvent: (event: AgentEvent) => void,
    options: { after?: number; canvasId?: string; onError?: (error: unknown) => void; onConnectionChange?: (status: "connecting" | "connected" | "reconnecting" | "disconnected") => void; timeoutMs?: number } = {},
) {
    const controller = new AbortController();
    let localSeq = 0;
    let cursor = Number.isSafeInteger(options.after) && (options.after as number) > 0 ? (options.after as number) : 0;
    let lastActiveMessageKey = "";
    let lastStatusKey = "";
    const emit = (type: string, payload: Record<string, unknown>) => {
        if (controller.signal.aborted) return;
        onEvent({ eventId: `${runId}:snapshot:${++localSeq}`, runId, seq: 0, type, payload, localSeq, createdAt: new Date().toISOString() });
    };
    void (async () => {
        let failures = 0;
        while (!controller.signal.aborted) {
            let terminal = false;
            let retryAfterMs = 0;
            const attempt = new AbortController();
            const abortAttempt = () => attempt.abort();
            controller.signal.addEventListener("abort", abortAttempt, { once: true });
            let watchdog: ReturnType<typeof setTimeout>;
            const touch = () => {
                clearTimeout(watchdog);
                watchdog = setTimeout(abortAttempt, options.timeoutMs ?? 45_000);
            };
            touch();
            options.onConnectionChange?.(failures ? "reconnecting" : "connecting");
            try {
                const query = new URLSearchParams({ after: String(cursor) });
                if (options.canvasId) {
                    query.set("scopeType", "canvas");
                    query.set("scopeId", options.canvasId);
                }
                const response = await fetch(`${String(apiBaseURL).replace(/\/+$/, "")}/agent/runs/${encodeURIComponent(runId)}/events?${query}`, {
                    credentials: "include",
                    signal: attempt.signal,
                    headers: { Accept: "text/event-stream", "Last-Event-ID": String(cursor) },
                });
                if (!response.ok) {
                    const error = new AgentStreamError(response.status);
                    const header = response.headers.get("retry-after");
                    if (header) retryAfterMs = Math.max(0, /^\d+(\.\d+)?$/.test(header) ? Number(header) * 1000 : Date.parse(header) - Date.now()) || 0;
                    if (retryAfterMs > 300_000) {
                        await response.body?.cancel();
                        options.onConnectionChange?.("disconnected");
                        options.onError?.(error);
                        return;
                    }
                    await response.body?.cancel();
                    if (response.status >= 400 && response.status < 500 && ![408, 429].includes(response.status)) {
                        options.onConnectionChange?.("disconnected");
                        options.onError?.(error);
                        return;
                    }
                    throw error;
                }
                if (!response.body || !response.headers.get("content-type")?.includes("text/event-stream")) throw new AgentStreamError(response.status);
                const reader = response.body.getReader();
                const decoder = new TextDecoder();
                const parser = createTaskTextStreamParser();
                const connectedAt = Date.now();
                touch();
                const cancelReader = () => {
                    void reader.cancel().catch(() => undefined);
                };
                attempt.signal.addEventListener("abort", cancelReader, { once: true });
                try {
                    while (!controller.signal.aborted && !attempt.signal.aborted) {
                        const { value, done } = await reader.read();
                        if (value?.length) touch();
                        consumeTaskTextStream(
                            parser,
                            decoder.decode(value, { stream: !done }),
                            (item) => {
                                if (item.event === "agent_event") {
                                    const event = item.data as AgentEvent;
                                    if (event.runId !== runId || !Number.isSafeInteger(event.seq) || event.seq <= cursor) return;
                                    cursor = event.seq;
                                    failures = 0;
                                    options.onConnectionChange?.("connected");
                                    onEvent({ ...event, localSeq: ++localSeq });
                                } else if (item.event === "run_snapshot") {
                                    const run = item.data as AgentRun;
                                    if (run.id !== runId) return;
                                    options.onConnectionChange?.("connected");
                                    if (run.activeMessage) {
                                        const messageKey = `${run.activeMessage.messageId}\u0000${run.activeMessage.text}`;
                                        if (messageKey !== lastActiveMessageKey) {
                                            lastActiveMessageKey = messageKey;
                                            // 快照里的 activeMessage 是**还在流式生成**的草稿，不是本轮最终答复：
                                            // 显式带 final=false，否则前端会按"缺省即结论"（升级前口径）把它当成最终回复。
                                            emit("assistant_message", { ...run.activeMessage, final: false });
                                        }
                                    }
                                    const statusPayload = {
                                        status: run.status,
                                        revision: run.revision,
                                        cleanupPending: run.cleanupPending,
                                        failureMessage: run.failureMessage,
                                        skillRuntimeMode: run.skillRuntimeMode,
                                        skills: run.skills,
                                        spentCredits: run.spentCredits,
                                        step: run.step,
                                        approval: run.approval,
                                        contextCompaction: run.contextCompaction,
                                    };
                                    const statusKey = JSON.stringify(statusPayload);
                                    if (statusKey !== lastStatusKey) {
                                        lastStatusKey = statusKey;
                                        emit("run_status", statusPayload);
                                    }
                                    terminal = !run.cleanupPending && ["completed", "failed", "cancelled", "rejected"].includes(run.status);
                                } else if (item.event === "error") throw new Error("Agent 状态读取失败");
                            },
                            done,
                        );
                        // A repeated initial snapshot is not a healthy connection.
                        // Sustained heartbeat traffic or advancing events reset the budget.
                        if (Date.now() - connectedAt >= 30_000 && value?.length) failures = 0;
                        if (done || terminal) break;
                    }
                } finally {
                    attempt.signal.removeEventListener("abort", cancelReader);
                    await reader.cancel().catch(() => undefined);
                    reader.releaseLock();
                }
                if (terminal) return;
                throw new Error("Agent 连接已中断，运行仍保留，可重新连接");
            } catch (error) {
                if (controller.signal.aborted) return;
                if (++failures >= 5) {
                    options.onConnectionChange?.("disconnected");
                    options.onError?.(error);
                    return;
                }
            } finally {
                clearTimeout(watchdog!);
                controller.signal.removeEventListener("abort", abortAttempt);
                attempt.abort();
            }
            options.onConnectionChange?.("reconnecting");
            await new Promise<void>((resolve) => {
                const finish = () => {
                    clearTimeout(timer);
                    controller.signal.removeEventListener("abort", finish);
                    resolve();
                };
                const backoff = Math.min(1000 * 2 ** failures, 15000) * (0.75 + Math.random() * 0.5);
                const timer = setTimeout(finish, Math.max(backoff, Math.min(retryAfterMs, 300_000)));
                controller.signal.addEventListener("abort", finish, { once: true });
                if (controller.signal.aborted) finish();
            });
        }
    })();
    return () => controller.abort();
}
