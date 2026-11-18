import { useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { Alert, Button, Input, InputNumber, Select, Space } from "antd";
import { Archive, ArrowUp, Clapperboard, History, LockKeyhole, Square } from "lucide-react";
import { AIMessageMarkdown } from "@/components/ai/ai-message-markdown";
import { ThinkingLoader } from "@/components/ui/thinking-loader-clean";
import { createAgentClient } from "@/sdk/agent/client";
import type { AgentConversation, AgentRunRequest, AgentRunSnapshot } from "@/sdk/agent/contract";
import { agentWebTransport } from "@/sdk/agent/http-transport";
import type { DramaModelChoice } from "@/services/api/drama";
import { dramaAgentAnalysisRequest } from "@/services/api/drama-agent";
import "./agent-analysis.css";

type AnalysisQuestion = { id: string; text: string; options: string[]; allowFreeform: boolean };

const statusLabels: Record<AgentRunSnapshot["status"], string> = {
    queued: "排队中",
    running: "正在分析",
    waiting_approval: "等待确认",
    completed: "已完成",
    failed: "分析失败",
    cancelled: "已停止",
    rejected: "已拒绝",
};

/** Displays server-owned Agent proposals. Script editing/adoption stays in the studio. */
export function DramaAgentAnalysis({ projectId, episodeId, model }: { projectId: string; episodeId: string; model: DramaModelChoice }) {
    const [params, setParams] = useSearchParams();
    const runId = params.get("agentProject") && params.get("agentProject") !== projectId ? "" : params.get("agentRun") || "";
    const client = useMemo(() => createAgentClient({ transport: agentWebTransport, scope: { type: "drama", id: projectId } }), [projectId]);
    const [run, setRun] = useState<AgentRunSnapshot>();
    const [conversations, setConversations] = useState<AgentConversation[]>([]);
    const [turns, setTurns] = useState<AgentRunSnapshot[]>([]);
    const [cursor, setCursor] = useState<string>();
    const [question, setQuestion] = useState<AnalysisQuestion>();
    const [awaitingRun, setAwaitingRun] = useState("");
    const [directoryRevision, refreshDirectory] = useState(0);
    const [prompt, setPrompt] = useState("分析本集的剧情连贯性、人物动机与镜头节奏，给出可供我审阅的具体修改建议。");
    const [credits, setCredits] = useState<number | null>(20);
    const [text, setText] = useState("");
    const [error, setError] = useState("");
    const [pending, setPending] = useState(false);
    // Keep the exact submission after response loss; edits explicitly start a new request.
    const submission = useRef<AgentRunRequest | undefined>(undefined);
    const viewIdentity = useRef(0);
    const selectRun = (id: string) =>
        setParams(
            (previous) => {
                const next = new URLSearchParams(previous);
                if (id) {
                    next.set("agentRun", id);
                    next.set("agentProject", projectId);
                } else {
                    next.delete("agentRun");
                    next.delete("agentProject");
                }
                return next;
            },
            { replace: true },
        );
    useEffect(() => {
        viewIdentity.current++;
        submission.current = undefined;
        setRun(undefined);
        setText("");
        setError("");
        setPending(false);
        return () => {
            viewIdentity.current++;
        };
    }, [projectId, episodeId]);
    useEffect(() => {
        const controller = new AbortController();
        void client
            .listConversations({}, controller.signal)
            .then((page) => {
                if (!controller.signal.aborted) {
                    setConversations(page.items);
                    setCursor(page.nextCursor);
                }
            })
            .catch((cause) => {
                if (!controller.signal.aborted) setError(String(cause.message));
            });
        return () => controller.abort();
    }, [client, directoryRevision]);
    useEffect(() => {
        const controller = new AbortController();
        setTurns([]);
        if (run?.conversationId) {
            void client
                .listConversationRuns(run.conversationId, {}, controller.signal)
                .then((page) => {
                    if (!controller.signal.aborted) setTurns(page.items);
                })
                .catch((cause) => {
                    if (!controller.signal.aborted) setError(String(cause.message));
                });
        }
        return () => controller.abort();
    }, [client, run?.conversationId, directoryRevision]);
    useEffect(() => {
        setText("");
        setQuestion(undefined);
        setRun(undefined);
        if (!runId) return;
        const controller = new AbortController();
        setError("");
        void (async () => {
            const { run: initial } = await client.getRun(runId, controller.signal);
            controller.signal.throwIfAborted();
            if (initial.scope?.type !== "drama" || initial.scope.id !== projectId) {
                throw new Error("该分析属于其他项目。");
            }
            setRun(initial);
            const messages = new Map<number, string>();
            let lastQuestion: AnalysisQuestion | undefined;
            await client.readEvents(
                runId,
                (event) => {
                    if (event.type === "assistant_message" && typeof event.payload.text === "string") {
                        messages.set(event.seq, event.payload.text);
                        setText([...messages.values()].join("\n\n"));
                    }
                    if (event.type === "user_question" && typeof event.payload.question === "string") {
                        const options = Array.isArray(event.payload.options) ? event.payload.options : [];
                        lastQuestion = {
                            id: `${runId}:${event.seq}`,
                            text: event.payload.question,
                            options: options.flatMap((option) => (typeof option?.label === "string" ? [option.label] : [])),
                            allowFreeform: event.payload.allowFreeform === true,
                        };
                    }
                },
                { signal: controller.signal, onSnapshot: setRun },
            );
            controller.signal.throwIfAborted();
            refreshDirectory((value) => value + 1);
            if (lastQuestion && initial.conversationId) {
                const queue = await client.getConversationQueue(initial.conversationId, controller.signal);
                if (!controller.signal.aborted && queue.awaitingQuestionId === lastQuestion.id) setQuestion(lastQuestion);
            }
        })().catch((cause) => {
            if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : "分析状态读取失败");
        });
        return () => controller.abort();
    }, [client, runId, projectId, episodeId]);
    useEffect(() => {
        if (!awaitingRun || !run?.conversationId) return;
        const controller = new AbortController();
        const conversationId = run.conversationId;
        const timer = window.setInterval(() => {
            void client
                .getConversationQueue(conversationId, controller.signal)
                .then((queue) => {
                    if (!controller.signal.aborted && queue.latestRunId !== awaitingRun) {
                        setAwaitingRun("");
                        setParams(
                            (previous) => {
                                const next = new URLSearchParams(previous);
                                next.set("agentRun", queue.latestRunId);
                                return next;
                            },
                            { replace: true },
                        );
                    }
                })
                .catch((cause) => {
                    if (!controller.signal.aborted) setError(String(cause.message));
                });
        }, 1500);
        return () => {
            controller.abort();
            window.clearInterval(timer);
        };
    }, [awaitingRun, client, run?.conversationId, setParams]);
    const current = run?.id === runId ? run : undefined;
    const active = current && !["completed", "failed", "cancelled", "rejected"].includes(current.status);
    const conversation = conversations.find((item) => item.id === current?.conversationId);
    const canContinue = current && conversation?.latestRunId === current.id && conversation.resumable && !conversation.archived && !current.cleanupPending;
    const start = async (mode: "new" | "continue" | "answer", answer = prompt) => {
        const identity = viewIdentity.current;
        setPending(true);
        setError("");
        try {
            if (mode !== "new" && (!current || !canContinue)) return;
            const targetEpisode = mode === "new" ? episodeId : current?.dramaEpisodeId || episodeId;
            const draft = dramaAgentAnalysisRequest({ projectId, episodeId: targetEpisode, model, prompt: answer, maxCredits: credits ?? 0, idempotencyKey: crypto.randomUUID() });
            if (submission.current?.prompt !== draft.prompt || submission.current?.dramaEpisodeId !== targetEpisode) submission.current = draft;
            const request = submission.current!;
            let nextRunId: string | undefined;
            if (mode === "answer" && current?.conversationId && question) {
                const receipt = await client.sendMessage(current.conversationId, {
                    mode: "interject",
                    expectedRunId: current.id,
                    replyToQuestionId: question.id,
                    request,
                });
                nextRunId = receipt.runId;
            } else {
                const receipt = mode === "continue" && current ? await client.continueRun(current.id, request) : await client.startRun(request);
                nextRunId = receipt.run.id;
            }
            if (identity !== viewIdentity.current) return;
            if (nextRunId) selectRun(nextRunId);
            else if (current) setAwaitingRun(current.id);
            setQuestion(undefined);
            submission.current = undefined;
        } catch (cause) {
            if (identity === viewIdentity.current) setError(cause instanceof Error ? cause.message : "发起分析失败");
        } finally {
            if (identity === viewIdentity.current) setPending(false);
        }
    };
    const canSubmit = !pending && !active && !awaitingRun && Boolean(prompt.trim() && credits);
    return (
        <section aria-label="本集 Agent 分析" className="drama-agent">
            <header className="drama-agent-header">
                <div className="drama-agent-heading">
                    <Clapperboard aria-hidden="true" size={20} />
                    <h2>本集分析建议</h2>
                    <span className="drama-agent-scope">
                        <LockKeyhole aria-hidden="true" size={12} />
                        只读
                    </span>
                </div>
            </header>
            <div className="drama-agent-directory" role="group" aria-label="分析历史">
                <History size={16} aria-hidden="true" />
                <Select
                    aria-label="分析会话"
                    placeholder="恢复分析会话"
                    className="min-w-48"
                    value={conversation?.id}
                    options={conversations
                        .filter((item) => !item.archived || item.id === conversation?.id)
                        .map((item) => ({
                            value: item.id,
                            label: item.title || new Date(item.createdAt).toLocaleString(),
                        }))}
                    onChange={(id) => {
                        setAwaitingRun("");
                        selectRun(conversations.find((item) => item.id === id)?.latestRunId || "");
                    }}
                />
                {cursor && (
                    <Button
                        onClick={() => {
                            const identity = viewIdentity.current;
                            void client
                                .listConversations({ before: cursor })
                                .then((page) => {
                                    if (identity !== viewIdentity.current) return;
                                    setConversations((previous) => [...previous, ...page.items]);
                                    setCursor(page.nextCursor);
                                })
                                .catch((cause) => {
                                    if (identity === viewIdentity.current) setError(String(cause.message));
                                });
                        }}
                    >
                        更多会话
                    </Button>
                )}
                {turns.length > 1 && <Select aria-label="会话历史轮次" value={runId} className="min-w-48" options={turns.map((turn, index) => ({ value: turn.id, label: `${index + 1}. ${turn.userPrompt || turn.status}` }))} onChange={selectRun} />}
                {conversation && !conversation.archived && (
                    <Button
                        type="text"
                        icon={<Archive size={15} />}
                        onClick={() => {
                            const identity = viewIdentity.current;
                            void client
                                .archiveConversation(conversation.id)
                                .then(() => {
                                    if (identity === viewIdentity.current) refreshDirectory((value) => value + 1);
                                })
                                .catch((cause) => {
                                    if (identity === viewIdentity.current) setError(String(cause.message));
                                });
                        }}
                    >
                        归档会话
                    </Button>
                )}
            </div>
            <div className="drama-agent-thread">
                {current?.userPrompt && <p className="drama-agent-message">{current.userPrompt}</p>}
                {current && text && (
                    <AIMessageMarkdown className="drama-agent-answer" streamingAnimation="none">
                        {text}
                    </AIMessageMarkdown>
                )}
                {current && (
                    <div className="drama-agent-progress">
                        {active && current.status !== "waiting_approval" && !text ? <ThinkingLoader phases={[statusLabels[current.status]]} phase={0} size="sm" variant="plain" /> : <span role="status">{statusLabels[current.status]}</span>}
                        {current.spentCredits !== undefined && <span>已用 {current.spentCredits} 积分</span>}
                    </div>
                )}
                {current?.failureMessage && <Alert type="error" message={current.failureMessage} showIcon />}
                {awaitingRun && (
                    <p className="drama-agent-progress" role="status">
                        回答已加入队列，等待下一轮开始。
                    </p>
                )}
                {error && <Alert type="error" message={error} showIcon />}
            </div>
            {question && canContinue && (
                <Alert
                    type="info"
                    message={question.text}
                    description={
                        <Space wrap>
                            {question.options.map((option, index) => (
                                <Button key={`${index}:${option}`} disabled={pending || !credits} onClick={() => void start("answer", option)}>
                                    {option}
                                </Button>
                            ))}
                        </Space>
                    }
                />
            )}
            <div className="drama-agent-composer">
                <Input.TextArea
                    variant="borderless"
                    aria-label="分析要求"
                    value={prompt}
                    autoSize={{ minRows: 2, maxRows: 7 }}
                    disabled={pending || Boolean(active)}
                    onChange={(event) => {
                        submission.current = undefined;
                        setPrompt(event.target.value);
                    }}
                />
                <div className="drama-agent-composer-actions">
                    <label className="drama-agent-budget">
                        <span>积分上限</span>
                        <InputNumber
                            aria-label="分析积分上限"
                            size="small"
                            min={0.01}
                            max={1000000}
                            value={credits}
                            disabled={pending || Boolean(active)}
                            onChange={(value) => {
                                submission.current = undefined;
                                setCredits(value);
                            }}
                        />
                    </label>
                    <Space wrap className="drama-agent-send">
                        {active && current.status !== "waiting_approval" ? (
                            <Button
                                icon={<Square size={14} />}
                                onClick={() => {
                                    const identity = viewIdentity.current;
                                    void client.cancelRun(current.id).catch((cause) => {
                                        if (identity === viewIdentity.current) setError(String(cause.message));
                                    });
                                }}
                            >
                                取消本轮
                            </Button>
                        ) : question && canContinue && question.allowFreeform ? (
                            <Button type="primary" icon={<ArrowUp size={16} />} disabled={!canSubmit} loading={pending} onClick={() => void start("answer")}>
                                发送自定义回答
                            </Button>
                        ) : (
                            <>
                                <Button type={canContinue && !question ? "default" : "primary"} icon={canContinue && !question ? undefined : <ArrowUp size={16} />} onClick={() => void start("new")} loading={pending} disabled={!canSubmit}>
                                    新建分析
                                </Button>
                                {canContinue && !question && (
                                    <Button type="primary" icon={<ArrowUp size={16} />} disabled={!canSubmit} onClick={() => void start("continue")}>
                                        继续会话
                                    </Button>
                                )}
                            </>
                        )}
                    </Space>
                </div>
            </div>
        </section>
    );
}
