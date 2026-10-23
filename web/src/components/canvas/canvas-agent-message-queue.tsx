import { useState } from "react";
import { Dropdown } from "antd";
import { Check, ChevronDown, ChevronUp } from "lucide-react";
import type { AgentMessageQueue, AgentQueuedMessage, AgentSendMode } from "@/services/api/agent";
import "./canvas-agent-message-queue.css";

export function AgentSendModeMenu({ value, disabled, onChange }: { value: AgentSendMode; disabled?: boolean; onChange: (value: AgentSendMode) => void | Promise<void> }) {
    const labels = { interject: "插队", queue: "排队" };
    return (
        <Dropdown
            trigger={["click"]}
            disabled={disabled}
            menu={{ selectedKeys: [value], items: (["interject", "queue"] as const).map((key) => ({ key, label: labels[key], icon: key === value ? <Check className="size-3.5" /> : null })), onClick: ({ key }) => void onChange(key as AgentSendMode) }}
        >
            <button type="button" className="agent-send-mode" aria-label={`默认发送方式：${labels[value]}`} disabled={disabled}>
                <span>{labels[value]}</span>
                <ChevronDown className="size-3" />
            </button>
        </Dropdown>
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
        <section className="agent-message-queue" aria-label="待发消息">
            <div className="agent-message-queue-header">
                <button type="button" onClick={() => setExpanded((value) => !value)} aria-expanded={expanded} className="agent-message-queue-expand">
                    待发 {queue?.items.length || 0}
                    {expanded ? <ChevronUp className="size-3.5" /> : <ChevronDown className="size-3.5" />}
                </button>
                {queue?.awaitingQuestionId ? <span className="agent-message-queue-status">等待回答</span> : null}
                {queue?.paused ? (
                    <>
                        <span className="agent-message-queue-status">已暂停</span>
                        <button type="button" disabled={busy} onClick={() => void onResume()}>
                            继续
                        </button>
                    </>
                ) : null}
            </div>
            {queue?.paused && queue.pauseReason ? <p className="agent-message-queue-status">{queue.pauseReason}</p> : null}
            {error ? (
                <div role="alert" className="agent-message-queue-error">
                    <span>{error}</span>
                    <button type="button" disabled={busy} onClick={() => void onRetry()}>
                        重试
                    </button>
                </div>
            ) : null}
            {expanded ? (
                <ol className="agent-message-queue-items">
                    {queue?.items.map((item, index) => (
                        <li key={item.id}>
                            <span className="agent-message-queue-index">{index + 1}</span>
                            <div className="agent-message-queue-content">
                                {editing?.item.id === item.id ? (
                                    <>
                                        <textarea aria-label="编辑待发消息" value={editing.value} onChange={(event) => setEditing({ ...editing, value: event.target.value })} disabled={busy} />
                                        <div className="agent-message-queue-actions">
                                            <button
                                                type="button"
                                                disabled={busy || !editing.value.trim()}
                                                onClick={() => {
                                                    void onEdit(editing.item, editing.value).then((saved) => {
                                                        if (saved) setEditing(undefined);
                                                    });
                                                }}
                                            >
                                                保存
                                            </button>
                                            <button type="button" disabled={busy} onClick={() => setEditing(undefined)}>
                                                取消
                                            </button>
                                        </div>
                                    </>
                                ) : (
                                    <>
                                        <p>{item.prompt}</p>
                                        {item.error ? <span className="agent-message-queue-status">暂时无法发送</span> : null}
                                        {item.status === "dispatching" ? (
                                            <span className="agent-message-queue-status">发送中…</span>
                                        ) : (
                                            <div className="agent-message-queue-actions">
                                                <button type="button" disabled={busy} onClick={() => setEditing({ item, value: item.prompt })}>
                                                    编辑
                                                </button>
                                                <button type="button" disabled={busy} onClick={() => void onPromote(item)}>
                                                    插队
                                                </button>
                                                <button type="button" disabled={busy} onClick={() => void onRemove(item)}>
                                                    移除
                                                </button>
                                            </div>
                                        )}
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
