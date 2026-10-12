import { useEffect, useRef, useState } from "react";
import { ArrowLeft, RefreshCw } from "lucide-react";
import type { CanvasTheme } from "@/lib/canvas-theme";
import { getAgentRun, subscribeAgentEvents, type AgentEvent } from "@/services/api/agent";
import type { AgentSubagent } from "@/services/api/agent-subagent";
import { AgentChatMessage, AgentOperationFeed, type CloudAgentChatMessage } from "./canvas-cloud-agent-chat-ui";
import { buildAgentFeedSegments } from "@/lib/canvas/agent-operation-feed";
import "./canvas-agent-subagent-conversation.css";

export function subagentConversationMessages(events: AgentEvent[]): CloudAgentChatMessage[] {
    const messages: CloudAgentChatMessage[] = [];
    const seen = new Set<string>();
    // SSE draft snapshots have seq=0: keep them after the persisted chunks they replace.
    let cursor = 0;
    const ordered = events
        .map((event) => {
            cursor = Math.max(cursor, event.seq);
            return { event, order: event.seq > 0 ? event.seq : cursor + 0.5 };
        })
        .sort((a, b) => a.order - b.order);
    for (const { event } of ordered) {
        if (seen.has(event.eventId)) continue;
        seen.add(event.eventId);
        const text = typeof event.payload.text === "string" ? event.payload.text : "";
        if (["assistant_delta", "assistant_snapshot", "assistant_message"].includes(event.type)) {
            const id = String(event.payload.messageId || (event.type === "assistant_delta" ? "assistant" : event.eventId));
            const existing = messages.find((item) => item.id === id);
            if (existing) existing.text = event.type === "assistant_delta" ? existing.text + text : text;
            else messages.push({ id, role: "assistant", text });
        } else if (event.type === "user_interjection") {
            messages.push({ id: event.eventId, role: "user", text });
        } else if (["tool_started", "tool_completed", "tool_failed"].includes(event.type)) {
            messages.push({ id: event.eventId, role: "tool", title: String(event.payload.toolName || "工具执行"), text, detail: { ...event.payload, eventType: event.type } });
        }
    }
    return messages;
}

export function AgentSubagentConversation({ agent, theme, onBack }: { agent: AgentSubagent; theme: CanvasTheme; onBack: () => void }) {
    const [events, setEvents] = useState<AgentEvent[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [truncated, setTruncated] = useState(false);
    const [reload, setReload] = useState(0);
    const [runMeta, setRunMeta] = useState<{ status: string; failureMessage?: string } | null>(null);
    useEffect(() => {
        const controller = new AbortController();
        let active = true;
        let stop: (() => void) | undefined;
        setLoading(true);
        setError("");
        void getAgentRun(agent.childRunId, controller.signal, { eventLimit: 500 })
            .then(({ run }) => {
                if (!active) return;
                setEvents(run.events || []);
                setRunMeta({ status: run.status, failureMessage: run.failureMessage });
                setTruncated(run.eventsTruncated === true);
                setLoading(false);
                if (!["completed", "failed", "cancelled", "rejected"].includes(run.status)) {
                    stop = subscribeAgentEvents(
                        agent.childRunId,
                        (event) => {
                            if (!active) return;
                            if (event.type === "run_status" && typeof event.payload.status === "string") {
                                setRunMeta({ status: event.payload.status, failureMessage: typeof event.payload.failureMessage === "string" ? event.payload.failureMessage : undefined });
                            }
                            setEvents((current) => {
                                if (current.some((item) => item.eventId === event.eventId)) return current;
                                return [...current, event];
                            });
                        },
                        {
                            after: run.latestSeq || 0,
                            onConnectionChange: (status) => {
                                if (active) setError(status === "disconnected" ? "会话连接中断，请刷新重试" : "");
                            },
                        },
                    );
                }
            })
            .catch(() => {
                if (active) {
                    setLoading(false);
                    setError("子代理会话读取失败，请刷新重试");
                }
            });
        return () => {
            active = false;
            controller.abort();
            stop?.();
        };
    }, [agent.childRunId, reload]);
    return (
        <AgentSubagentConversationView
            agent={runMeta ? { ...agent, ...runMeta } : agent}
            theme={theme}
            messages={subagentConversationMessages(events)}
            loading={loading}
            error={error}
            truncated={truncated}
            onBack={onBack}
            onRefresh={() => setReload((value) => value + 1)}
        />
    );
}

export function AgentSubagentConversationView({
    agent,
    theme,
    messages,
    loading,
    error,
    truncated,
    onBack,
    onRefresh,
}: {
    agent: AgentSubagent;
    theme: CanvasTheme;
    messages: CloudAgentChatMessage[];
    loading: boolean;
    error: string;
    truncated?: boolean;
    onBack: () => void;
    onRefresh: () => void;
}) {
    const back = useRef<HTMLButtonElement>(null);
    useEffect(() => {
        back.current?.focus();
    }, []);
    return (
        <section className="agent-subagent-conversation" aria-label={`${agent.displayName}的会话`}>
            <header className="agent-subagent-conversation-header">
                <button ref={back} type="button" aria-label="返回父 Agent 对话" onClick={onBack}>
                    <ArrowLeft size={16} aria-hidden="true" />
                </button>
                <div className="min-w-0 flex-1">
                    <h2>{agent.displayName}</h2>
                    <p>
                        {agent.roleLabel} · {statusLabel(agent.status)}
                    </p>
                </div>
                <button type="button" aria-label="刷新会话" title="刷新会话" onClick={onRefresh} disabled={loading}>
                    <RefreshCw size={16} aria-hidden="true" />
                </button>
            </header>
            <div className="agent-subagent-conversation-scroll thin-scrollbar" data-canvas-wheel-scroll>
                <div className="agent-subagent-objective">
                    <span>任务目标</span>
                    <p>{agent.objective}</p>
                </div>
                {agent.failureMessage ? <p role="alert">{agent.failureMessage}</p> : null}
                {agent.messages?.length ? (
                    <details className="agent-subagent-reports" open={!messages.length}>
                        <summary>与父 Agent 的任务回报</summary>
                        {[...agent.messages]
                            .sort((a, b) => a.sequence - b.sequence)
                            .map((message) => (
                                <article key={message.id}>
                                    <span>
                                        {message.direction === "parent_to_child" ? "父 Agent" : agent.displayName} · {message.kind === "final_result" ? "最终回报" : message.kind === "progress" ? "进展" : "消息"}
                                    </span>
                                    <p>{message.text}</p>
                                </article>
                            ))}
                    </details>
                ) : null}
                {loading ? <p role="status">正在读取会话…</p> : null}
                {error ? <p role="alert">{error}</p> : null}
                {truncated ? <p className="agent-subagent-transcript-note">显示最近 500 条运行记录；更早的内容已省略。</p> : null}
                <div className="agent-subagent-transcript">
                    {buildAgentFeedSegments(messages).map((segment) =>
                        segment.kind === "operations" ? <AgentOperationFeed key={segment.key} items={segment.items} theme={theme} /> : segment.kind === "message" ? <AgentChatMessage key={segment.key} item={segment.item} theme={theme} /> : null,
                    )}
                </div>
                {!loading && !error && !messages.length ? <p className="agent-subagent-transcript-note">暂无会话正文，可先查看任务回报。</p> : null}
            </div>
        </section>
    );
}

function statusLabel(status: string) {
    return status === "completed" ? "已完成" : status === "failed" ? "失败" : status === "cancelled" ? "已取消" : status === "queued" || status.startsWith("waiting") ? "等待中" : "运行中";
}
