import type { AgentApprovalSnapshot, AgentEvent, AgentRunSnapshot } from "@/sdk/agent";
import type { AgentConversationMessage, AgentConversationView } from "@/services/agent/conversation-types";
import { reduceAgentRunState } from "./run-state";

function appendUnique(messages: AgentConversationMessage[], message: AgentConversationMessage) {
    return messages.some((item) => item.id === message.id) ? messages : [...messages, message];
}

function upsertText(messages: AgentConversationMessage[], message: AgentConversationMessage) {
    const index = messages.findIndex((item) => item.id === message.id);
    if (index < 0) return [...messages, message];
    const next = [...messages];
    next[index] = { ...next[index], ...message };
    return next;
}

export function applyAgentConversationEvent(view: AgentConversationView, event: AgentEvent): AgentConversationView {
    const payload = event.payload || {};
    const text = String(payload.text || payload.summary || payload.message || "");
    let messages = view.messages;
    let run = view.run;
    let approval = view.approval;

    if (["run_status", "context_compaction_requested", "context_compacted"].includes(event.type)) run = reduceAgentRunState(run, event);
    if (event.type === "run_status") {
        const snapshotApproval = payload.approval && typeof payload.approval === "object" ? (payload.approval as AgentApprovalSnapshot) : null;
        approval = snapshotApproval && !snapshotApproval.decision ? snapshotApproval : null;
        if (payload.failureMessage) {
            messages = appendUnique(messages, {
                id: `run-error-${event.runId}-${event.seq || event.eventId}`,
                role: "error",
                text: String(payload.failureMessage),
                runId: event.runId,
            });
        }
        return { messages, run, approval };
    }
    if (event.type === "assistant_delta") {
        messages = upsertText(messages, { id: String(payload.messageId || `assistant:${event.runId}`), role: "assistant", text, streaming: true, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "assistant_message" || event.type === "assistant_snapshot") {
        messages = upsertText(messages, {
            id: String(payload.messageId || `assistant:${event.runId}:${event.seq || event.eventId}`),
            role: "assistant",
            text,
            streaming: false,
            final: payload.final !== false,
            runId: event.runId,
        });
        return { messages, run, approval };
    }
    if (event.type === "reasoning_delta" || event.type === "reasoning_message") {
        const id = String(payload.messageId || `${event.runId}:reasoning`);
        messages = upsertText(messages, { id, role: "assistant", text, reasoning: true, streaming: event.type === "reasoning_delta", runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "plan_updated" && Array.isArray(payload.items)) {
        const id = `plan-${event.runId}`;
        messages = upsertText(messages, { id, role: "tool", text: "", planItems: payload.items as AgentConversationMessage["planItems"], runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "user_question" && typeof payload.question === "string") {
        const options = Array.isArray(payload.options)
            ? (payload.options as Array<{ label?: unknown; detail?: unknown }>)
                  .map((option) => ({ label: String(option?.label || "").trim(), detail: option?.detail === undefined ? undefined : String(option.detail) }))
                  .filter((option) => option.label)
            : [];
        if (options.length >= 2) {
            const questionId = `${event.runId}:${event.seq || event.eventId}`;
            messages = appendUnique(messages, {
                id: questionId,
                role: "assistant",
                text: "",
                question: { id: questionId, question: payload.question, options, allowFreeform: payload.allowFreeform !== false },
                runId: event.runId,
            });
        }
        return { messages, run, approval };
    }
    if (event.type === "approval_requested") {
        approval = payload as AgentApprovalSnapshot;
        return { messages, run, approval };
    }
    if (event.type === "approval_decided") {
        approval = null;
        if (payload.decision === "reject") messages = appendUnique(messages, { id: event.eventId, role: "system", text: text || "已拒绝本次操作", runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "user_interjection" && text) {
        messages = appendUnique(messages, { id: String(payload.messageId || event.eventId), role: "user", text, interjection: "sent", runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "user_interjection_delivered") {
        const id = String(payload.messageId || "");
        messages = messages.map((message) => (message.id === id && message.interjection === "sent" ? { ...message, interjection: "delivered" } : message));
        return { messages, run, approval };
    }
    if (event.type === "run_failed" || event.type === "error") {
        messages = appendUnique(messages, { id: event.eventId, role: "error", text: text || "Agent 运行失败", runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type.startsWith("tool_")) {
        messages = appendUnique(messages, {
            id: event.eventId,
            role: "tool",
            title: String(payload.toolName || "工具执行"),
            text: text || event.type,
            detail: { ...payload, eventType: event.type },
            runId: event.runId,
        });
        return { messages, run, approval };
    }
    if (event.type === "completion_blocked") {
        messages = appendUnique(messages, { id: event.eventId, role: "system", text: text || "本次收尾尚有协议或交付阻塞。", runId: event.runId });
        return { messages, run, approval };
    }
    return { messages, run, approval };
}

export function pendingAgentQuestion(messages: AgentConversationMessage[], awaitingQuestionId?: string) {
    if (!awaitingQuestionId) return undefined;
    return [...messages].reverse().find((message) => message.question?.id === awaitingQuestionId)?.question;
}

export function projectAgentConversation(conversationId: string, runs: AgentRunSnapshot[]): AgentConversationView {
    const ordered = [...runs].sort((a, b) => a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id));
    let view: AgentConversationView = { messages: [], run: null, approval: null };
    for (const source of ordered) {
        if (source.conversationId !== conversationId) continue;
        if (source.userPrompt) view.messages = appendUnique(view.messages, { id: `user-${source.id}`, role: "user", text: source.userPrompt, runId: source.id });
        const seen = new Set<number>();
        for (const event of [...(source.events || [])].sort((a, b) => a.seq - b.seq)) {
            if (!Number.isSafeInteger(event.seq) || event.seq <= 0 || seen.has(event.seq) || event.runId !== source.id) continue;
            seen.add(event.seq);
            view = applyAgentConversationEvent(view, event);
        }
        view.run = source;
    }
    return view;
}
