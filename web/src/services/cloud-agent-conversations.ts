import { localForageStorageForScope } from "@/lib/localforage-storage";
import { markdownPlainText } from "@/lib/markdown-plain-text";
import { getActiveUserScope } from "@/lib/user-scope";
import type { AgentPermissionMode, AgentRun } from "@/services/api/agent";
import type { AgentContextUsage } from "@/lib/canvas/agent-context-usage";

export type CloudAgentConversationMessage = {
    id: string;
    role: "user" | "assistant" | "system" | "tool" | "error";
    title?: string;
    text: string;
    streaming?: boolean;
    reasoning?: boolean;
    planItems?: Array<{ id: string; title: string; status: "pending" | "doing" | "done" }>;
    planTerminal?: boolean;
    question?: { question: string; options: Array<{ label: string; detail?: string }>; allowFreeform?: boolean };
    meta?: string;
    runId?: string;
    detail?: unknown;
    attachments?: Array<{ id: string; name: string; url: string }>;
    interjection?: "sent" | "delivered" | "undelivered" | "withdrawn";
};

export type CloudAgentConversation = {
	archived?: boolean;
    id: string;
    serverConversationId?: string;
    runs?: AgentRun[];
    lastEventSeq?: number;
    historyState?: { status: "partial" | "unavailable"; message: string };
    draft?: string;
    title: string;
    messages: CloudAgentConversationMessage[];
    run: AgentRun | null;
    contextUsage?: AgentContextUsage;
    model?: string;
    permissionMode: AgentPermissionMode;
    skillIds?: string[];
    createdAt: string;
    updatedAt: string;
};

export function cloudAgentConversationServerId(conversation: CloudAgentConversation) {
    return conversation.serverConversationId || conversation.run?.conversationId;
}

type CloudAgentConversationDocument = {
    version: 1;
    activeId: string | null;
    conversations: CloudAgentConversation[];
};

export type CloudAgentPendingSubmission = {
    fingerprint: string;
    key: string;
    request?: import("@/services/api/agent").CreateAgentRunInput;
    parentRunId?: string;
    messageId?: string;
    /** Draft retained when a canvas node sends a separate request. */
    composerDraft?: string;
    conversationMessage?: { conversationId: string; mode: import("@/services/api/agent").AgentSendMode; expectedRunId: string; replyToQuestionId?: string };
};

const CLOUD_AGENT_CONVERSATIONS_KEY = "cloud-agent-conversations-v1";

/** 云端同步失败的提示只描述当前连接状态：同步恢复后撤下，也不写进对话历史。 */
export const CLOUD_AGENT_SYNC_ERROR_MESSAGE_ID = "cloud-history-error";

function withoutTransientMessages(conversation: CloudAgentConversation): CloudAgentConversation {
    return conversation.messages.some((message) => message.id === CLOUD_AGENT_SYNC_ERROR_MESSAGE_ID)
        ? { ...conversation, messages: conversation.messages.filter((message) => message.id !== CLOUD_AGENT_SYNC_ERROR_MESSAGE_ID) }
        : conversation;
}

export async function loadCloudAgentConversations(canvasId: string, scope = getActiveUserScope()): Promise<CloudAgentConversationDocument> {
    if (!canvasId) throw new Error("缺少画布 ID，无法读取 Agent 对话");
    const value = await localForageStorageForScope(scope).getItem(storageKey(canvasId));
    if (!value) return emptyDocument();

    let parsed: unknown;
    try {
        parsed = JSON.parse(value);
    } catch {
        throw new Error("Agent 对话历史已损坏");
    }
    if (!isConversationDocument(parsed)) throw new Error("Agent 对话历史格式无效");
    // 旧版本会把同步失败提示存进历史；读出时一并清掉。
    return { ...parsed, conversations: parsed.conversations.map(withoutTransientMessages) };
}

export async function saveCloudAgentConversations(canvasId: string, activeId: string | null, conversations: CloudAgentConversation[], scope = getActiveUserScope()) {
    if (!canvasId) throw new Error("缺少画布 ID，无法保存 Agent 对话");
    // 流式标记只属于内存中的观察状态。若网络在最后一个 delta 后断开，
    // 也不能让下次恢复把思考卡片误判为仍在实时展开。
    const persistedConversations = conversations.map((conversation) => {
        const persisted = withoutTransientMessages(conversation);
        return { ...persisted, messages: persisted.messages.map((message) => ({ ...message, streaming: false })) };
    });
    const document: CloudAgentConversationDocument = { version: 1, activeId, conversations: persistedConversations };
    await localForageStorageForScope(scope).setItem(storageKey(canvasId), JSON.stringify(document));
}

// Keep the retry key outside the chat snapshot. If the response is lost, a
// page refresh can safely repeat the same request without creating another
// billed Agent turn.
export async function loadCloudAgentPendingSubmission(canvasId: string, conversationId: string, scope = getActiveUserScope()) {
    if (!canvasId || !conversationId) return null;
    const value = await localForageStorageForScope(scope).getItem(pendingStorageKey(canvasId, conversationId));
    if (value === null || value === undefined) return null;
    // A corrupt recovery record is not proof that the previous POST never ran.
    // Fail closed rather than discarding its identity and risking another charge.
    try {
        const parsed = JSON.parse(value) as Partial<CloudAgentPendingSubmission> | null;
        if (!parsed || typeof parsed.fingerprint !== "string" || typeof parsed.key !== "string" || parsed.key.length < 8) throw new Error("invalid identity");
        if (parsed.request !== undefined && (!parsed.request || parsed.request.idempotencyKey !== parsed.key || parsed.request.canvasId !== canvasId || typeof parsed.request.prompt !== "string")) throw new Error("invalid request");
        if (parsed.parentRunId !== undefined && typeof parsed.parentRunId !== "string") throw new Error("invalid parent");
        if (parsed.messageId !== undefined && typeof parsed.messageId !== "string") throw new Error("invalid message");
        if (parsed.composerDraft !== undefined && typeof parsed.composerDraft !== "string") throw new Error("invalid composer draft");
        const delivery = parsed.conversationMessage;
        if (delivery !== undefined && (!delivery || !parsed.request || typeof delivery.conversationId !== "string" || !delivery.conversationId || (delivery.mode !== "interject" && delivery.mode !== "queue") || typeof delivery.expectedRunId !== "string" || delivery.expectedRunId !== parsed.parentRunId || (delivery.replyToQuestionId !== undefined && (typeof delivery.replyToQuestionId !== "string" || delivery.mode !== "interject")))) throw new Error("invalid delivery");
        return { fingerprint: parsed.fingerprint, key: parsed.key, request: parsed.request, parentRunId: parsed.parentRunId, messageId: parsed.messageId, ...(parsed.composerDraft !== undefined ? { composerDraft: parsed.composerDraft } : {}), ...(delivery ? { conversationMessage: delivery } : {}) } satisfies CloudAgentPendingSubmission;
    } catch {
        throw new Error("待确认请求记录损坏，已暂停本对话发送；请先在任务中心核对原运行，不要直接重复生成");
    }
}

export async function saveCloudAgentPendingSubmission(canvasId: string, conversationId: string, pending: CloudAgentPendingSubmission, scope = getActiveUserScope()) {
    if (!canvasId || !conversationId) throw new Error("缺少 Agent 对话范围，无法保存幂等提交");
    await localForageStorageForScope(scope).setItem(pendingStorageKey(canvasId, conversationId), JSON.stringify(pending));
}

export async function clearCloudAgentPendingSubmission(canvasId: string, conversationId: string, scope = getActiveUserScope()) {
    if (!canvasId || !conversationId) return;
    await localForageStorageForScope(scope).removeItem(pendingStorageKey(canvasId, conversationId));
}

export function cloudAgentConversationTitle(messages: CloudAgentConversationMessage[]) {
    const firstPrompt = displayConversationTitle(messages.find((message) => message.role === "user")?.text || "");
    return firstPrompt.length > 28 ? `${firstPrompt.slice(0, 28)}...` : firstPrompt;
}

function displayConversationTitle(value: string) {
    return markdownPlainText(value.replace(/@\[skill:[^\]]+\]/gu, "技能包")) || "新对话";
}

function storageKey(canvasId: string) {
    return `${CLOUD_AGENT_CONVERSATIONS_KEY}:${encodeURIComponent(canvasId)}`;
}

function pendingStorageKey(canvasId: string, conversationId: string) {
    return `${CLOUD_AGENT_CONVERSATIONS_KEY}:pending:${encodeURIComponent(canvasId)}:${encodeURIComponent(conversationId)}`;
}

function emptyDocument(): CloudAgentConversationDocument {
    return { version: 1, activeId: null, conversations: [] };
}

function isConversationDocument(value: unknown): value is CloudAgentConversationDocument {
    if (!value || typeof value !== "object") return false;
    const document = value as Partial<CloudAgentConversationDocument>;
    if (document.version !== 1 || !Array.isArray(document.conversations)) return false;
    return document.conversations.every((conversation) => {
        if (!conversation || typeof conversation !== "object") return false;
        const candidate = conversation as Partial<CloudAgentConversation>;
        return typeof candidate.id === "string"
            && typeof candidate.title === "string"
            && Array.isArray(candidate.messages)
            && typeof candidate.createdAt === "string"
            && typeof candidate.updatedAt === "string"
            && ["read_only", "auto", "request_approval"].includes(candidate.permissionMode || "");
    });
}
