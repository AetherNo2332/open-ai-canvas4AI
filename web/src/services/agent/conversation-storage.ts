import { localForageStorageForScope } from "@/lib/localforage-storage";
import { getActiveUserScope } from "@/lib/user-scope";
import type { AgentRunSnapshot } from "@/sdk/agent";
import type { AgentConversationDocument, AgentConversationIdentity, AgentConversationMessage, AgentConversationRecord, AgentPendingSubmission } from "./conversation-types";
import { emptyAgentConversationDocument } from "./conversation-types";
import type { AgentContextUsage } from "@/lib/agent/context-usage";

export type AgentConversationStore = {
    loadDocument: (identity: AgentConversationIdentity) => Promise<AgentConversationDocument>;
    saveDocument: (identity: AgentConversationIdentity, document: AgentConversationDocument) => Promise<void>;
    loadDraft: (identity: AgentConversationIdentity, conversationId: string) => Promise<string>;
    saveDraft: (identity: AgentConversationIdentity, conversationId: string, draft: string) => Promise<void>;
    clearDraft: (identity: AgentConversationIdentity, conversationId: string) => Promise<void>;
    loadPending: (identity: AgentConversationIdentity, conversationId: string) => Promise<AgentPendingSubmission | null>;
    savePending: (identity: AgentConversationIdentity, conversationId: string, pending: AgentPendingSubmission) => Promise<void>;
    clearPending: (identity: AgentConversationIdentity, conversationId: string) => Promise<void>;
};

const DOCUMENT_KEY = "cloud-agent-conversations-v2";
const DRAFT_KEY = "cloud-agent-draft-v2";
const PENDING_KEY = "cloud-agent-pending-v2";

function identityKey(identity: AgentConversationIdentity) {
    return [identity.scope.type, identity.scope.id, identity.agentDefinitionId].map((value) => encodeURIComponent(value)).join(":");
}

function documentKey(identity: AgentConversationIdentity) {
    return `${DOCUMENT_KEY}:${identityKey(identity)}`;
}

function draftKey(identity: AgentConversationIdentity, conversationId: string) {
    return `${DRAFT_KEY}:${identityKey(identity)}:${encodeURIComponent(conversationId)}`;
}

function pendingKey(identity: AgentConversationIdentity, conversationId: string) {
    return `${PENDING_KEY}:${identityKey(identity)}:${encodeURIComponent(conversationId)}`;
}

function storage(identity: AgentConversationIdentity) {
    return localForageStorageForScope(identity.userScope || getActiveUserScope());
}

export async function loadAgentConversationDocument(identity: AgentConversationIdentity): Promise<AgentConversationDocument> {
    const value = await storage(identity).getItem(documentKey(identity));
    if (!value) return loadLegacyCanvasConversationDocument(identity);
    let parsed: unknown;
    try {
        parsed = JSON.parse(value);
    } catch {
        throw new Error("Agent 对话历史已损坏");
    }
    if (!isConversationDocument(parsed)) throw new Error("Agent 对话历史格式无效");
    return parsed;
}

export async function saveAgentConversationDocument(identity: AgentConversationIdentity, document: AgentConversationDocument) {
    await storage(identity).setItem(documentKey(identity), JSON.stringify({ ...document, version: 2 }));
}

export async function loadAgentConversationDraft(identity: AgentConversationIdentity, conversationId: string) {
    const value = await storage(identity).getItem(draftKey(identity, conversationId));
    return typeof value === "string" ? value : "";
}

export async function saveAgentConversationDraft(identity: AgentConversationIdentity, conversationId: string, draft: string) {
    if (!conversationId) return;
    await storage(identity).setItem(draftKey(identity, conversationId), draft);
}

export async function clearAgentConversationDraft(identity: AgentConversationIdentity, conversationId: string) {
    if (!conversationId) return;
    await storage(identity).removeItem(draftKey(identity, conversationId));
}

export async function loadAgentPendingSubmission(identity: AgentConversationIdentity, conversationId: string): Promise<AgentPendingSubmission | null> {
    const value = await storage(identity).getItem(pendingKey(identity, conversationId));
    if (value === null || value === undefined) return loadLegacyCanvasPendingSubmission(identity, conversationId);
    let parsed: unknown;
    try {
        parsed = JSON.parse(value);
    } catch {
        throw new Error("待确认请求记录损坏，已暂停发送；请先核对原运行");
    }
    if (!isPendingSubmission(parsed)) throw new Error("待确认请求记录损坏，已暂停发送；请先核对原运行");
    return parsed;
}

export async function saveAgentPendingSubmission(identity: AgentConversationIdentity, conversationId: string, pending: AgentPendingSubmission) {
    await storage(identity).setItem(pendingKey(identity, conversationId), JSON.stringify(pending));
}

export async function clearAgentPendingSubmission(identity: AgentConversationIdentity, conversationId: string) {
    await storage(identity).removeItem(pendingKey(identity, conversationId));
}

export const defaultAgentConversationStore: AgentConversationStore = {
    loadDocument: loadAgentConversationDocument,
    saveDocument: saveAgentConversationDocument,
    loadDraft: loadAgentConversationDraft,
    saveDraft: saveAgentConversationDraft,
    clearDraft: clearAgentConversationDraft,
    loadPending: loadAgentPendingSubmission,
    savePending: saveAgentPendingSubmission,
    clearPending: clearAgentPendingSubmission,
};

async function loadLegacyCanvasConversationDocument(identity: AgentConversationIdentity): Promise<AgentConversationDocument> {
    if (identity.scope.type !== "canvas") return emptyAgentConversationDocument();
    const value = await storage(identity).getItem(`cloud-agent-conversations-v1:${encodeURIComponent(identity.scope.id)}`);
    if (!value) return emptyAgentConversationDocument();
    return migrateLegacyCanvasConversationDocument(value, identity.scope.id);
}

export function migrateLegacyCanvasConversationDocument(value: string, canvasId: string): AgentConversationDocument {
    let parsed: unknown;
    try {
        parsed = JSON.parse(value);
    } catch {
        throw new Error("旧版 Agent 对话历史已损坏");
    }
    if (!isLegacyConversationDocument(parsed)) throw new Error("旧版 Agent 对话历史格式无效");
    if (parsed.conversations.some((conversation) => conversation.run && (conversation.run as AgentRunSnapshot).canvasId && (conversation.run as AgentRunSnapshot).canvasId !== canvasId)) {
        throw new Error("旧版 Agent 对话历史不属于当前画布");
    }
    const conversations = parsed.conversations.map((raw) => legacyConversation(raw));
    return { version: 2, activeId: parsed.activeId || conversations[0]?.id || null, conversations };
}

async function loadLegacyCanvasPendingSubmission(identity: AgentConversationIdentity, conversationId: string): Promise<AgentPendingSubmission | null> {
    if (identity.scope.type !== "canvas") return null;
    const value = await storage(identity).getItem(`cloud-agent-conversations-v1:pending:${encodeURIComponent(identity.scope.id)}:${encodeURIComponent(conversationId)}`);
    if (value === null || value === undefined) return null;
    return migrateLegacyCanvasPendingSubmission(value, identity.scope.id);
}

export function migrateLegacyCanvasPendingSubmission(value: string, canvasId: string): AgentPendingSubmission {
    let parsed: unknown;
    try {
        parsed = JSON.parse(value);
    } catch {
        throw new Error("旧版待确认请求记录损坏，已暂停发送；请先核对原运行");
    }
    if (!isLegacyPendingSubmission(parsed, canvasId)) throw new Error("旧版待确认请求记录损坏，已暂停发送；请先核对原运行");
    return parsed;
}

function isConversationDocument(value: unknown): value is AgentConversationDocument {
    if (!value || typeof value !== "object") return false;
    const candidate = value as Partial<AgentConversationDocument>;
    return (
        candidate.version === 2 &&
        (candidate.activeId === null || typeof candidate.activeId === "string") &&
        Array.isArray(candidate.conversations) &&
        candidate.conversations.every((conversation) => {
            if (!conversation || typeof conversation !== "object") return false;
            const item = conversation as Partial<AgentConversationDocument["conversations"][number]>;
            return (
                typeof item.id === "string" &&
                typeof item.title === "string" &&
                Array.isArray(item.messages) &&
                typeof item.createdAt === "string" &&
                typeof item.updatedAt === "string" &&
                ["read_only", "auto", "request_approval"].includes(item.permissionMode || "")
            );
        })
    );
}

function isPendingSubmission(value: unknown): value is AgentPendingSubmission {
    if (!value || typeof value !== "object") return false;
    const pending = value as Partial<AgentPendingSubmission>;
    if (typeof pending.fingerprint !== "string" || typeof pending.key !== "string" || pending.key.length < 8) return false;
    if (pending.request !== undefined) {
        if (!pending.request || pending.request.idempotencyKey !== pending.key || typeof pending.request.prompt !== "string") return false;
        if (!pending.request.scope && !pending.request.canvasId) return false;
    }
    if (pending.parentRunId !== undefined && typeof pending.parentRunId !== "string") return false;
    if (pending.messageId !== undefined && typeof pending.messageId !== "string") return false;
    if (pending.composerDraft !== undefined && typeof pending.composerDraft !== "string") return false;
    const delivery = pending.conversationMessage;
    if (delivery !== undefined) {
        if (
            !delivery ||
            !pending.request ||
            typeof delivery.conversationId !== "string" ||
            !delivery.conversationId ||
            (delivery.mode !== "interject" && delivery.mode !== "queue") ||
            typeof delivery.expectedRunId !== "string" ||
            delivery.expectedRunId !== pending.parentRunId ||
            (delivery.inheritRunConfiguration !== undefined && typeof delivery.inheritRunConfiguration !== "boolean") ||
            (delivery.replyToQuestionId !== undefined && (typeof delivery.replyToQuestionId !== "string" || delivery.mode !== "interject"))
        )
            return false;
    }
    return true;
}

type LegacyConversation = {
    archived?: boolean;
    id: string;
    serverConversationId?: string;
    runs?: unknown[];
    lastEventSeq?: number;
    historyState?: { status: "partial" | "unavailable"; message: string };
    draft?: string;
    contextUsage?: AgentContextUsage;
    reasoningMode?: AgentConversationRecord["reasoningMode"];
    sendMode?: AgentConversationRecord["sendMode"];
    storyboardDisabled?: boolean;
    title: string;
    messages: AgentConversationMessage[];
    run: unknown;
    model?: string;
    permissionMode: "read_only" | "auto" | "request_approval";
    skillIds?: string[];
    createdAt: string;
    updatedAt: string;
};

function isLegacyConversationDocument(value: unknown): value is { version: 1; activeId: string | null; conversations: LegacyConversation[] } {
    if (!value || typeof value !== "object") return false;
    const candidate = value as { version?: unknown; activeId?: unknown; conversations?: unknown };
    return (
        candidate.version === 1 &&
        (typeof candidate.activeId === "string" || candidate.activeId === null) &&
        Array.isArray(candidate.conversations) &&
        candidate.conversations.every((raw) => {
            if (!raw || typeof raw !== "object") return false;
            const item = raw as Partial<LegacyConversation>;
            return (
                typeof item.id === "string" &&
                typeof item.title === "string" &&
                Array.isArray(item.messages) &&
                typeof item.createdAt === "string" &&
                typeof item.updatedAt === "string" &&
                ["read_only", "auto", "request_approval"].includes(item.permissionMode || "")
            );
        })
    );
}

function legacyConversation(value: LegacyConversation): AgentConversationRecord {
    const run = value.run && typeof value.run === "object" ? (value.run as AgentRunSnapshot) : null;
    const runs = Array.isArray(value.runs) ? value.runs.filter((item): item is AgentRunSnapshot => Boolean(item && typeof item === "object")) : undefined;
    const eventCursors: Record<string, number> = {};
    for (const item of [...(runs || []), ...(run ? [run] : [])]) {
        if (item.id) eventCursors[item.id] = Math.max(item.latestSeq || 0, ...(item.events || []).filter((event) => event.runId === item.id).map((event) => event.seq));
    }
    return {
        id: value.id,
        serverConversationId: value.serverConversationId || run?.conversationId,
        runs,
        lastEventSeq: value.lastEventSeq,
        eventCursors,
        historyState: value.historyState,
        draft: value.draft,
        contextUsage: value.contextUsage,
        reasoningMode: value.reasoningMode,
        sendMode: value.sendMode,
        title: value.title,
        messages: value.messages,
        run,
        permissionMode: value.permissionMode,
        model: value.model,
        skillIds: value.skillIds,
        adapterData: value.storyboardDisabled === true ? { storyboardDisabled: true } : undefined,
        archived: value.archived,
        createdAt: value.createdAt,
        updatedAt: value.updatedAt,
    };
}

function isLegacyPendingSubmission(value: unknown, canvasId: string): value is AgentPendingSubmission {
    if (!isPendingSubmission(value)) return false;
    if (value.request?.canvasId && value.request.canvasId !== canvasId) return false;
    if (value.request?.scope && (value.request.scope.type !== "canvas" || value.request.scope.id !== canvasId)) return false;
    return true;
}
