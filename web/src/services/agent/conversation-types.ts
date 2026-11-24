import type { AgentApprovalSnapshot, AgentBusinessScope, AgentMessageInput, AgentPermissionMode, AgentRunRequest, AgentRunSnapshot } from "@/sdk/agent";
import type { AgentContextUsage } from "@/lib/agent/context-usage";
import type { AgentChatMessage } from "@/lib/agent/chat-types";

export type AgentConversationMessage = AgentChatMessage;

export type AgentConversationHistoryState = {
    status: "partial" | "unavailable";
    message: string;
};

export type AgentConversationRecord = {
    archived?: boolean;
    id: string;
    serverConversationId?: string;
    runs?: AgentRunSnapshot[];
    lastEventSeq?: number;
    eventCursors?: Record<string, number>;
    historyState?: AgentConversationHistoryState;
    draft?: string;
    title: string;
    messages: AgentConversationMessage[];
    run: AgentRunSnapshot | null;
    permissionMode: AgentPermissionMode;
    model?: string;
    reasoningMode?: NonNullable<AgentRunRequest["reasoningMode"]>;
    skillIds?: string[];
    contextUsage?: AgentContextUsage;
    sendMode?: AgentMessageInput["mode"];
    createdAt: string;
    updatedAt: string;
    /** Canvas-only presentation metadata belongs to the canvas adapter. */
    adapterData?: Record<string, unknown>;
};

export type AgentConversationDocument = {
    version: 2;
    activeId: string | null;
    conversations: AgentConversationRecord[];
};

export type AgentPendingSubmission = {
    fingerprint: string;
    key: string;
    request?: AgentRunRequest;
    parentRunId?: string;
    messageId?: string;
    composerDraft?: string;
    conversationMessage?: { conversationId: string } & Omit<AgentMessageInput, "request">;
};

export type AgentConversationIdentity = {
    userScope: string;
    scope: AgentBusinessScope;
    agentDefinitionId: string;
};

export type AgentConversationView = {
    messages: AgentConversationMessage[];
    run: AgentRunSnapshot | null;
    approval: AgentApprovalSnapshot | null;
};

export function emptyAgentConversationDocument(): AgentConversationDocument {
    return { version: 2, activeId: null, conversations: [] };
}
