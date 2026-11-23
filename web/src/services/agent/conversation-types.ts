import type {
    AgentApprovalSnapshot,
    AgentBusinessScope,
    AgentMessageInput,
    AgentPermissionMode,
    AgentRunRequest,
    AgentRunSnapshot,
} from "@/sdk/agent";

export type AgentConversationMessage = {
    id: string;
    role: "user" | "assistant" | "system" | "tool" | "error";
    title?: string;
    text: string;
    streaming?: boolean;
    reasoning?: boolean;
    final?: boolean;
    planItems?: Array<{ id: string; title: string; status: "pending" | "doing" | "done" }>;
    question?: { id?: string; question: string; options: Array<{ label: string; detail?: string }>; allowFreeform?: boolean };
    meta?: string;
    runId?: string;
    detail?: unknown;
    interjection?: "sent" | "delivered" | "undelivered" | "withdrawn";
};

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
    skillIds?: string[];
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
