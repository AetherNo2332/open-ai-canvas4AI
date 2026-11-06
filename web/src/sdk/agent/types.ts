/**
 * Agent 服务 SDK 的类型：与后端 /api/agent/v2 合同一致。
 * 会话范围（scope）创建后不可变；消息的发送者身份由服务端写入，客户端只给消息 ID 与正文。
 */

export type Scope = { application: string; spaceType: string; spaceId: string };

export type SessionView = {
    id: string;
    definitionId: string;
    definitionVersion: string;
    role: "business" | "supervisor";
    scope: Scope;
    workItemId?: string;
    conversationId?: string;
    title: string;
    status: "active" | "archived";
    revision: number;
    activeRunId?: string;
    createdAt: string;
    updatedAt: string;
    archivedAt?: string;
};

export type SessionPage = { sessions: SessionView[]; nextCursor?: string };

export type DefinitionView = { id: string; version: string; role: string; application: string; spaceTypes: string[]; title: string; tools: string[] };

export type SessionMessage = {
    sessionId: string;
    messageId: string;
    sequence: number;
    kind: "message" | "summary" | "tool_call" | "tool_result";
    actor: "user" | "supervisor" | "executor" | "system";
    role: string;
    text: string;
    createdAt: string;
};

export type MessagePage = { messages: SessionMessage[]; nextAfter: number };

export type MessageReceipt = { accepted: boolean; workItemId?: string; workVersion?: number };

export type ApprovalView = { id: string; workItemId: string; sessionId: string; summary: string; quoteMicrocredits: number; authorizationVersion: number; status: "pending" | "approved" | "rejected" | "consumed" | "expired" };

export type WorkView = {
    id: string;
    parentWorkId?: string;
    rootWorkId: string;
    goal: string;
    status: "pending" | "running" | "waiting_user" | "paused" | "completed" | "failed" | "cancelled";
    version: number;
    supervisorSessionId?: string;
    scope: Scope;
    budgetGrantId: string;
    activeAssignmentId?: string;
    resultSummary?: string;
};

export type ReportView = { reportId: string; assignmentId: string; status: string; summary: string; applied: boolean; createdAt: string };

/** 费用视图：预算、实收、在途、待核对与退回分开显示，待核对不算作 0。 */
export type BudgetView = { limit: number; available: number; charged: number; inFlight: number; pendingReview: number; refunded: number; status: "open" | "closed" };

export type AgentErrorCode =
    "invalid_request" | "unauthorized" | "forbidden" | "not_found" | "scope_mismatch" | "session_conflict" | "stale_lease" | "idempotency_conflict" | "work_stopped" | "stale_assignment" | "budget_exceeded" | "version_conflict" | "internal" | "transport";

export type AgentError = { code: AgentErrorCode; message: string; retryable: boolean; httpStatus?: number };
