import type { AgentApprovalSnapshot, AgentEvent, AgentRunSnapshot } from "@/sdk/agent";
import type { AgentChatMessage } from "@/lib/agent/chat-types";
import type { AgentConversationView } from "@/services/agent/conversation-types";
import { agentRunErrorMessage } from "./agent-error-presentation";
import { mergeAgentMediaBatchProgress } from "./cloud-agent-media-batch";
import { agentToolRetry, mergeAgentToolRetry } from "./agent-tool-retry";
import { nativeSkillEventPresentation } from "./agent-tool-presentation";
import { reduceAgentRunState } from "./run-state";

const TERMINAL = new Set<AgentRunSnapshot["status"]>(["completed", "failed", "cancelled", "rejected"]);

function appendUnique(messages: AgentChatMessage[], message: AgentChatMessage) {
    return messages.some((item) => item.id === message.id) ? messages : [...messages, message];
}

function upsertText(messages: AgentChatMessage[], message: AgentChatMessage, append = false) {
    const index = messages.findIndex((item) => item.id === message.id);
    if (index < 0) return [...messages, message];
    const previous = messages[index];
    if (!append && previous.text === message.text && previous.streaming === message.streaming && previous.final === message.final) return messages;
    const next = [...messages];
    next[index] = { ...previous, ...message, text: append ? `${previous.text}${message.text}` : message.text };
    return next;
}

function upsertMessage(messages: AgentChatMessage[], message: AgentChatMessage) {
    const index = messages.findIndex((item) => item.id === message.id);
    if (index < 0) return [...messages, message];
    const next = [...messages];
    next[index] = message;
    return next;
}

function record(value: unknown): Record<string, unknown> {
    return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function permissionLabel(value: unknown) {
    return { read_only: "只读", request_approval: "请求审批", auto: "自动执行" }[String(value)] || String(value || "未知");
}

function permissionMode(value: unknown) {
    return value === "read_only" || value === "request_approval" || value === "auto" ? value : null;
}

function toolDetailNodeIds(detail: unknown): Set<string> {
    const payload = record(detail);
    const ids = new Set<string>();
    for (const value of [payload.nodeId, record(payload.result).nodeId]) {
        if (typeof value === "string" && value) ids.add(value);
    }
    if (Array.isArray(payload.actions)) {
        for (const action of payload.actions) {
            const nodeId = record(action).nodeId;
            if (typeof nodeId === "string" && nodeId) ids.add(nodeId);
        }
    }
    if (typeof payload.arguments === "string") {
        try {
            const args = record(JSON.parse(payload.arguments));
            if (Array.isArray(args.ops)) {
                for (const op of args.ops) {
                    const nodeId = record(op).id;
                    if (typeof nodeId === "string" && nodeId) ids.add(nodeId);
                }
            }
        } catch {
            // Tool arguments are diagnostic data; malformed values must not break playback.
        }
    }
    return ids;
}

function toolDetailTaskIds(detail: unknown): Set<string> {
    const payload = record(detail);
    const ids = new Set<string>();
    for (const value of [payload.taskId, record(payload.result).taskId]) {
        if (typeof value === "string" && value) ids.add(value);
    }
    return ids;
}

function mergeToolDetails(previous: unknown, next: unknown): Record<string, unknown> {
    const previousDetail = record(previous);
    const nextDetail = record(next);
    return {
        ...previousDetail,
        ...nextDetail,
        actions: Array.isArray(nextDetail.actions) ? nextDetail.actions : previousDetail.actions,
        arguments: nextDetail.arguments || previousDetail.arguments,
    };
}

function upsertMediaToolTrace(messages: AgentChatMessage[], message: AgentChatMessage) {
    const nextNodeIds = toolDetailNodeIds(message.detail);
    const nextTaskIds = toolDetailTaskIds(message.detail);
    const index = messages.findIndex((item) => {
        if (item.role !== "tool") return false;
        if (item.title !== "canvas_apply_ops" && item.title !== "generate_media") return false;
        const itemNodeIds = toolDetailNodeIds(item.detail);
        const itemTaskIds = toolDetailTaskIds(item.detail);
        return [...nextNodeIds].some((id) => itemNodeIds.has(id)) || [...nextTaskIds].some((id) => itemTaskIds.has(id));
    });
    if (index < 0) return appendUnique(messages, message);
    const next = [...messages];
    const previous = next[index];
    next[index] = {
        ...previous,
        ...message,
        id: previous.id,
        detail: mergeToolDetails(previous.detail, message.detail),
    };
    return next;
}

function contextCompactionNotice(id: string, text: string, meta: string | undefined, detail: Record<string, unknown>, streaming: boolean): AgentChatMessage {
    return { id, role: "system", text, streaming, meta, detail: { ...detail, eventType: "context_compaction" } };
}

/**
 * Project a server event into the common conversation state. This is the
 * non-React equivalent of the former canvas event reducer: business adapters
 * only handle side effects such as canvas focus and media insertion.
 */
export function applyAgentConversationEvent(view: AgentConversationView, event: AgentEvent): AgentConversationView {
    const payload = event.payload || {};
    const text = String(payload.text || payload.summary || payload.message || "");
    let messages = view.messages;
    let run = view.run;
    let approval = view.approval;

    if (event.type === "media_batch_progress") {
        const id = `media-batch-${event.runId}-${String(payload.batchId || "")}`;
        const previous = messages.find((message) => message.id === id);
        const mediaBatch = mergeAgentMediaBatchProgress(previous?.mediaBatch, payload);
        if (mediaBatch) messages = upsertMessage(messages, { id, role: "tool", text: "", runId: event.runId, mediaBatch });
        return { messages, run, approval };
    }

    const nativeSkill = nativeSkillEventPresentation(event.type, payload);
    if (nativeSkill) {
        messages = appendUnique(messages, { id: event.eventId, role: "tool", ...nativeSkill });
        return { messages, run, approval };
    }

    if (["run_status", "context_compaction_requested", "context_compacted"].includes(event.type)) run = reduceAgentRunState(run, event);
    if (event.type === "run_status") {
        const snapshotApproval = payload.approval && typeof payload.approval === "object" ? (payload.approval as AgentApprovalSnapshot) : null;
        approval = snapshotApproval && !snapshotApproval.decision ? snapshotApproval : null;
        if (payload.failureMessage) messages = appendUnique(messages, agentRunErrorMessage(event));
        if (run && TERMINAL.has(run.status)) {
            messages = messages.map((message) => (message.id === `plan-${event.runId}` ? { ...message, planTerminal: true, streaming: false } : message));
        }
        return { messages, run, approval };
    }
    if (event.type === "approval_requested") {
        approval = { approvalId: String(payload.approvalId || "approval"), ...payload } as AgentApprovalSnapshot;
        return { messages, run, approval };
    }
    if (event.type === "approval_decided") {
        approval = null;
        if (payload.decision === "reject") messages = appendUnique(messages, { id: event.eventId, role: "system", text: text || "已拒绝本次操作，未写入画布。你可以告诉 Agent 修改方向后重新申请。", runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "permission_changed") {
        const to = permissionMode(payload.to);
        if (to && run?.id === event.runId) run = { ...run, permissionMode: to };
        messages = appendUnique(messages, {
            id: event.eventId,
            role: "system",
            text: `执行权限已从「${permissionLabel(payload.from)}」改为「${permissionLabel(payload.to)}」，从下一次工具调用起生效`,
            detail: { ...payload, eventType: event.type },
            runId: event.runId,
        });
        return { messages, run, approval };
    }
    if (event.type === "permission_notice_delivered" || event.type === "generation_notice_delivered") return { messages, run, approval };
    if (event.type === "generation_settled") {
        const title = String(payload.title || "") || (payload.mode === "video" ? "视频" : payload.mode === "audio" ? "音频" : "图片");
        const summary = String(payload.summary || "生成已结束");
        const error = typeof payload.error === "string" && payload.error ? `：${payload.error}` : "";
        messages = appendUnique(messages, { id: event.eventId, role: "system", text: `生成结果 · ${title}${summary ? ` ${summary}` : ""}${error}`, detail: { ...payload, eventType: event.type }, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "progress_summary") {
        messages = appendUnique(messages, { id: event.eventId, role: "system", text: text || "Agent 正在整理执行计划", runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "canvas_contention") {
        messages = appendUnique(messages, { id: event.eventId, role: "system", text: "画布一直在变化，Agent 已暂停等你", detail: { ...payload, eventType: event.type }, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "canvas_undone" && payload.scope === "run") {
        messages = appendUnique(messages, { id: `undone-${event.runId}`, role: "system", text: "已撤销本次修改", detail: { ...payload, eventType: event.type }, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "context_compaction_requested") {
        const turnCount = Number(payload.turnCount ?? payload.turns ?? 0);
        const ratio = Number(payload.pressureRatio || 0);
        const projected = Number(payload.projectedTokens || 0);
        const usable = Number(payload.usableInputTokens || 0);
        const byTokens = payload.basis === "tokens" && usable > 0;
        messages = upsertMessage(
            messages,
            contextCompactionNotice(
                `${event.runId}:context-compaction:${turnCount}`,
                byTokens
                    ? `上下文已用到模型上限的 ${Math.round(ratio * 100)}%，已暂停本轮执行并压缩历史（保留用户提示摘要、操作记录、未完成任务、当前工作、设计决策、限制和偏好），压完会从压缩后的上下文继续。`
                    : "上下文已超过服务端压缩阈值，已暂停本轮执行并压缩历史（保留用户提示摘要、操作记录、未完成任务、当前工作、设计决策、限制和偏好），压完会从压缩后的上下文继续。",
                [byTokens ? `${projected.toLocaleString("zh-CN")} / ${usable.toLocaleString("zh-CN")} Token · 阈值 ${Math.round(Number(payload.thresholdRatio || 0.85) * 100)}%` : undefined, turnCount > 0 ? `已整理前 ${turnCount} 轮对话` : undefined]
                    .filter(Boolean)
                    .join(" · ") || undefined,
                { ...payload, status: "running" },
                true,
            ),
        );
        return { messages, run, approval };
    }
    if (event.type === "context_compacted") {
        const turnCount = Number(payload.compactedTurnCount || 0);
        const fallback = payload.mode === "fallback";
        const historyMessages = Number(payload.historyMessages || 0);
        messages = upsertMessage(
            messages,
            contextCompactionNotice(
                `${event.runId}:context-compaction:${turnCount}`,
                fallback ? "模型压缩不可用，已使用服务端保底检查点完成整理，后续工作可以继续。" : "已生成结构化检查点并保留最近对话，后续工作将从压缩后的上下文继续。",
                `${turnCount > 0 ? `已整理 ${turnCount} 轮对话` : "上下文已整理"}${historyMessages > 0 ? ` · 保留 ${historyMessages} 条上下文消息` : ""}`,
                { ...payload, status: "completed" },
                false,
            ),
        );
        return { messages, run, approval };
    }
    if (event.type === "assistant_delta") {
        messages = upsertText(messages, { id: String(payload.messageId || `assistant:${event.runId}`), role: "assistant", text, streaming: true, runId: event.runId }, true);
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
        messages = upsertText(messages, { id, role: "assistant", text, reasoning: true, streaming: event.type === "reasoning_delta", runId: event.runId }, event.type === "reasoning_delta");
        return { messages, run, approval };
    }
    if (event.type === "plan_updated" && Array.isArray(payload.items)) {
        const id = `plan-${event.runId}`;
        messages = upsertMessage(messages, { id, role: "tool", text: "", planItems: payload.items as AgentChatMessage["planItems"], runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "user_question" && typeof payload.question === "string") {
        const options = Array.isArray(payload.options)
            ? (payload.options as Array<{ label?: unknown; detail?: unknown }>).map((option) => ({ label: String(option?.label || "").trim(), detail: option?.detail === undefined ? undefined : String(option.detail) })).filter((option) => option.label)
            : [];
        if (options.length >= 2) {
            const questionId = `${event.runId}:${event.seq || event.eventId}`;
            messages = upsertMessage(messages, {
                id: questionId,
                role: "assistant",
                text: "",
                question: { id: questionId, question: payload.question, options, allowFreeform: payload.allowFreeform !== false },
                runId: event.runId,
            });
        }
        return { messages, run, approval };
    }
    if (event.type === "completion_blocked") {
        const attempt = Number(payload.attempt || 0);
        const maxAttempts = Number(payload.maxAttempts || 0);
        messages = appendUnique(messages, {
            id: `completion-blocked-${event.runId}:${event.seq ?? event.eventId}`,
            role: "system",
            text: text || "本次收尾尚有协议或交付阻塞。",
            meta: attempt > 0 && maxAttempts > 0 ? `第 ${attempt}/${maxAttempts} 次` : undefined,
            runId: event.runId,
        });
        return { messages, run, approval };
    }
    if (event.type === "canvas_updated" && Array.isArray(payload.actions)) {
        if (payload.operation === "generate_media_submit" || payload.operation === "generate_media_complete") return { messages, run, approval };
        const { canvasPatch: _patch, ...detail } = payload;
        const id = payload.callId ? `canvas-${event.runId}-${payload.callId}` : event.eventId;
        messages = appendUnique(messages, { id, role: "tool", title: "canvas_apply_ops", text: "画布操作已完成", detail: { ...detail, eventType: event.type }, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type.startsWith("tool_") && agentToolRetry(payload)) {
        const message: AgentChatMessage = { id: event.eventId, role: "tool", title: String(payload.toolName || "工具执行"), text, detail: { ...payload, eventType: event.type }, runId: event.runId };
        messages = mergeAgentToolRetry(messages, message);
        if (event.type === "tool_failed") return { messages, run, approval };
    }
    if (event.type === "director_preview_requested") {
        messages = appendUnique(messages, { id: event.eventId, role: "tool", title: "director", text: "等待导演台录制预演", detail: { ...payload, eventType: event.type }, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "tool_completed" && payload.toolName === "canvas_apply_ops" && payload.callId) {
        const id = `canvas-${event.runId}-${payload.callId}`;
        messages = appendUnique(messages, { id, role: "tool", title: "canvas_apply_ops", text: text || "画布操作已完成", detail: { ...payload, eventType: event.type }, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "memory_updated") {
        const revisionId = String(payload.revisionId || "");
        const changes = Array.isArray(payload.changes)
            ? (payload.changes as Array<Record<string, unknown>>).map((change) => ({
                  kind: (["add", "replace", "remove"].includes(String(change.kind)) ? String(change.kind) : "add") as "add" | "replace" | "remove",
                  section: String(change.section || ""),
                  before: change.before === undefined ? undefined : String(change.before),
                  after: change.after === undefined ? undefined : String(change.after),
              }))
            : [];
        const memoryUpdate = { revisionId, status: payload.status === "pending_review" ? ("pending" as const) : ("applied" as const), summary: String(payload.summary || ""), changes };
        messages = appendUnique(messages, { id: `memory-${revisionId || event.eventId}`, role: "system", text: text || "长期记忆已更新", memoryUpdate, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "tool_completed" && payload.toolName === "memory_update") return { messages, run, approval };
    if (event.type === "generation_task_created") {
        messages = upsertMediaToolTrace(messages, { id: event.eventId, role: "tool", title: "generate_media", text: text || event.type, detail: { ...payload, eventType: event.type }, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "tool_pending") {
        const id = `tool-${event.runId}-${payload.callId || event.eventId}`;
        messages = upsertMessage(messages, {
            id,
            role: "tool",
            title: String(payload.toolName || "工具执行"),
            text: text || "工具已接单，正在后台执行",
            detail: { ...payload, eventType: event.type },
            runId: event.runId,
        });
        return { messages, run, approval };
    }
    if (event.type.startsWith("tool_")) {
        const message: AgentChatMessage = {
            id: event.eventId,
            role: "tool",
            title: String(payload.toolName || payload.title || "工具执行"),
            text: text || event.type,
            detail: { ...payload, eventType: event.type },
            runId: event.runId,
        };
        if (payload.toolName === "generate_media") {
            messages = upsertMediaToolTrace(messages, message);
            return { messages, run, approval };
        }
        const pendingId = payload.callId ? `tool-${event.runId}-${payload.callId}` : "";
        messages = pendingId && messages.some((item) => item.id === pendingId) ? upsertMessage(messages, { ...message, id: pendingId }) : appendUnique(messages, message);
        return { messages, run, approval };
    }
    if (event.type === "model_failure_recovered") {
        messages = appendUnique(messages, { id: event.eventId, role: "tool", title: "模型响应自动重试", text: text || "已关闭思考并重试同一步", detail: { ...payload, eventType: event.type }, runId: event.runId });
        return { messages, run, approval };
    }
    if (event.type === "run_failed" || event.type === "error") {
        messages = appendUnique(messages, agentRunErrorMessage(event));
        return { messages, run, approval };
    }
    return { messages, run, approval };
}

export function pendingAgentQuestion(messages: AgentChatMessage[], awaitingQuestionId?: string) {
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
