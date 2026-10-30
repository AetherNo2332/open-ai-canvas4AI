import type { Dispatch, SetStateAction } from "react";
import type { AgentEvent, AgentRun } from "@/services/api/agent";
import { reduceAgentRun } from "@/lib/canvas/agent-run-state";
import { agentRunErrorMessage } from "@/lib/canvas/agent-error-presentation";
import { agentToolRetry, mergeAgentToolRetry } from "@/lib/canvas/agent-tool-retry";
import { nativeSkillEventPresentation } from "@/lib/canvas/agent-tool-presentation";
import { agentRunUndoneMessageId } from "@/lib/canvas/agent-run-undo";
import { agentPermissionChangedText, agentPermissionModeValue } from "@/lib/canvas/agent-permission-switch";
import { agentAssistantFinality, agentControlMessage, type CloudAgentChatMessage, type CloudAgentPlanItem } from "@/components/canvas/canvas-cloud-agent-chat-ui";

type ApprovalState = { approvalId: string; detail: Record<string, unknown>; reason: string };

export function applyAgentEvent(
    event: AgentEvent,
    setMessages: Dispatch<SetStateAction<CloudAgentChatMessage[]>>,
    setRun: Dispatch<SetStateAction<AgentRun | null>>,
    setApproval: Dispatch<SetStateAction<ApprovalState | null>>,
    setPrompt?: Dispatch<SetStateAction<string>>,
) {
    const payload = event.payload || {};
    const text = String(payload.text || payload.summary || payload.message || "");
    const nativeSkill = nativeSkillEventPresentation(event.type, payload);
    if (nativeSkill) {
        setMessages((current) => appendUniqueMessage(current, { id: event.eventId, role: "tool", ...nativeSkill }));
        return;
    }
    if (["run_status", "context_compaction_requested", "context_compacted"].includes(event.type)) setRun((current) => reduceAgentRun(current, event));
    if (event.type === "run_status") {
        const snapshotApproval = payload.approval && typeof payload.approval === "object" ? (payload.approval as AgentRun["approval"]) : undefined;
        const nextStatus = String(payload.status || "") as AgentRun["status"];
        const terminal = ["completed", "failed", "cancelled", "rejected"].includes(nextStatus);
        if (terminal) {
            setMessages((current) => current.map((message) => (message.id === `plan-${event.runId}` && message.planItems?.length ? { ...message, planTerminal: true, streaming: false } : message)));
        }
        if (payload.failureMessage) setMessages((current) => appendUniqueMessage(current, agentRunErrorMessage(event)));
        if (snapshotApproval && !snapshotApproval.decision && snapshotApproval.approvalId) {
            setApproval((current) => ({ approvalId: snapshotApproval.approvalId, detail: snapshotApproval, reason: current?.approvalId === snapshotApproval.approvalId ? current.reason : snapshotApproval.reason || "" }));
        } else {
            setApproval(null);
        }
        return;
    }
    if (event.type === "approval_decided") {
        setApproval(null);
        if (payload.decision === "reject") {
            setMessages((current) =>
                appendUniqueMessage(current, {
                    id: event.eventId,
                    role: "system",
                    text: text || "已拒绝本次操作，未写入画布。你可以告诉 Agent 修改方向后重新申请。",
                }),
            );
        }
        return;
    }
    if (event.type === "permission_changed") {
        // 运行中切换权限：时间线留一行，并同步运行快照（其他标签页由面板据此更新控件）。
        const to = agentPermissionModeValue(payload.to);
        if (to) setRun((current) => (current && current.id === event.runId ? { ...current, permissionMode: to } : current));
        setMessages((current) => appendUniqueMessage(current, { id: event.eventId, role: "system", text: agentPermissionChangedText(payload.from, payload.to), detail: { ...payload, eventType: event.type } }));
        return;
    }
    if (event.type === "permission_notice_delivered") return;
    if (event.type === "progress_summary") {
        setMessages((current) => appendUniqueMessage(current, { id: event.eventId, role: "system", text: text || "Agent 正在整理执行计划" }));
        return;
    }
    if (event.type === "canvas_contention") {
        // 用户与 Agent 同时编辑：画布持续变化，Agent 暂停询问用户。这是状态提示，不是错误。
        setMessages((current) => appendUniqueMessage(current, { id: event.eventId, role: "system", text: "画布一直在变化，Agent 已暂停等你", detail: { ...payload, eventType: event.type } }));
        return;
    }
    if (event.type === "canvas_undone" && payload.scope === "run") {
        setMessages((current) => appendUniqueMessage(current, { id: agentRunUndoneMessageId(event.runId), role: "system", text: "已撤销本次修改", detail: { ...payload, eventType: event.type } }));
        return;
    }
    if (event.type === "context_compaction_requested") {
        const turnCount = Number(payload.turnCount ?? payload.turns ?? 0);
        const ratio = Number(payload.pressureRatio || 0);
        const projected = Number(payload.projectedTokens || 0);
        const usable = Number(payload.usableInputTokens || 0);
        const byTokens = payload.basis === "tokens" && usable > 0;
        setMessages((current) =>
            upsertContextCompactionNotice(current, {
                id: `${event.runId}:context-compaction:${turnCount}`,
                role: "system",
                // 触发时步进循环是暂停的：文案要说清"暂停 + 为什么 + 压完会继续"。
                text: byTokens
                    ? `上下文已用到模型上限的 ${Math.round(ratio * 100)}%，已暂停本轮执行并压缩历史（保留用户提示摘要、操作记录、未完成任务、当前工作、设计决策、限制和偏好），压完会从压缩后的上下文继续。`
                    : "上下文已超过服务端压缩阈值，已暂停本轮执行并压缩历史（保留用户提示摘要、操作记录、未完成任务、当前工作、设计决策、限制和偏好），压完会从压缩后的上下文继续。",
                streaming: true,
                meta:
                    [byTokens ? `${projected.toLocaleString("zh-CN")} / ${usable.toLocaleString("zh-CN")} Token · 阈值 ${Math.round(Number(payload.thresholdRatio || 0.85) * 100)}%` : undefined, turnCount > 0 ? `已整理前 ${turnCount} 轮对话` : undefined]
                        .filter(Boolean)
                        .join(" · ") || undefined,
                detail: { ...payload, eventType: "context_compaction", status: "running" },
            }),
        );
        return;
    }
    if (event.type === "context_compacted") {
        const turnCount = Number(payload.compactedTurnCount || 0);
        const fallback = payload.mode === "fallback";
        const historyMessages = Number(payload.historyMessages || 0);
        setMessages((current) =>
            upsertContextCompactionNotice(current, {
                id: `${event.runId}:context-compaction:${turnCount}`,
                role: "system",
                text: fallback ? "模型压缩不可用，已使用服务端保底检查点完成整理，后续工作可以继续。" : "已生成结构化检查点并保留最近对话，后续工作将从压缩后的上下文继续。",
                streaming: false,
                meta: `${turnCount > 0 ? `已整理 ${turnCount} 轮对话` : "上下文已整理"}${historyMessages > 0 ? ` · 保留 ${historyMessages} 条上下文消息` : ""}`,
                detail: { ...payload, eventType: "context_compaction", status: "completed" },
            }),
        );
        return;
    }
    if (event.type === "assistant_delta") {
        setMessages((current) => upsertTextMessage(current, String(payload.messageId || "assistant"), text, true));
        return;
    }
    if (event.type === "reasoning_delta" || event.type === "reasoning_message") {
        const id = String(payload.messageId || `${event.runId}:reasoning`);
        setMessages((current) => upsertTextMessage(current, id, text, event.type === "reasoning_delta").map((item) => (item.id === id ? { ...item, reasoning: true } : item)));
        return;
    }
    if (event.type === "plan_updated" && Array.isArray(payload.items)) {
        const id = `plan-${event.runId}`;
        const planItems = payload.items as CloudAgentPlanItem[];
        setMessages((current) => {
            const index = current.findIndex((entry) => entry.id === id);
            const message: CloudAgentChatMessage = { id, role: "tool", text: "", planItems };
            if (index < 0) return [...current, message];
            const next = [...current];
            next[index] = message;
            return next;
        });
        return;
    }
    if (event.type === "user_interjection") {
        if (!text) return;
        setMessages((current) => appendUniqueMessage(current, { id: String(payload.messageId || event.eventId), role: "user", text, interjection: "sent" }));
        return;
    }
    if (event.type === "user_interjection_dropped") {
        const messageId = String(payload.messageId || event.eventId);
        const reason = String(payload.reason || "本轮已结束");
        setMessages((current) => {
            const marked = current.map((item) => (item.id === messageId ? { ...item, interjection: "undelivered" as const } : item));
            return appendUniqueMessage(marked, { id: `interjection-dropped-${messageId}`, role: "system", text: `${reason}，这条插话没有送到模型。需要的话重新发一次，它会作为新一轮。` });
        });
        setPrompt?.((current) => (current.trim() ? current : text));
        return;
    }
    if (event.type === "user_interjection_delivered") {
        // 已进入模型上下文：后续撤回入口（U8）只对「等待送达」的插话开放。
        const messageId = String(payload.messageId || "");
        setMessages((current) => current.map((item) => (item.id === messageId && item.interjection === "sent" ? { ...item, interjection: "delivered" as const } : item)));
        return;
    }
    if (event.type === "user_question") {
        const options = Array.isArray(payload.options)
            ? (payload.options as Array<{ label?: unknown; detail?: unknown }>).map((option) => ({ label: String(option?.label || "").trim(), detail: option?.detail === undefined ? undefined : String(option.detail) })).filter((option) => option.label)
            : [];
        const question = String(payload.question || "").trim();
        if (!question || options.length < 2) return;
        const id = `question-${event.runId}:${event.seq ?? event.eventId}`;
        setMessages((current) =>
            appendUniqueMessage(current, {
                id,
                role: "assistant",
                text: "",
                question: { question, options, allowFreeform: payload.allowFreeform !== false },
            }),
        );
        return;
    }
    if (event.type === "assistant_message") {
        // final=false 是过程说明（模型边做边说的一段），不是本轮结论：界面上必须能区分，
        // 否则用户会把"我已经做完了"的中间稿当成最终答复（工作项 A 的原始症状）。
        setMessages((current) => upsertTextMessage(current, String(payload.messageId || event.eventId), text, false, agentAssistantFinality(payload)));
        return;
    }
    if (event.type === "completion_blocked") {
        // 收尾闸门的控制消息：模型想收尾但被拦下了。它是服务端控制行，**不是**用户发言，
        // 所以走 system 行，不进用户气泡。
        const attempt = Number(payload.attempt || 0);
        const maxAttempts = Number(payload.maxAttempts || 0);
        const id = `completion-blocked-${event.runId}:${event.seq ?? event.eventId}`;
        setMessages((current) => appendUniqueMessage(current, agentControlMessage(id, text || "本次收尾尚有协议或交付阻塞。", attempt > 0 && maxAttempts > 0 ? `第 ${attempt}/${maxAttempts} 次` : undefined)));
        return;
    }
    if (event.type === "assistant_snapshot") {
        setMessages((current) => upsertTextMessage(current, String(payload.messageId || event.eventId), text, false));
        return;
    }
    if (event.type === "approval_requested") {
        const approvalId = String(payload.approvalId || "");
        setApproval((current) => ({ approvalId, detail: payload, reason: current?.approvalId === approvalId ? current.reason : "" }));
        return;
    }
    if (event.type === "canvas_updated" && Array.isArray(payload.actions)) {
        if (payload.operation === "generate_media_submit" || payload.operation === "generate_media_complete") return;
        const { canvasPatch: _patch, ...detail } = payload;
        const id = payload.callId ? `canvas-${event.runId}-${payload.callId}` : event.eventId;
        setMessages((current) => appendUniqueMessage(current, { id, role: "tool", title: "canvas_apply_ops", text: "画布操作已完成", detail: { ...detail, eventType: event.type } }));
        return;
    }
    if (event.type.startsWith("tool_") && agentToolRetry(payload)) {
        const message: CloudAgentChatMessage = { id: event.eventId, role: "tool", title: String(payload.toolName || "工具执行"), text, detail: { ...payload, eventType: event.type } };
        setMessages((current) => mergeAgentToolRetry(current, message));
        if (event.type === "tool_failed") return;
    }
    if (event.type === "director_preview_requested") {
        // director preview 挂起等待浏览器录制；回执 tool_completed/tool_failed 稍后另起一条。
        setMessages((current) => appendUniqueMessage(current, { id: event.eventId, role: "tool", title: "director", text: "等待导演台录制预演", detail: { ...payload, eventType: event.type } }));
        return;
    }
    if (event.type === "tool_completed" && payload.toolName === "canvas_apply_ops" && payload.callId) {
        const id = `canvas-${event.runId}-${payload.callId}`;
        setMessages((current) => appendUniqueMessage(current, { id, role: "tool", title: "canvas_apply_ops", text: text || "画布操作已完成", detail: { ...payload, eventType: event.type } }));
        return;
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
        setMessages((current) => appendUniqueMessage(current, { id: `memory-${revisionId || event.eventId}`, role: "system", text: text || "长期记忆已更新", memoryUpdate }));
        return;
    }
    // 记忆写入成功时已有专门的记忆卡，不再重复渲染通用工具行；失败仍按工具行展示。
    if (event.type === "tool_completed" && payload.toolName === "memory_update") return;
    if (event.type === "generation_task_created") {
        const message: CloudAgentChatMessage = { id: event.eventId, role: "tool", title: "generate_media", text: text || event.type, detail: { ...payload, eventType: event.type } };
        setMessages((current) => upsertMediaToolTrace(current, message));
        return;
    }
    if (event.type.startsWith("tool_")) {
        const message: CloudAgentChatMessage = { id: event.eventId, role: "tool", title: String(payload.toolName || payload.title || "工具执行"), text: text || event.type, detail: { ...payload, eventType: event.type } };
        if (payload.toolName === "generate_media") {
            setMessages((current) => upsertMediaToolTrace(current, message));
        } else {
            setMessages((current) => appendUniqueMessage(current, message));
        }
        return;
    }
    if (event.type === "model_failure_recovered") {
        // 自动重试不该只躺在事件流里：用户看到的是"卡了一下又继续"，要能自己解释为什么。
        const id = event.eventId;
        setMessages((current) =>
            appendUniqueMessage(current, {
                id,
                role: "tool",
                title: "模型响应自动重试",
                text: text || "已关闭思考并重试同一步",
                detail: { ...payload, eventType: event.type },
            }),
        );
        return;
    }
    if (event.type === "run_failed" || event.type === "error") setMessages((current) => appendUniqueMessage(current, agentRunErrorMessage(event)));
}

function toolDetailRecord(value: unknown): Record<string, unknown> {
    return value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
}

function toolDetailNodeIds(detail: unknown): Set<string> {
    const payload = toolDetailRecord(detail);
    const ids = new Set<string>();
    for (const value of [payload.nodeId, toolDetailRecord(payload.result).nodeId]) {
        if (typeof value === "string" && value) ids.add(value);
    }
    if (Array.isArray(payload.actions)) {
        for (const action of payload.actions) {
            const nodeId = toolDetailRecord(action).nodeId;
            if (typeof nodeId === "string" && nodeId) ids.add(nodeId);
        }
    }
    if (typeof payload.arguments === "string") {
        try {
            const args = toolDetailRecord(JSON.parse(payload.arguments));
            if (Array.isArray(args.ops)) {
                for (const op of args.ops) {
                    const nodeId = toolDetailRecord(op).id;
                    if (typeof nodeId === "string" && nodeId) ids.add(nodeId);
                }
            }
        } catch {
            // Tool arguments are diagnostic data; a malformed value must not break the event feed.
        }
    }
    return ids;
}

function toolDetailTaskIds(detail: unknown): Set<string> {
    const payload = toolDetailRecord(detail);
    const ids = new Set<string>();
    for (const value of [payload.taskId, toolDetailRecord(payload.result).taskId]) {
        if (typeof value === "string" && value) ids.add(value);
    }
    return ids;
}

function mergeToolDetails(previous: unknown, next: unknown): Record<string, unknown> {
    const previousDetail = toolDetailRecord(previous);
    const nextDetail = toolDetailRecord(next);
    return {
        ...previousDetail,
        ...nextDetail,
        actions: Array.isArray(nextDetail.actions) ? nextDetail.actions : previousDetail.actions,
        arguments: nextDetail.arguments || previousDetail.arguments,
    };
}

function upsertMediaToolTrace(current: CloudAgentChatMessage[], message: CloudAgentChatMessage): CloudAgentChatMessage[] {
    const nextNodeIds = toolDetailNodeIds(message.detail);
    const nextTaskIds = toolDetailTaskIds(message.detail);
    const index = current.findIndex((item) => {
        if (item.role !== "tool") return false;
        const itemToolName = item.title || "";
        if (itemToolName !== "canvas_apply_ops" && itemToolName !== "generate_media") return false;
        const itemNodeIds = toolDetailNodeIds(item.detail);
        const itemTaskIds = toolDetailTaskIds(item.detail);
        return [...nextNodeIds].some((id) => itemNodeIds.has(id)) || [...nextTaskIds].some((id) => itemTaskIds.has(id));
    });
    if (index < 0) return appendUniqueMessage(current, message);
    const next = [...current];
    const previous = next[index];
    next[index] = {
        ...previous,
        ...message,
        id: previous.id,
        detail: mergeToolDetails(previous.detail, message.detail),
    };
    return next;
}
export function appendUniqueMessage(current: CloudAgentChatMessage[], message: CloudAgentChatMessage) {
    return current.some((item) => item.id === message.id) ? current : [...current, message];
}
function upsertContextCompactionNotice(current: CloudAgentChatMessage[], message: CloudAgentChatMessage) {
    const index = current.findIndex((item) => item.id === message.id);
    if (index < 0) return [...current, message];
    const next = [...current];
    next[index] = message;
    return next;
}

function upsertTextMessage(current: CloudAgentChatMessage[], id: string, text: string, append: boolean, final?: boolean): CloudAgentChatMessage[] {
    const index = current.findIndex((item) => item.id === id);
    if (index < 0) return [...current, { id, role: "assistant" as const, text, streaming: append, final }];
    // final 也要参与"没变化"的判断：流式快照先建了气泡，结论事件可能带着同样的正文与
    // final=false 到来，少了这一项就会把过程/结论的口径吞掉（界面不再单独出角标，但标记仍如实保存）。
    if (!append && current[index].text === text && !current[index].streaming && current[index].final === final) return current;
    const next = [...current];
    next[index] = { ...next[index], text: append ? `${next[index].text}${text}` : text, streaming: append, final };
    return next;
}
