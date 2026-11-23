import type { AgentReference } from "@/lib/agent/reference";
import type { CloudAgentMediaBatchProgress } from "@/lib/agent/cloud-agent-media-batch";
import type { AgentAttachmentStatus } from "@/lib/agent/agent-attachments";

export type AgentPlanItem = { id: string; title: string; status: "pending" | "doing" | "done" };

export type AgentMemoryUpdate = {
    revisionId: string;
    status: "applied" | "pending";
    summary: string;
    changes: Array<{ kind: "add" | "replace" | "remove"; section: string; before?: string; after?: string }>;
};

export type AgentUserQuestion = {
    id?: string;
    question: string;
    options: Array<{ label: string; detail?: string }>;
    allowFreeform?: boolean;
};

export type AgentChatAttachment = {
    id: string;
    name: string;
    url: string;
    nodeId?: string;
    kind?: "image" | "text" | "video" | "audio";
    status?: AgentAttachmentStatus | "missing";
    progress?: number;
    error?: string;
    retryable?: boolean;
};

export type AgentChatMessage = {
    id: string;
    role: "user" | "assistant" | "system" | "tool" | "error";
    title?: string;
    text: string;
    errorSeverity?: "warning" | "error";
    streaming?: boolean;
    reasoning?: boolean;
    final?: boolean;
    planItems?: AgentPlanItem[];
    mediaBatch?: CloudAgentMediaBatchProgress;
    planTerminal?: boolean;
    question?: AgentUserQuestion;
    meta?: string;
    runId?: string;
    detail?: unknown;
    attachments?: AgentChatAttachment[];
    canvasReferenceNodeId?: string;
    interjection?: "sent" | "delivered" | "undelivered" | "withdrawn";
    memoryUpdate?: AgentMemoryUpdate;
    reference?: AgentReference;
};
