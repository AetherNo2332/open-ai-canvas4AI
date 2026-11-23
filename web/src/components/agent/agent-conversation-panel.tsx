import { useEffect, useState, type ReactNode } from "react";
import { Alert, App, Button, Input, Select, Tag } from "antd";
import { Archive, CircleStop, History, MessageSquarePlus, RotateCcw, Send, X } from "lucide-react";
import { AIMessageMarkdown } from "@/components/ai/ai-message-markdown";
import { useAgentConversation } from "./use-agent-conversation";
import type { AgentConversationRecord } from "@/services/agent/conversation-types";
import type { AgentPageContext } from "@/services/agent/business-context";
import type { AgentRunRequest } from "@/sdk/agent";
import type { createAgentClient } from "@/sdk/agent/client";
import { createClientUUID } from "@/lib/client-id";

type ScopedClient = ReturnType<typeof createAgentClient>;

export type BuildAgentRequest = (input: { prompt: string; conversation: AgentConversationRecord | null }) => AgentRunRequest;

export function AgentConversationPanel({
    context,
    client,
    buildRequest,
    scopeLabel,
    userScope,
    headerExtra,
    open,
    onOpen,
    onClose,
}: {
    context: AgentPageContext;
    client: ScopedClient;
    buildRequest: BuildAgentRequest;
    scopeLabel: string;
    userScope: string;
    headerExtra?: ReactNode;
    open: boolean;
    onOpen: () => void;
    onClose: () => void;
}) {
    const { message } = App.useApp();
    const { controller, state } = useAgentConversation({
        userScope,
        scope: context.scope || null,
        agentDefinitionId: context.agentDefinitionId || "",
        client: context.scope ? client : null,
    });
    const [draft, setDraft] = useState("");
    const [sending, setSending] = useState(false);
    const [historyOpen, setHistoryOpen] = useState(false);
    const [editingQueuedId, setEditingQueuedId] = useState("");
    const [queuedDraft, setQueuedDraft] = useState("");
    const [queueBusy, setQueueBusy] = useState(false);
    const activeRecord = state.conversations.find((item) => item.id === state.activeConversationId);
    const activeRun = state.run;
    const running = Boolean(activeRun && (activeRun.cleanupPending || ["queued", "running", "waiting_approval"].includes(activeRun.status)));
    const question = [...state.messages].reverse().find((item) => item.question)?.question;

    useEffect(() => {
        if (state.draft !== draft && state.status === "ready") setDraft(state.draft);
        // The controller owns persistence; only mirror its draft when the active
        // conversation or a server-cleared draft changes.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [state.activeConversationId, state.draft]);

    const updateDraft = (value: string) => {
        setDraft(value);
        controller?.setDraft(value);
    };

    const submit = async (value = draft, replyToQuestionId?: string) => {
        if (!controller || !value.trim() || sending || state.busy) return;
        const prompt = value.trim();
        setSending(true);
        try {
            const request = buildRequest({ prompt, conversation: activeRecord || null });
            if (!request.idempotencyKey) request.idempotencyKey = createClientUUID();
            const ok = await controller.submit({
                request,
                mode: replyToQuestionId ? "interject" : running ? "queue" : undefined,
                replyToQuestionId,
            });
            if (ok) setDraft("");
            else if (state.pending?.request) message.warning("请求结果未确认，请先重试原消息");
        } catch (cause) {
            message.error(cause instanceof Error ? cause.message : "无法准备本次请求");
        } finally {
            setSending(false);
        }
    };

    const runQueueAction = async (action: () => Promise<boolean>, success?: string) => {
        setQueueBusy(true);
        try {
            if (await action()) {
                if (success) message.success(success);
                setEditingQueuedId("");
            }
        } catch (cause) {
            message.error(cause instanceof Error ? cause.message : "待发消息操作失败，请刷新后重试");
        } finally {
            setQueueBusy(false);
        }
    };

    const title = activeRecord?.title || "新对话";
    const status = state.status === "loading" ? "加载中" : activeRun?.status === "running" ? "运行中" : activeRun?.status === "waiting_approval" ? "等待审批" : "待命";
    return (
        <>
            {!open ? (
                <button type="button" onClick={onOpen} className="fixed bottom-5 right-5 z-[70] flex items-center gap-2 rounded-full bg-neutral-900 px-4 py-3 text-sm text-white shadow-xl">
                    <MessageSquarePlus className="size-4" />
                    {scopeLabel}
                </button>
            ) : null}
            <aside className={`fixed bottom-4 right-4 z-[70] flex h-[min(720px,calc(100dvh-32px))] w-[min(440px,calc(100vw-32px))] flex-col overflow-hidden rounded-2xl border border-black/10 bg-white shadow-2xl ${open ? "" : "hidden"}`} aria-label="Agent 工作台">
                <header className="flex items-center gap-2 border-b border-black/10 px-4 py-3">
                    <div className="flex min-w-0 flex-1 flex-col">
                        <strong className="truncate text-sm">{scopeLabel}</strong>
                        <span className="truncate text-[11px] text-neutral-500">
                            {title} · {status}
                        </span>
                    </div>
                    {headerExtra}
                    <Button type="text" size="small" aria-label="新对话" icon={<MessageSquarePlus className="size-4" />} onClick={() => void controller?.newConversation()} />
                    <Button type="text" size="small" aria-label="历史" icon={<History className="size-4" />} onClick={() => setHistoryOpen((value) => !value)} />
                    <Button type="text" size="small" aria-label="关闭" icon={<X className="size-4" />} onClick={onClose} />
                </header>
                {historyOpen ? (
                    <div className="border-b border-black/10 p-3">
                        <Select
                            className="w-full"
                            value={state.activeConversationId || undefined}
                            options={state.conversations.map((conversation) => ({ value: conversation.id, label: conversation.title || "新对话" }))}
                            onChange={(id) => void controller?.openConversation(id)}
                            placeholder="选择会话"
                        />
                        <div className="mt-2 flex justify-end gap-2">
                            {activeRecord?.serverConversationId ? (
                                <Button size="small" type="text" danger icon={<Archive className="size-3.5" />} onClick={() => void controller?.archiveConversation(activeRecord.id)}>
                                    归档
                                </Button>
                            ) : null}
                        </div>
                    </div>
                ) : null}
                <div className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-4">
                    {state.status === "error" ? <Alert type="error" message={state.error || "会话加载失败"} showIcon /> : null}
                    {activeRecord?.historyState?.status === "unavailable" ? <Alert type="warning" message={activeRecord.historyState.message} showIcon /> : null}
                    {state.messages.length ? (
                        state.messages.map((item) => (
                            <div key={item.id} className={item.role === "user" ? "ml-8 rounded-2xl bg-neutral-900 px-3 py-2 text-sm text-white" : "mr-3"}>
                                {item.role === "tool" ? (
                                    <div className="rounded-lg border border-black/10 px-3 py-2 text-xs text-neutral-600">
                                        <b>{item.title || "工具"}</b>
                                        {item.text ? <p className="mt-1 whitespace-pre-wrap">{item.text}</p> : null}
                                    </div>
                                ) : item.role === "error" ? (
                                    <Alert type="error" message={item.text} showIcon />
                                ) : item.role === "assistant" && item.text ? (
                                    item.reasoning ? <div className="text-xs text-neutral-500">思考：{item.text}</div> : <AIMessageMarkdown streamingAnimation={item.streaming ? "word" : "none"}>{item.text}</AIMessageMarkdown>
                                ) : item.text ? (
                                    <span className="whitespace-pre-wrap">{item.text}</span>
                                ) : null}
                            </div>
                        ))
                    ) : state.status === "ready" ? (
                        <div className="rounded-xl border border-dashed border-black/15 p-4 text-sm text-neutral-500">向 Agent 说明你想完成的工作。当前页面只提供已授权的 {scopeLabel} 操作。</div>
                    ) : null}
                    {question ? (
                        <div className="rounded-xl border border-amber-200 bg-amber-50 p-3 text-sm">
                            <p className="font-medium">{question.question}</p>
                            <div className="mt-2 flex flex-wrap gap-2">
                                {question.options.map((option) => (
                                    <Button key={option.label} size="small" onClick={() => void submit(option.label, question.id)}>
                                        {option.label}
                                    </Button>
                                ))}
                            </div>
                        </div>
                    ) : null}
                    {state.approval ? (
                        <div className="rounded-xl border border-sky-200 bg-sky-50 p-3 text-sm">
                            <p className="font-medium">Agent 请求审批</p>
                            <p className="mt-1 text-xs text-neutral-600">批准后才会执行本次业务写入。</p>
                            <div className="mt-2 flex gap-2">
                                <Button type="primary" size="small" onClick={() => void controller?.decideApproval("approve")}>
                                    批准
                                </Button>
                                <Button size="small" onClick={() => void controller?.decideApproval("reject")}>
                                    拒绝
                                </Button>
                            </div>
                        </div>
                    ) : null}
                    {state.queue?.items.length ? (
                        <div className="rounded-xl border border-black/10 p-3 text-xs">
                            <div className="flex items-center justify-between">
                                <b>待发消息</b>
                                <div className="flex items-center gap-2">
                                    <span>{state.queue.paused ? state.queue.pauseReason || "已暂停" : `${state.queue.items.length} 条`}</span>
                                    <Button
                                        size="small"
                                        type="text"
                                        loading={queueBusy}
                                        onClick={() => void runQueueAction(() => controller?.updateQueue(state.queue?.paused ? "resume" : "pause") || Promise.resolve(false))}
                                    >
                                        {state.queue.paused ? "继续" : "暂停"}
                                    </Button>
                                </div>
                            </div>
                            {state.queue.items.map((item) => (
                                <div key={item.id} className="mt-2 rounded-lg bg-neutral-50 px-2 py-1.5">
                                    {editingQueuedId === item.id ? (
                                        <Input
                                            size="small"
                                            value={queuedDraft}
                                            onChange={(event) => setQueuedDraft(event.target.value)}
                                            onPressEnter={() => void runQueueAction(() => controller?.editQueued(item, queuedDraft.trim()) || Promise.resolve(false), "待发消息已更新")}
                                        />
                                    ) : (
                                        <p className="line-clamp-2 whitespace-pre-wrap">{item.prompt}</p>
                                    )}
                                    <div className="mt-1 flex flex-wrap items-center gap-1">
                                        {editingQueuedId === item.id ? (
                                            <>
                                                <Button
                                                    size="small"
                                                    type="text"
                                                    disabled={!queuedDraft.trim()}
                                                    loading={queueBusy}
                                                    onClick={() => void runQueueAction(() => controller?.editQueued(item, queuedDraft.trim()) || Promise.resolve(false), "待发消息已更新")}
                                                >
                                                    保存
                                                </Button>
                                                <Button size="small" type="text" onClick={() => setEditingQueuedId("")}>
                                                    取消
                                                </Button>
                                            </>
                                        ) : (
                                            <>
                                                <Button
                                                    size="small"
                                                    type="text"
                                                    onClick={() => {
                                                        setEditingQueuedId(item.id);
                                                        setQueuedDraft(item.prompt);
                                                    }}
                                                >
                                                    编辑
                                                </Button>
                                                <Button
                                                    size="small"
                                                    type="text"
                                                    loading={queueBusy}
                                                    onClick={() => void runQueueAction(() => controller?.interjectQueued(item) || Promise.resolve(false), "已提升为插话")}
                                                >
                                                    插队
                                                </Button>
                                                <Button
                                                    size="small"
                                                    type="text"
                                                    danger
                                                    loading={queueBusy}
                                                    onClick={() => void runQueueAction(() => controller?.cancelQueued(item) || Promise.resolve(false), "已移除待发消息")}
                                                >
                                                    移除
                                                </Button>
                                            </>
                                        )}
                                    </div>
                                </div>
                            ))}
                        </div>
                    ) : null}
                    {state.error && state.status !== "error" ? <Alert type="error" message={state.error} showIcon /> : null}
                </div>
                <div className="border-t border-black/10 p-3">
                    {state.pending?.request ? (
                        <div className="mb-2 flex items-center justify-between rounded-lg bg-amber-50 px-2 py-1.5 text-xs text-amber-800">
                            <span>上一条请求结果未确认</span>
                            <Button size="small" type="text" icon={<RotateCcw className="size-3.5" />} onClick={() => void controller?.retryPending()}>
                                原样重试
                            </Button>
                        </div>
                    ) : null}
                    <Input.TextArea
                        value={draft}
                        onChange={(event) => updateDraft(event.target.value)}
                        onPressEnter={(event) => {
                            if (!event.shiftKey) {
                                event.preventDefault();
                                void submit();
                            }
                        }}
                        placeholder="说明你想完成的工作…"
                        autoSize={{ minRows: 2, maxRows: 6 }}
                        disabled={state.status !== "ready"}
                    />
                    <div className="mt-2 flex items-center justify-between">
                        <div className="flex items-center gap-2 text-[11px] text-neutral-500">
                            {running ? <Tag color="gold">服务端仍在执行</Tag> : <Tag>{scopeLabel}</Tag>}
                            {state.pending ? <span>待确认提交</span> : null}
                        </div>
                        <div className="flex gap-2">
                            {running ? (
                                <Button size="small" icon={<CircleStop className="size-3.5" />} onClick={() => void controller?.cancelRun()}>
                                    停止
                                </Button>
                            ) : null}
                            <Button type="primary" size="small" icon={<Send className="size-3.5" />} loading={sending || state.busy} disabled={!draft.trim() || state.status !== "ready"} onClick={() => void submit()}>
                                发送
                            </Button>
                        </div>
                    </div>
                </div>
            </aside>
        </>
    );
}
