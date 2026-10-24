import { useState } from "react";
import { Check, ChevronDown, ChevronUp, CornerUpLeft, ListOrdered, LoaderCircle, Pencil, Play, RotateCw, Trash2, X } from "lucide-react";
import type { AgentMessageQueue, AgentQueuedMessage, AgentSendMode } from "@/services/api/agent";
import "./canvas-agent-message-queue.css";

export function AgentSendModeButton({ value, saving, onOpenSettings }: { value?: AgentSendMode; saving?: boolean; onOpenSettings: () => void }) {
    const label = !value ? "发送方式加载中" : `默认${value === "queue" ? "排队" : "插队"} · Ctrl/⌘+Enter 本次${value === "queue" ? "插队" : "排队"}`;
    const Icon = saving || !value ? LoaderCircle : value === "queue" ? ListOrdered : CornerUpLeft;
    return (
        <button type="button" className="agent-send-mode" aria-label={`${label}，打开发送设置`} title={`${label}；点击打开 Agent 设置`} onClick={onOpenSettings}>
            <Icon aria-hidden="true" className={`size-4 ${saving ? "animate-spin" : ""}`} />
        </button>
    );
}

export function AgentMessageQueueList({
    queue,
    error,
    busy,
    onEdit,
    onPromote,
    onRemove,
    onResume,
    onRetry,
}: {
    queue?: AgentMessageQueue;
    error?: string;
    busy?: boolean;
    onEdit: (item: AgentQueuedMessage, value: string) => Promise<boolean>;
    onPromote: (item: AgentQueuedMessage) => Promise<boolean>;
    onRemove: (item: AgentQueuedMessage) => Promise<boolean>;
    onResume: () => Promise<boolean>;
    onRetry: () => void | Promise<void>;
}) {
    const [expanded, setExpanded] = useState(false);
    const [editing, setEditing] = useState<{ item: AgentQueuedMessage; value: string }>();
    if (!queue?.items.length && !queue?.paused && !error) return null;
    return (
        <section className="agent-message-queue" aria-label="待发消息" data-expanded={expanded || undefined}>
            {(queue?.items.length || 0) > 1 || queue?.paused || queue?.awaitingQuestionId ? (
                <div className="agent-message-queue-header">
                    <button type="button" onClick={() => setExpanded((value) => !value)} aria-expanded={expanded} className="agent-message-queue-expand">
                        <ListOrdered aria-hidden="true" className="size-3.5" />
                        待发 {queue?.items.length || 0}
                        {expanded ? <ChevronUp className="size-3.5" /> : <ChevronDown className="size-3.5" />}
                    </button>
                    {queue?.awaitingQuestionId ? <span className="agent-message-queue-status">等待回答</span> : null}
                    {queue?.paused ? (
                        <>
                            <span className="agent-message-queue-status">已暂停</span>
                            <button type="button" title="继续发送" aria-label="继续发送" disabled={busy} onClick={() => void onResume()}>
                                <Play aria-hidden="true" className="size-3.5" />
                            </button>
                        </>
                    ) : null}
                </div>
            ) : null}
            {queue?.paused && queue.pauseReason ? <p className="agent-message-queue-status">{queue.pauseReason}</p> : null}
            {error ? (
                <div role="alert" className="agent-message-queue-error">
                    <span>{error}</span>
                    <button type="button" title="重试" aria-label="重试" disabled={busy} onClick={() => void onRetry()}>
                        <RotateCw aria-hidden="true" className="size-3.5" />
                    </button>
                </div>
            ) : null}
            {queue?.items.length ? (
                <ol className="agent-message-queue-items">
                    {(expanded ? queue.items : queue.items.slice(0, 1)).map((item, index) => (
                        <li key={item.id}>
                            <span className="agent-message-queue-index">{queue.items.length > 1 ? index + 1 : <ListOrdered aria-hidden="true" className="size-3.5" />}</span>
                            <div className="agent-message-queue-content" data-editing={editing?.item.id === item.id || undefined}>
                                {editing?.item.id === item.id ? (
                                    <>
                                        <textarea aria-label="编辑待发消息" value={editing.value} onChange={(event) => setEditing({ ...editing, value: event.target.value })} disabled={busy} />
                                        <div className="agent-message-queue-actions">
                                            <button
                                                type="button"
                                                title="保存待发消息"
                                                aria-label="保存待发消息"
                                                disabled={busy || !editing.value.trim()}
                                                onClick={() => {
                                                    void onEdit(editing.item, editing.value).then((saved) => {
                                                        if (saved) setEditing(undefined);
                                                    });
                                                }}
                                            >
                                                <Check aria-hidden="true" className="size-3.5" />
                                            </button>
                                            <button type="button" title="取消编辑" aria-label="取消编辑" disabled={busy} onClick={() => setEditing(undefined)}>
                                                <X aria-hidden="true" className="size-3.5" />
                                            </button>
                                        </div>
                                    </>
                                ) : (
                                    <>
                                        <p title={item.prompt}>{item.prompt}</p>
                                        {item.status === "dispatching" ? (
                                            <span className="agent-message-queue-status">发送中…</span>
                                        ) : (
                                            <div className="agent-message-queue-actions">
                                                <button type="button" title="编辑待发消息" aria-label="编辑待发消息" disabled={busy} onClick={() => setEditing({ item, value: item.prompt })}>
                                                    <Pencil aria-hidden="true" className="size-3.5" />
                                                </button>
                                                <button type="button" title="插队发送" aria-label="插队发送" disabled={busy} onClick={() => void onPromote(item)}>
                                                    <CornerUpLeft aria-hidden="true" className="size-3.5" />
                                                </button>
                                                <button type="button" title="移除待发消息" aria-label="移除待发消息" disabled={busy} onClick={() => void onRemove(item)}>
                                                    <Trash2 aria-hidden="true" className="size-3.5" />
                                                </button>
                                            </div>
                                        )}
                                        {item.error ? <span className="agent-message-queue-status agent-message-queue-item-error">暂时无法发送</span> : null}
                                    </>
                                )}
                            </div>
                        </li>
                    ))}
                </ol>
            ) : null}
        </section>
    );
}
