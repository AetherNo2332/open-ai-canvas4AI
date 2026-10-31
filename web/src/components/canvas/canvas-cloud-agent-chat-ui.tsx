import { agentCanvasActions, agentCanvasActionLabel } from "@/lib/canvas/agent-canvas-actions";
import { Button, Typography } from "antd";
import { Tooltip } from "@/components/ui/base/tooltip";
import { memo, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ClipboardEvent as ReactClipboardEvent, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";

import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import {
    ArrowLeft,
    ArrowUp,
    AtSign,
    Bookmark,
    CheckCircle2,
    ChevronDown,
    ChevronUp,
    CircleAlert,
    CircleDot,
    Clapperboard,
    HelpCircle,
    ImagePlus,
    Info,
    Layers3,
    ListChecks,
    LoaderCircle,
    Palette,
    RotateCcw,
    Shapes,
    Share2,
    ShoppingBag,
    Sparkles,
    Square,
    X,
    XCircle,
} from "lucide-react";

import { canvasThemes } from "@/lib/canvas-theme";
import { useAppearanceStore } from "@/stores/use-appearance-store";
import { AIMessageMarkdown } from "@/components/ai/ai-message-markdown";
import { WorkingGlow } from "@/components/ai/working-indicator";
import { CanvasResourceMentionTextarea } from "./canvas-resource-mention-textarea";
import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import type { Skill, SkillPreset } from "@/services/api/skills";
import type { AgentSendMode } from "@/services/api/agent";
import { AgentSendModeButton } from "./canvas-agent-message-queue";
import { buildSkillMentionReferences } from "@/services/skill-runtime";
import { agentToolCategory, agentToolCategoryLabel, agentToolErrorClassLabel, agentToolName, agentToolRetryLabel, agentToolStatus, friendlyAgentToolSummary, isAgentCanvasReadTool, type AgentToolCategory } from "@/lib/canvas/agent-tool-presentation";
import { agentOperationCategory, agentOperationFailed, agentOperationSegmentLabel } from "@/lib/canvas/agent-operation-feed";
import { agentToolRetry, type AgentToolRetryAttempt } from "@/lib/canvas/agent-tool-retry";
import { AgentMemoryUpdateCard, type CloudAgentMemoryUpdate } from "./canvas-cloud-agent-memory-cards";
import { AgentReplyOptionCards, agentReplyOptionsTarget } from "./canvas-agent-reply-options";
import { AgentChevronGlyph, AgentThinkGlyph, agentActivityGlyph } from "./agent-activity-icons";
import type { AgentReplyOptions } from "@/lib/canvas/agent-reply-options";
import { agentErrorContent } from "@/lib/canvas/agent-error-presentation";
import { buildAgentFeedSegments } from "@/lib/canvas/agent-operation-feed";
import type { Components } from "streamdown";

export type CloudAgentChatAttachment = { id: string; name: string; url: string };
type CloudAgentOperationImpact = {
    operationCount: number;
    affectedNodeCount: number;
    destructiveCount: number;
    generationCount: number;
    items: string[];
    warning?: string;
};
export type CloudAgentPlanItem = { id: string; title: string; status: "pending" | "doing" | "done" };
export type CloudAgentUserQuestion = {
    question: string;
    options: Array<{ label: string; detail?: string }>;
    allowFreeform?: boolean;
};
export type CloudAgentChatMessage = {
    id: string;
    role: "user" | "assistant" | "system" | "tool" | "error";
    title?: string;
    text: string;
    /** 错误卡的严重程度：warning 用警示色（例如自动重试中），缺省按 error。 */
    errorSeverity?: "warning" | "error";
    streaming?: boolean;
    reasoning?: boolean;
    /**
     * 本轮最终答复（服务端收尾闸门的结论）。false 表示这是过程正文——模型边做边说的那一段，
     * 不是结论：运行还在继续，或者这次收尾被闸门拦下了。缺省按 true 处理（老后端没有这个字段）。
     * 界面不再为它单独出角标（用户口径），但标记仍如实保存。
     */
    final?: boolean;
    planItems?: CloudAgentPlanItem[];
    /** 运行已进入终态，但计划项仍未全部完成；用于历史恢复时停止显示 loading。 */
    planTerminal?: boolean;
    question?: CloudAgentUserQuestion;
    meta?: string;
    runId?: string;
    detail?: unknown;
    attachments?: CloudAgentChatAttachment[];
    /** 由画布节点发起的请求（如「根据此图…」）：正文里的“此图”渲染成该节点的 chip。 */
    canvasReferenceNodeId?: string;
    /** withdrawn 由撤回插话（U8）写入；当前只保留状态与标签。 */
    interjection?: AgentInterjectionStatus;
    /** memory_updated 事件：渲染为记忆变更卡（可撤销 / 等确认）。 */
    memoryUpdate?: CloudAgentMemoryUpdate;
};

/**
 * 助手正文的收尾语义（工作项 A）：final=false 是过程说明，true / 缺省是结论。
 * 缺省按"结论"处理是为了兼容升级前的后端事件——那时所有正文都按最终正文展示。
 */
export function agentAssistantFinality(payload: Record<string, unknown>): boolean {
    return payload.final !== false;
}

/**
 * 服务端控制消息（例如运行时的收尾闸门 `completion_blocked`）在时间线里的表示：
 * 它是服务端说明，不是用户发言，所以走 system 行——绝不能渲染成真人 user 气泡。
 */
export function agentControlMessage(id: string, text: string, meta?: string): CloudAgentChatMessage {
    return { id, role: "system", text, meta };
}

/**
 * 类别的图标语言：清单用节点网格（读到存在与规模）、信息用文档、画面用眼睛（图片字节真的
 * 交给了模型）、创建用加号方块、写画布用笔尖；未登记的工具走中性的滑杆，自动纠正用回转箭头。
 */
function agentToolCategoryIcon(toolName: string, category: AgentToolCategory, retry = false) {
    return agentActivityGlyph(category, { canvasInventory: isAgentCanvasReadTool(toolName), retry });
}

export type CloudAgentQuickAction = { label: string; prompt: string };

/**
 * Turn the short numbered choices the Agent already emits into real UI actions.
 * This deliberately stays conservative: only assistant messages with 1–4
 * numbered lines are eligible, and code blocks are ignored.
 */
export function extractCloudAgentQuickActions(text: string): CloudAgentQuickAction[] {
    if (!text.trim() || text.includes("```")) return [];
    const actions: CloudAgentQuickAction[] = [];
    const seen = new Set<string>();
    for (const line of text.split(/\r?\n/u)) {
        const match = /^\s*(?:[-*]\s*)?(\d{1,2})[.)、]\s*(.+?)\s*$/u.exec(line);
        if (!match) continue;
        const label = match[2].replace(/^[*_\s]+|[*_\s]+$/gu, "").trim();
        if (!label || label.length > 96 || seen.has(label)) continue;
        seen.add(label);
        actions.push({ label, prompt: label });
        if (actions.length >= 4) break;
    }
    return actions;
}

const WORKING_TEXT = "正在处理";
const MIN_AGENT_PROMPT_HEIGHT = 60;
const MAX_AGENT_PROMPT_HEIGHT = 240;

function clampAgentPromptHeight(height: number) {
    return Math.min(MAX_AGENT_PROMPT_HEIGHT, Math.max(MIN_AGENT_PROMPT_HEIGHT, Math.ceil(height)));
}

const AGENT_NODE_LINK_PREFIX = "#agent-node:";
const AGENT_NODE_TYPE_LABELS: Record<string, string> = {
    image: "图片节点",
    video: "视频节点",
    audio: "音频节点",
    text: "文本节点",
    script: "脚本节点",
    drawing: "绘图节点",
    config: "工作流节点",
    frame: "画框节点",
    markdown: "Markdown 节点",
    svg: "SVG 节点",
    html: "HTML 节点",
    panorama: "全景节点",
    compare: "对比节点",
    chart: "图表节点",
    colorgrade: "调色节点",
    "media-conversion": "转码节点",
    "batch-table": "批量表节点",
};
const AGENT_NODE_TYPE_PREFIXES = Object.keys(AGENT_NODE_TYPE_LABELS).sort((left, right) => right.length - left.length);
const BUILTIN_AGENT_NODE_ID_PATTERN = `(?:${AGENT_NODE_TYPE_PREFIXES.join("|")})-[A-Za-z0-9][A-Za-z0-9_-]*`;

function escapeAgentNodePattern(value: string) {
    return value.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
}

function agentNodeTypeLabel(nodeId: string, reference?: CanvasResourceReference) {
    // 角色卡底层是 text 节点，sourceType 会是 text；按资源 kind 先识别，避免报成"文本节点"。
    if (reference?.kind === "character") return "角色卡";
    const sourceType = String(reference?.sourceType || "");
    if (sourceType && AGENT_NODE_TYPE_LABELS[sourceType]) return AGENT_NODE_TYPE_LABELS[sourceType];
    const prefix = AGENT_NODE_TYPE_PREFIXES.find((candidate) => nodeId.startsWith(`${candidate}-`));
    return (prefix && AGENT_NODE_TYPE_LABELS[prefix]) || (reference?.kind ? `${reference.kind} 节点` : "画布节点");
}

function agentNodeLinkLabel(nodeId: string, reference?: CanvasResourceReference) {
    const kindLabel = agentNodeTypeLabel(nodeId, reference);
    const title = (reference?.title || reference?.label || "").replace(/\s+/gu, " ").trim();
    return title ? `${kindLabel} · ${title}` : kindLabel;
}

function createAgentNodeLink(nodeId: string, reference?: CanvasResourceReference) {
    return `[${agentNodeLinkLabel(nodeId, reference).replace(/[\\[\]]/gu, "\\$&")}](${AGENT_NODE_LINK_PREFIX}${encodeURIComponent(nodeId)})`;
}

/**
 * 把助手 Markdown 里的画布节点 ID 换成语义链接（渲染时再变成节点 chip）。
 * 围栏代码保持原样；“新节点 ID：”这类实现措辞缩成“新节点：”，用户看到的是节点卡而不是 ID。
 */
export function rewriteAgentNodeLinks(text: string, references: readonly CanvasResourceReference[] = []) {
    if (!text) return text;
    const referenceByNodeId = new Map(references.filter((reference) => reference.nodeId).map((reference) => [reference.nodeId, reference]));
    const exactNodeIds = [...referenceByNodeId.keys()].sort((left, right) => right.length - left.length).map(escapeAgentNodePattern);
    const nodeIdPattern = exactNodeIds.length ? `(?:${exactNodeIds.join("|")}|${BUILTIN_AGENT_NODE_ID_PATTERN})` : BUILTIN_AGENT_NODE_ID_PATTERN;
    const nodeTokenPattern = new RegExp(`(?<![A-Za-z0-9_-])(?:\\x60(${nodeIdPattern})\\x60|(${nodeIdPattern}))(?![A-Za-z0-9_-])`, "gu");
    const newNodePrefixPattern = /(新节点|节点|新建节点)\s*ID(?=\s*[：:])/gu;
    let inFence = false;
    return text
        .split(/(\r?\n)/u)
        .map((part) => {
            if (/^\r?\n$/u.test(part)) return part;
            if (/^\s{0,3}(`{3,}|~{3,})/u.test(part)) {
                inFence = !inFence;
                return part;
            }
            if (inFence) return part;
            return part.replace(newNodePrefixPattern, "$1").replace(nodeTokenPattern, (match, backtickedId: string | undefined, plainId: string | undefined) => {
                const nodeId = backtickedId || plainId;
                return nodeId ? createAgentNodeLink(nodeId, referenceByNodeId.get(nodeId)) : match;
            });
        })
        .join("");
}

function agentNodeIdFromHref(href?: string) {
    if (!href?.startsWith(AGENT_NODE_LINK_PREFIX)) return null;
    try {
        return decodeURIComponent(href.slice(AGENT_NODE_LINK_PREFIX.length)) || null;
    } catch {
        return null;
    }
}

/**
 * 节点链接的渲染器。画布里（有 onFocusNode）渲染成可点击的节点 chip；画布外没有可定位的
 * 节点，只渲染成纯文本，避免出现点了没反应的按钮。
 */
function createAgentMessageMarkdownComponents(references: readonly CanvasResourceReference[], onFocusNode?: (nodeId: string) => void): Components {
    const referenceByNodeId = new Map(references.filter((reference) => reference.nodeId).map((reference) => [reference.nodeId, reference]));
    return {
        a: ({ children, href, className, ...props }) => {
            const nodeId = agentNodeIdFromHref(href);
            if (!nodeId) {
                return (
                    <a {...props} href={href} className={`ai-message-markdown-link ${className || ""}`.trim()} target="_blank" rel="noreferrer">
                        {children}
                    </a>
                );
            }
            if (!onFocusNode) return <span className="agent-message-node-text">{children}</span>;
            const reference = referenceByNodeId.get(nodeId);
            const title = reference?.title || reference?.label || "画布节点";
            return (
                <a
                    {...props}
                    href={href}
                    className="agent-message-node-link"
                    data-agent-node-id={nodeId}
                    aria-label={`定位到画布中的${title}`}
                    title={`定位到画布中的${title}`}
                    onClick={(event) => {
                        event.preventDefault();
                        onFocusNode(nodeId);
                    }}
                >
                    {reference?.previewUrl ? (
                        <img className="agent-message-node-link-preview" src={reference.previewUrl} alt="" loading="lazy" />
                    ) : (
                        <span className="agent-message-node-link-icon" aria-hidden="true">
                            <CircleDot className="size-3.5" />
                        </span>
                    )}
                    <span className="agent-message-node-link-copy">
                        <span className="agent-message-node-link-kind">{agentNodeTypeLabel(nodeId, reference)}</span>
                        <span className="agent-message-node-link-title">{reference?.title || reference?.label || children}</span>
                    </span>
                </a>
            );
        },
    };
}

const EMPTY_REFERENCES: readonly CanvasResourceReference[] = [];

/**
 * 渲染计数探针：只给测试用，默认关闭，生产路径不记录任何数据。
 * 打开后每次 AgentChatMessage 实际渲染都按消息 id 计一次，用来证明流式更新只重渲染当前消息。
 */
export const agentChatRenderProbe: { enabled: boolean; counts: Map<string, number> } = { enabled: false, counts: new Map() };

type AgentChatMessageProps = {
    item: CloudAgentChatMessage;
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    references?: readonly CanvasResourceReference[];
    isStreaming?: boolean;
    retrying?: boolean;
    onRejectTool?: (id: string) => void;
    onApproveTool?: (id: string) => void;
    onRetry?: () => void;
    onFocusNode?: (nodeId: string) => void;
    /** 只给最后一条、还没被回复的 Agent 消息：末尾编号选项改为可点击卡片。 */
    replyOptions?: AgentReplyOptions | null;
    onReplyOption?: (text: string) => void;
    replyOptionsDisabled?: boolean;
};

// 面板输入、流式输出都会让对话列表重渲染；消息组件 memo 后，只有自己的消息或展示参数变了才重新解析 Markdown。
export const AgentChatMessage = memo(function AgentChatMessage({
    item,
    theme,
    references = EMPTY_REFERENCES,
    isStreaming = false,
    retrying = false,
    onRejectTool,
    onApproveTool,
    onRetry,
    onFocusNode,
    replyOptions,
    onReplyOption,
    replyOptionsDisabled = false,
}: AgentChatMessageProps) {
    if (agentChatRenderProbe.enabled) agentChatRenderProbe.counts.set(item.id, (agentChatRenderProbe.counts.get(item.id) ?? 0) + 1);
    const isUser = item.role === "user";
    const isSystem = item.role === "system";
    const isAssistant = item.role === "assistant";
    const displayedText = useTypewriterText(item.text, isAssistant && isStreaming);
    const markdownComponents = useMemo(() => createAgentMessageMarkdownComponents(references, onFocusNode), [onFocusNode, references]);
    const showReplyOptions = Boolean(replyOptions && onReplyOption && isAssistant && !isStreaming);
    const agentMarkdownText = isAssistant ? rewriteAgentNodeLinks(showReplyOptions && replyOptions ? replyOptions.body : displayedText, references) : "";
    // 「根据此图…」这类由画布节点发起的请求：把“此图”换成节点 chip。节点已不在引用表里时保持原句。
    const canvasReference = isUser && item.canvasReferenceNodeId && item.text.includes("此图") ? references.find((reference) => reference.nodeId === item.canvasReferenceNodeId) : undefined;
    const streamingAssistant = isAssistant && isStreaming;
    if (item.reasoning) {
        return <AgentReasoningFeed items={[item]} theme={theme} />;
    }
    if (objectField(item.detail, "eventType") === "context_compaction") {
        return <AgentContextCompactionNotice item={item} theme={theme} />;
    }
    if (item.memoryUpdate) return <AgentMemoryUpdateCard update={item.memoryUpdate} theme={theme} />;
    if (isSystem) {
        return (
            <div className="agent-status-message agent-status-message--system flex items-start gap-2 text-xs" data-agent-enter="">
                <AgentTimelineMarker theme={theme} tone="muted" icon={<Info className="size-3" />} />
                <div className="min-w-0 flex-1 py-0.5 leading-5" style={{ color: theme.node.muted }}>
                    {item.text}
                    {item.meta ? <span className="ml-2 opacity-60">{item.meta}</span> : null}
                </div>
            </div>
        );
    }
    if (item.role === "tool") {
        if (objectField(item.detail, "status") === "pending") return <AgentPendingToolCard summary={item.text} detail={item.detail} theme={theme} onReject={() => onRejectTool?.(item.id)} onApprove={() => onApproveTool?.(item.id)} />;
        return (
            <div className="flex min-w-0 items-start">
                <AgentToolCard title={item.title || "工具调用"} text={item.text} detail={item.detail} theme={theme} references={references} onFocusNode={onFocusNode} />
            </div>
        );
    }
    if (item.role === "error") return <AgentErrorCard item={item} theme={theme} retrying={retrying} onRetry={onRetry} />;
    return (
        <div className={`flex items-start gap-3 ${isUser ? "justify-end" : "justify-start"}`} data-agent-enter={isUser ? "user" : ""}>
            <div
                className={`agent-message-body min-w-0 text-sm leading-6 text-left ${isUser ? "agent-message-user max-w-[82%] px-4 py-3" : "max-w-full flex-1"}${streamingAssistant ? " is-streaming" : ""}`}
                style={{ color: theme.node.text }}
                aria-busy={streamingAssistant || undefined}
            >
                {item.interjection ? (
                    <span className="agent-interjection-tag" data-status={item.interjection}>
                        {AGENT_INTERJECTION_LABELS[item.interjection]}
                    </span>
                ) : null}
                {isAssistant ? (
                    <>
                        {agentMarkdownText ? (
                            <AIMessageMarkdown className="text-left" isStreaming={isStreaming} streamingAnimation="none" components={markdownComponents}>
                                {agentMarkdownText}
                            </AIMessageMarkdown>
                        ) : null}
                        {showReplyOptions && replyOptions && onReplyOption ? <AgentReplyOptionCards options={replyOptions.options} disabled={replyOptionsDisabled} onSelect={(option) => onReplyOption(option.text)} /> : null}
                    </>
                ) : canvasReference ? (
                    <AIMessageMarkdown className="text-left" components={markdownComponents}>
                        {item.text.replace("此图", createAgentNodeLink(canvasReference.nodeId, canvasReference))}
                    </AIMessageMarkdown>
                ) : (
                    <AgentMessageText text={item.text} references={references} />
                )}
                {item.attachments?.length ? <AgentMessageAttachments attachments={item.attachments} /> : null}
                {item.meta ? <div className="mt-1 text-[var(--fs-label)] opacity-45">{item.meta}</div> : null}
            </div>
        </div>
    );
});

/**
 * 错误卡：标题与可操作提示常显，色调按严重程度取皮肤状态色（warning / error）；
 * 诊断号和 Run ID 只在排查时有用，折叠在“诊断信息”后面，默认收起。
 */
function AgentErrorCard({ item, theme, retrying, onRetry }: { item: CloudAgentChatMessage; theme: (typeof canvasThemes)[keyof typeof canvasThemes]; retrying: boolean; onRetry?: () => void }) {
    const tone = item.errorSeverity === "warning" ? "warning" : "error";
    const title = item.title || (tone === "warning" ? "Agent 正在重试" : "Agent 暂时无法继续");
    const { description, diagnosticId } = agentErrorContent(item.text, title);
    return (
        <div className={`agent-status-message agent-status-message--${tone} agent-error-message`} data-agent-enter="">
            <span className="agent-error-icon" aria-hidden="true">
                <CircleAlert className="size-3.5" />
            </span>
            <div className="min-w-0 flex-1">
                <div role="status">
                    <div className="agent-error-title">{title}</div>
                    {description ? <p className="agent-error-detail">{description}</p> : null}
                </div>
                {item.meta ? <p className="agent-error-meta">{item.meta}</p> : null}
                {diagnosticId || item.runId ? (
                    <details className="agent-error-diagnostics">
                        <summary>
                            <ChevronDown className="size-3" aria-hidden="true" />
                            诊断信息
                        </summary>
                        <div className="agent-error-diagnostics-body">
                            {diagnosticId ? (
                                <>
                                    <p>如反复出现，请将诊断号反馈给管理员。</p>
                                    <code>{diagnosticId}</code>
                                </>
                            ) : null}
                            {item.runId && item.runId !== diagnosticId ? (
                                <p className="break-all">
                                    <Typography.Text style={{ color: "inherit" }} copyable={{ text: item.runId, tooltips: ["复制 Run ID", "已复制"], icon: <span aria-label="复制 Run ID">复制</span> }}>
                                        Run ID：<span className="font-mono select-text">{item.runId}</span>
                                    </Typography.Text>
                                </p>
                            ) : null}
                        </div>
                    </details>
                ) : null}
                {onRetry ? (
                    <Button type="text" size="small" className="agent-error-retry" icon={retrying ? <LoaderCircle className="size-3.5 animate-spin" /> : <RotateCcw className="size-3.5" />} disabled={retrying} onClick={onRetry}>
                        {retrying ? "重试中" : "重试本轮"}
                    </Button>
                ) : null}
            </div>
        </div>
    );
}

/** 操作段与推理段每次都由消息列表重新切出新数组；元素都没变就视为同一段，不重渲染。 */
function sameFeedProps<P extends { items: readonly CloudAgentChatMessage[] }>(previous: P, next: P) {
    const keys = Object.keys(next) as Array<keyof P>;
    if (keys.length !== Object.keys(previous).length) return false;
    return keys.every((key) => {
        if (key !== "items") return Object.is(previous[key], next[key]);
        return previous.items === next.items || (previous.items.length === next.items.length && previous.items.every((item, index) => item === next.items[index]));
    });
}

/** 消息只用引用表做节点链接和提及标签；节点正文变了不影响消息展示。 */
function sameAgentLinkReference(left: CanvasResourceReference, right: CanvasResourceReference) {
    return (
        left === right ||
        (left.id === right.id &&
            left.nodeId === right.nodeId &&
            left.kind === right.kind &&
            left.label === right.label &&
            left.title === right.title &&
            left.previewUrl === right.previewUrl &&
            left.mentionToken === right.mentionToken &&
            left.sourceType === right.sourceType &&
            left.assetId === right.assetId &&
            left.skill === right.skill)
    );
}

/**
 * 面板每次渲染都会拼出新的引用表；链接字段都相同就沿用上一份，消息不会因此重新解析 Markdown。
 * 输入框的 @ 菜单仍拿最新的完整引用表。
 */
function useLinkStableReferences(references: readonly CanvasResourceReference[]) {
    const previousRef = useRef(references);
    const previous = previousRef.current;
    const stable = previous.length === references.length && previous.every((item, index) => sameAgentLinkReference(item, references[index])) ? previous : references;
    useLayoutEffect(() => {
        previousRef.current = stable;
    }, [stable]);
    return stable;
}

/**
 * 对话消息列表：把消息切成正文 / 思考 / 操作段，并给 memo 过的子组件传稳定的回调与引用表，
 * 流式输出某一条时其余消息不重渲染。回复选项只挂在最后一条未被回复的 Agent 消息上。
 */
export function AgentMessageList({
    messages,
    theme,
    references,
    busy,
    streamingPaused = false,
    onFocusNode,
    onReplyOption,
    replyOptionsDisabled = false,
}: {
    messages: CloudAgentChatMessage[];
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    references: readonly CanvasResourceReference[];
    busy: boolean;
    /** 等待审批等情况：本轮没停，但正文不在流式输出，也不该出选项卡片。 */
    streamingPaused?: boolean;
    onFocusNode?: (nodeId: string) => void;
    /** 不传表示当前不该出现选项卡片（画布外、ask_user 提问条已在展示）。 */
    onReplyOption?: (text: string) => void;
    replyOptionsDisabled?: boolean;
}) {
    const segments = useMemo(() => buildAgentFeedSegments(messages), [messages]);
    const lastMessage = messages.at(-1);
    const replyOptionsEnabled = Boolean(onReplyOption) && !busy && !streamingPaused;
    const replyTarget = useMemo(() => (replyOptionsEnabled ? agentReplyOptionsTarget(messages) : null), [messages, replyOptionsEnabled]);
    // 父组件每次渲染都会传入新的回调；换成稳定引用，memo 过的消息才能跳过与它无关的重渲染。
    const handlersRef = useRef({ onFocusNode, onReplyOption });
    useLayoutEffect(() => {
        handlersRef.current = { onFocusNode, onReplyOption };
    });
    const stableFocus = useCallback((nodeId: string) => handlersRef.current.onFocusNode?.(nodeId), []);
    const stableReply = useCallback((text: string) => handlersRef.current.onReplyOption?.(text), []);
    const focus = onFocusNode ? stableFocus : undefined;
    const linkReferences = useLinkStableReferences(references);
    return (
        <>
            {segments.map((segment) =>
                segment.kind === "operations" ? (
                    // 聚合后按最新事件判断当前动作，不按行在列表里的位置判断。
                    <AgentOperationFeed key={segment.key} items={segment.items} theme={theme} references={linkReferences} onFocusNode={focus} live={busy && segment.items.at(-1) === lastMessage} />
                ) : segment.kind === "reasoning" ? (
                    <AgentReasoningFeed key={segment.key} items={segment.items} theme={theme} />
                ) : (
                    <AgentChatMessage
                        key={segment.key}
                        item={segment.item}
                        theme={theme}
                        references={linkReferences}
                        onFocusNode={focus}
                        isStreaming={busy && !streamingPaused && segment.item.streaming === true && segment.item === lastMessage}
                        replyOptions={replyTarget?.id === segment.item.id ? replyTarget.options : null}
                        onReplyOption={replyTarget?.id === segment.item.id ? stableReply : undefined}
                        replyOptionsDisabled={replyOptionsDisabled}
                    />
                ),
            )}
        </>
    );
}

/**
 * 推理摘要默认只保留一行入口；流式更新不改变用户手动展开的状态。
 */
function agentReasoningPreview(items: readonly CloudAgentChatMessage[]) {
    for (let index = items.length - 1; index >= 0; index -= 1) {
        const line = items[index].text
            .split(/\r?\n/u)
            .map((value) => value.replace(/^[#>*\-\s]+|\*\*|`/gu, "").trim())
            .find(Boolean);
        if (line) return line.length > 160 ? `${line.slice(0, 160)}…` : line;
    }
    return "";
}

/**
 * 推理是辅助信息，不与正文争夺主视觉：连续摘要合成一行，收起时用最新一段的首行做预览（与上游一致）。
 */
export const AgentReasoningFeed = memo(function AgentReasoningFeed({
    items,
    theme,
}: {
    items: readonly CloudAgentChatMessage[];
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
}) {
    const [expanded, setExpanded] = useState(false);
    const contentId = useId();
    const streaming = items.some((item) => item.streaming);
    const text = items.map((item) => item.text.trim()).filter(Boolean).join("\n\n");
    const preview = agentReasoningPreview(items);
    const countLabel = !streaming && items.length > 1 ? `${items.length} 段` : "";
    return (
        <div className={`agent-activity agent-reasoning${expanded ? " is-open" : ""}${streaming ? " is-live" : ""}`} data-agent-tone="think" style={{ "--agent-reasoning-accent": theme.accent.primary } as CSSProperties}>
            <button type="button" className="agent-activity-row" aria-expanded={expanded} aria-controls={contentId} aria-label={`${expanded ? "收起" : "展开"}思考过程`} onClick={() => setExpanded((current) => !current)}>
                <span className="agent-activity-glyph" aria-hidden="true">
                    <AgentThinkGlyph />
                </span>
                <span className="agent-activity-title">{streaming ? "正在思考" : "思考过程"}</span>
                {preview && !expanded ? <span className="agent-activity-detail agent-reasoning-preview">{preview}</span> : null}
                {countLabel ? <span className="agent-activity-meta">{countLabel}</span> : null}
                <AgentChevronGlyph className="agent-activity-chevron" />
            </button>
            <AgentFeedDisclosure expanded={expanded} id={contentId} label="思考过程">
                <div className="agent-activity-body agent-reasoning-content" data-canvas-wheel-scroll>
                    <div className="agent-reasoning-text">{text || (streaming ? "正在整理思路…" : "暂无可展示的推理摘要")}</div>
                </div>
            </AgentFeedDisclosure>
        </div>
    );
}, sameFeedProps);

/** 同时保留退出动画与辅助技术的即时收起语义；时长读取当前站点皮肤。 */
function AgentFeedDisclosure({ expanded, id, label, children }: { expanded: boolean; id: string; label: string; children: ReactNode }) {
    const reducedMotion = useReducedMotion();
    const duration = useAppearanceStore((state) => state.appearance.activeSkin.tokens.components.motionNormal) / 1000;
    return <div className="agent-feed-disclosure" inert={!expanded} aria-hidden={!expanded}>
        <AnimatePresence initial={false}>
            {expanded ? <motion.div id={id} role="region" aria-label={label} initial={{ height: 0, opacity: 0 }} animate={{ height: "auto", opacity: 1 }} exit={{ height: 0, opacity: 0 }} transition={{ duration: reducedMotion ? 0 : duration, ease: [0.16, 1, 0.3, 1] }}>
                {children}
            </motion.div> : null}
        </AnimatePresence>
    </div>;
}

/**
 * Agent SSE events contain text chunks. Keep the full text in the message
 * state, but reveal one code point at a time so a chunk never appears as a
 * whole block. The loop continues briefly after the stream ends to drain any
 * text that was buffered by the network.
 */
function useTypewriterText(targetText: string, shouldAnimate: boolean) {
    const targetRef = useRef(targetText);
    const visibleRef = useRef(shouldAnimate ? "" : targetText);
    const hasAnimatedRef = useRef(shouldAnimate);
    const runningRef = useRef(false);
    const timerRef = useRef<number | null>(null);
    const [visibleText, setVisibleText] = useState(visibleRef.current);

    targetRef.current = targetText;

    const startLoop = useCallback(() => {
        if (runningRef.current) return;
        runningRef.current = true;

        const step = () => {
            const target = targetRef.current;
            const targetCharacters = Array.from(target);
            const visibleCharacters = Array.from(visibleRef.current);
            const visibleIsPrefix = target.startsWith(visibleRef.current);

            if (!visibleIsPrefix || visibleCharacters.length > targetCharacters.length) {
                visibleRef.current = "";
                setVisibleText("");
            }

            const currentCharacters = Array.from(visibleRef.current);
            if (currentCharacters.length >= targetCharacters.length) {
                runningRef.current = false;
                timerRef.current = null;
                return;
            }

            const nextText = targetCharacters.slice(0, currentCharacters.length + 1).join("");
            visibleRef.current = nextText;
            setVisibleText(nextText);
            timerRef.current = window.setTimeout(step, 16);
        };

        step();
    }, []);

    useEffect(() => {
        if (shouldAnimate) hasAnimatedRef.current = true;
        if (!hasAnimatedRef.current && !shouldAnimate) {
            visibleRef.current = targetText;
            setVisibleText(targetText);
            return;
        }
        startLoop();
    }, [shouldAnimate, startLoop, targetText]);

    useEffect(
        () => () => {
            if (timerRef.current !== null) window.clearTimeout(timerRef.current);
            runningRef.current = false;
        },
        [],
    );

    return visibleText;
}

function AgentMessageText({ text, references }: { text: string; references: readonly CanvasResourceReference[] }) {
    const parts = splitAgentMessageMentions(text, references);
    return (
        <div className="whitespace-pre-wrap break-words text-left">
            {parts.map((part, index) =>
                part.reference ? (
                    <span key={`${part.token}-${index}`} className="agent-message-mention" title={part.reference.title || part.reference.label}>
                        <span aria-hidden="true" className="agent-message-mention-icon">
                            {part.reference.kind === "skill" ? "✦" : "@"}
                        </span>
                        <span>{part.reference.label}</span>
                    </span>
                ) : (
                    <span key={`${part.text}-${index}`}>{part.text}</span>
                ),
            )}
        </div>
    );
}

type AgentMessageMentionPart = { text: string; token?: string; reference?: CanvasResourceReference };

function splitAgentMessageMentions(text: string, references: readonly CanvasResourceReference[]): AgentMessageMentionPart[] {
    if (!text) return [];
    const byToken = new Map<string, CanvasResourceReference>();
    references.forEach((reference) => {
        byToken.set(canvasResourceMentionTokenForAgentMessage(reference), reference);
        if (reference.kind === "skill" && reference.skill?.skillName) byToken.set(`@${reference.skill.skillName}`, reference);
    });
    return text.split(/(@\[[^\]]+\])/gu).map((part) => {
        const reference = byToken.get(part);
        if (reference) return { text: "", token: part, reference };
        // Never expose an internal Skill ID in the chat bubble when an old
        // conversation references a removed/unloaded Skill.
        if (/^@\[skill:[^\]]+\]$/u.test(part)) return { text: "技能包" };
        return { text: part };
    });
}

function canvasResourceMentionTokenForAgentMessage(reference: CanvasResourceReference) {
    if (reference.mentionToken) return reference.mentionToken;
    if (reference.kind === "skill" && reference.skill?.skillId) return `@[skill:${reference.skill.skillId}]`;
    if (reference.assetId) return `@[asset:${reference.assetId}]`;
    return `@[node:${reference.nodeId}]`;
}

export function AgentPendingToolCard({ summary, detail, theme, onReject, onApprove }: { summary: string; detail?: unknown; theme: (typeof canvasThemes)[keyof typeof canvasThemes]; onReject?: () => void; onApprove?: () => void }) {
    const impact = agentImpactFromDetail(detail);
    const friendlySummary = friendlyAgentToolSummary(agentToolName("", detail), summary, detail, true);
    return (
        <div className="agent-status-message flex items-start gap-2">
            <AgentTimelineMarker theme={theme} tone="approval" icon={<CircleAlert className="size-3.5" />} />
            <div className="agent-pending-tool min-w-0 flex-1 rounded-lg border py-2 pl-3 pr-3" style={{ borderColor: "color-mix(in srgb, var(--palette-status-warning) 22%, transparent)", background: "color-mix(in srgb, var(--palette-status-warning) 5%, transparent)", color: theme.node.text }}>
                <div className="flex items-start gap-3">
                    <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold leading-5">
                            <span>需要你的确认</span>
                            <span className="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[var(--fs-label)] font-medium" style={{ color: "var(--palette-status-warning)", background: "color-mix(in srgb, var(--palette-status-warning) 10%, transparent)" }}>
                                等待确认
                            </span>
                        </div>
                        <div className="mt-1 text-xs leading-5" style={{ color: theme.node.text }}>
                            {friendlySummary}
                        </div>
                    </div>
                </div>
                {impact?.operationCount ? (
                    <div className="mt-3 pt-1">
                        <div className="grid grid-cols-4 gap-1">
                            <ImpactMetric label="操作" value={impact.operationCount} theme={theme} />
                            <ImpactMetric label="涉及节点" value={impact.affectedNodeCount} theme={theme} />
                            <ImpactMetric label="删除" value={impact.destructiveCount} attention={impact.destructiveCount > 0} theme={theme} />
                            <ImpactMetric label="生成" value={impact.generationCount} attention={impact.generationCount > 0} theme={theme} />
                        </div>
                        {impact.items.length ? (
                            <div className="mt-3 space-y-1.5">
                                {impact.items.map((item, index) => (
                                    <div key={`${item}-${index}`} className="flex gap-2 text-xs leading-5" style={{ color: theme.node.muted }}>
                                        <span className="mt-2 size-1 shrink-0 rounded-full bg-current" />
                                        <span>{item}</span>
                                    </div>
                                ))}
                            </div>
                        ) : null}
                        {impact.warning ? <div className="mt-3 rounded-md bg-amber-500/[.08] px-2.5 py-2 text-xs leading-5 text-amber-700 dark:text-amber-300">{impact.warning}</div> : null}
                    </div>
                ) : null}
                {onReject || onApprove ? (
                    <div className="mt-2 flex gap-2">
                        <Button danger size="small" className="!h-8 flex-1" icon={<XCircle className="size-3.5" />} onClick={() => onReject?.()}>
                            暂不执行
                        </Button>
                        <Button size="small" className="!h-8 flex-1" icon={<CheckCircle2 className="size-3.5" />} style={{ borderColor: "color-mix(in srgb, var(--palette-status-success) 42%, transparent)", color: "var(--palette-status-success)", background: "transparent" }} onClick={() => onApprove?.()}>
                            确认执行
                        </Button>
                    </div>
                ) : null}
            </div>
        </div>
    );
}

function ImpactMetric({ label, value, attention = false, theme }: { label: string; value: number; attention?: boolean; theme: (typeof canvasThemes)[keyof typeof canvasThemes] }) {
    return (
        <div className="px-1 py-1">
            <div className="text-[var(--fs-tiny)]" style={{ color: theme.node.muted }}>
                {label}
            </div>
            <div className="mt-0.5 text-sm font-semibold tabular-nums" style={{ color: attention ? "var(--palette-status-warning)" : theme.node.text }}>
                {value}
            </div>
        </div>
    );
}

function agentImpactFromDetail(detail: unknown) {
    const impact = objectField(detail, "impact");
    if (!impact || typeof impact !== "object") return null;
    const value = impact as Partial<CloudAgentOperationImpact>;
    return {
        operationCount: Number(value.operationCount) || 0,
        affectedNodeCount: Number(value.affectedNodeCount) || 0,
        destructiveCount: Number(value.destructiveCount) || 0,
        generationCount: Number(value.generationCount) || 0,
        items: Array.isArray(value.items) ? value.items.filter((item): item is string => typeof item === "string") : [],
        warning: typeof value.warning === "string" ? value.warning : "",
    } satisfies CloudAgentOperationImpact;
}

export function AgentToolCard({
    title,
    text,
    detail,
    theme,
    references = EMPTY_REFERENCES,
    onFocusNode,
}: {
    title: string;
    text: string;
    detail?: unknown;
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    references?: readonly CanvasResourceReference[];
    onFocusNode?: (nodeId: string) => void;
}) {
    const state = toolCardState(title, text, detail);
    const toolName = agentToolName(title, detail);
    const category = agentToolCategory(toolName, detail);
    const categoryLabel = agentToolCategoryLabel(toolName, category);
    const summary = friendlyAgentToolSummary(toolName, text, detail);
    const actions = agentCanvasActions(toolName, detail, references);
    const isNodeRead = toolName === "canvas_get_state" && category === "read";
    const [readExpanded, setReadExpanded] = useState(false);
    const visibleActions = actions.slice(0, isNodeRead && !readExpanded ? 1 : 8);
    const collapsedReadNodeCount = isNodeRead ? Math.max(0, actions.length - visibleActions.length) : 0;
    const isPlain = !actions.length && !state.isError;
    const conciseError = text.length > 180 ? `${text.slice(0, 180)}…` : text;
    const retry = agentToolRetry(detail);
    const categoryIcon = agentToolCategoryIcon(toolName, category, Boolean(retry));
    const attempts = objectField(detail, "retryAttempts");
    if (retry && Array.isArray(attempts)) {
        const label = agentToolRetryLabel(retry);
        return (
            <details data-agent-tool-retry className="min-w-0 flex-1 text-xs leading-5" style={{ color: theme.node.muted }}>
                <summary className="cursor-pointer rounded-sm focus-visible:outline focus-visible:outline-2" style={{ outlineColor: theme.node.muted }}>
                    {retry.status === "recovered" ? `${label} · 已恢复` : `${label} · ${retry.attempt}/${retry.maxAttempts} 次尝试未通过`}
                </summary>
                <ol className="mt-2 space-y-1 pl-4" aria-label="自动纠正详情">
                    {(attempts as AgentToolRetryAttempt[]).map((attempt, index) => (
                        <li key={attempt.id} className="whitespace-pre-wrap break-words">
                            第 {index + 1} 次：{attempt.text}
                        </li>
                    ))}
                </ol>
            </details>
        );
    }
    return (
        <div data-agent-tool-card className={`agent-tool-row agent-tool-row--${category}${isPlain ? " agent-tool-row--plain" : ""} flex min-w-0 flex-1 items-start gap-2.5 text-left`} style={{ color: theme.node.text }}>
            <span className="agent-tool-status shrink-0" style={{ color: state.color }} aria-hidden="true">
                {state.icon}
            </span>
            <div className="min-w-0 flex-1 break-words text-xs leading-5" style={{ color: state.isError ? state.color : theme.node.muted }}>
                <div className="agent-tool-header">
                    <span className="agent-tool-category">
                        {categoryIcon}
                        <span>{categoryLabel}</span>
                    </span>
                    <span className="agent-tool-summary">{summary}</span>
                    {state.label !== "已完成" ? (
                        <span className="agent-tool-label" style={{ color: state.color }}>
                            {state.label}
                        </span>
                    ) : null}
                </div>
                {actions.length ? (
                    <div className="agent-tool-action-list">
                        {visibleActions.map((action) => (
                            <button
                                key={`${action.action}-${action.nodeId}`}
                                type="button"
                                data-agent-node-id={action.nodeId}
                                disabled={!onFocusNode}
                                onClick={() => onFocusNode?.(action.nodeId)}
                                aria-label={`在画布中定位${action.title}`}
                                className="agent-tool-action-link"
                            >
                                {agentCanvasActionLabel(action)}
                            </button>
                        ))}
                        {isNodeRead && (collapsedReadNodeCount > 0 || readExpanded) ? (
                            <button type="button" className="agent-tool-more" aria-expanded={readExpanded} onClick={() => setReadExpanded((current) => !current)}>
                                {readExpanded ? `收起其余 ${Math.max(0, actions.length - 1)} 个节点` : `另有 ${collapsedReadNodeCount} 个节点只是清单（未查看画面），展开查看`}
                            </button>
                        ) : null}
                    </div>
                ) : null}
                {state.isError && text && text !== summary ? (
                    <span className="mt-1 block whitespace-pre-wrap break-words" style={{ color: theme.node.muted }}>
                        {conciseError}
                    </span>
                ) : null}
                {state.isError && text.length > 180 ? (
                    <details className="mt-1" style={{ color: theme.node.muted }}>
                        <summary className="cursor-pointer">查看完整错误详情</summary>
                        <p className="whitespace-pre-wrap break-words">{text}</p>
                    </details>
                ) : null}
                {state.isError && objectField(objectField(detail, "result"), "taskId") ? (
                    <span className="mt-1 block break-all" style={{ color: theme.node.muted }}>
                        任务 ID：{String(objectField(objectField(detail, "result"), "taskId"))}
                    </span>
                ) : null}
            </div>
            {state.label === "已完成" ? <span className="sr-only">已完成</span> : null}
        </div>
    );
}

/**
 * Agent 的操作记录（连续的 `role === "tool"` 消息）折成一行：报最新一步，点击展开完整记录。
 *
 * 语义要点：折叠态整段只有这一行可见，所以**类别徽标必须在折叠行上**（清单 / 查看画面 /
 * 修改画布），否则默认状态看不出刚才到底是读了清单还是真看了画面。
 *
 * 失败也保持收起，通过摘要文字和错误色提示；新步骤不重置用户的展开状态。
 * `live` 由面板按"这一段是不是对话末尾且在跑"给出：只有还在推进时才流光。
 */
export const AgentOperationFeed = memo(function AgentOperationFeed({
    items,
    theme,
    references = EMPTY_REFERENCES,
    onFocusNode,
    live = false,
}: {
    items: readonly CloudAgentChatMessage[];
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    references?: readonly CanvasResourceReference[];
    onFocusNode?: (nodeId: string) => void;
    live?: boolean;
}) {
    const [expanded, setExpanded] = useState(false);
    const reducedMotion = useReducedMotion();
    const duration = useAppearanceStore((state) => state.appearance.activeSkin.tokens.components.motionNormal) / 1000;
    const listId = useId();
    const latest = items[items.length - 1];
    if (!latest) return null;
    const category = agentOperationCategory(latest);
    const label = agentOperationSegmentLabel(items);
    // 已自动纠正的旧错误不污染最新一步的状态。
    const failed = agentOperationFailed(latest);
    const shimmering = live && !failed;
    const latestToolName = agentToolName(latest.title || "工具执行", latest.detail);
    const latestGlyph = agentToolCategoryIcon(latestToolName, category, Boolean(agentToolRetry(latest.detail)));
    const latestRetry = agentToolRetry(latest.detail);
    // 自动纠正不属于任何工具类别；标题直接写“自动纠正”（与上游一致）。
    const categoryLabel = latestRetry ? "自动纠正" : agentToolCategoryLabel(latestToolName, category);
    return (
        <div
            className={`agent-activity agent-operation-feed${expanded ? " is-open" : ""}${failed ? " is-failed" : ""}${shimmering ? " is-live" : ""}`}
            data-agent-operation-feed
            data-agent-category={category}
            data-agent-tone={failed ? "error" : latestRetry ? (latestRetry.status === "recovered" ? "other" : "warning") : category}
        >
            <button
                type="button"
                className="agent-activity-row agent-operation-toggle"
                aria-expanded={expanded}
                aria-controls={listId}
                aria-label={`${expanded ? "收起" : "展开"} ${items.length} 步${categoryLabel}记录，最新一步：${label}`}
                onClick={() => setExpanded((current) => !current)}
            >
                <span className="agent-activity-glyph" aria-hidden="true">
                    {latestGlyph}
                </span>
                <span className="agent-activity-title">{categoryLabel}</span>
                {/* 换步时旧文案淡出上移、新文案从下方上浮；只动 transform / opacity，减少动态效果时只换不动。 */}
                <AnimatePresence mode="wait" initial={false}>
                    <motion.span
                        key={label}
                        className="agent-activity-detail agent-operation-latest"
                        initial={reducedMotion ? false : { opacity: 0, y: 6 }}
                        animate={{ opacity: 1, y: 0 }}
                        exit={reducedMotion ? { opacity: 0, transition: { duration: 0 } } : { opacity: 0, y: -4 }}
                        transition={{ duration: reducedMotion ? 0 : duration, ease: [0.16, 1, 0.3, 1] }}
                    >
                        {label}
                    </motion.span>
                </AnimatePresence>
                {items.length > 1 ? <span className="agent-activity-meta">{items.length} 步</span> : null}
                <AgentChevronGlyph className="agent-activity-chevron" />
            </button>
            <AgentFeedDisclosure expanded={expanded} id={listId} label="工具调用操作记录">
                <div className="agent-activity-body agent-operation-list">
                    {items.map((item) => (
                        <AgentChatMessage key={item.id} item={item} theme={theme} references={references} onFocusNode={onFocusNode} />
                    ))}
                </div>
            </AgentFeedDisclosure>
        </div>
    );
}, sameFeedProps);

/**
 * 运行结束后的撤销入口：撤销本轮对画布的全部修改。它不是本卡片的主要命令，
 * 所以用 default 按钮；结果由画布实时同步呈现，冲突提示由调用方展示。
 */
export function AgentRunUndoAction({ theme, pending, onUndo }: { theme: (typeof canvasThemes)[keyof typeof canvasThemes]; pending: boolean; onUndo: () => void }) {
    return (
        <div className="agent-status-message flex items-center gap-2 text-xs">
            <AgentTimelineMarker theme={theme} tone="muted" icon={<RotateCcw className="size-3" />} />
            <span className="min-w-0 flex-1 leading-5" style={{ color: theme.node.muted }}>
                本轮已结束，可撤销本轮对画布的修改
            </span>
            <Button type="default" size="small" disabled={pending} icon={pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <RotateCcw className="size-3.5" />} onClick={onUndo}>
                {pending ? "正在撤销…" : "撤销本次修改"}
            </Button>
        </div>
    );
}

export function AgentWorkingMessage({ theme, label = WORKING_TEXT }: { theme: (typeof canvasThemes)[keyof typeof canvasThemes]; label?: string }) {
    return (
        <div role="status" aria-live="polite" className="agent-working-indicator" style={{ color: theme.node.muted }}>
            <span className="agent-working-signal" aria-hidden="true">
                <span />
            </span>
            <span>{label}</span>
            <span className="agent-working-caption">实时处理中</span>
        </div>
    );
}

export function AgentPlanBar({ items, theme, minimized, onToggle, terminal = false }: { items: CloudAgentPlanItem[]; theme: (typeof canvasThemes)[keyof typeof canvasThemes]; minimized: boolean; onToggle: () => void; terminal?: boolean }) {
    const doneCount = items.filter((entry) => entry.status === "done").length;
    const allDone = doneCount === items.length;
    return (
        <div className="agent-plan-bar mx-3 mb-2 overflow-hidden rounded-xl" style={{ color: theme.node.text }}>
            <button type="button" className="flex w-full items-center gap-2 px-3 py-2 text-left focus-visible:outline focus-visible:outline-2" aria-expanded={!minimized} onClick={onToggle}>
                <ListChecks className="size-3.5 shrink-0" style={{ color: allDone ? "var(--palette-status-success)" : theme.node.muted }} />
                <span className="text-xs font-semibold">本轮待办</span>
                <span className="text-[11px] tabular-nums opacity-50">
                    {doneCount}/{items.length}
                </span>
                {terminal && !allDone ? <span className="shrink-0 text-[10px] opacity-55">本轮已结束，未完成项已停止</span> : null}
                <span className="min-w-0 flex-1" />
                <span className="shrink-0 text-[11px] opacity-50">{minimized ? "展开" : "收起"}</span>
                {minimized ? <ChevronDown className="size-3.5 shrink-0 opacity-50" /> : <ChevronUp className="size-3.5 shrink-0 opacity-50" />}
            </button>
            {minimized ? null : (
                <ul className="thin-scrollbar max-h-40 space-y-1 overflow-y-auto px-3 pb-2">
                    {items.map((entry) => {
                        const done = entry.status === "done";
                        const doing = entry.status === "doing";
                        const Icon = done ? CheckCircle2 : terminal ? CircleAlert : doing ? LoaderCircle : CircleDot;
                        return (
                            <li key={entry.id} className="flex min-w-0 items-start gap-1.5 text-xs">
                                <Icon className={doing && !terminal ? "mt-[3px] size-3 shrink-0 animate-spin" : "mt-[3px] size-3 shrink-0"} style={{ color: done ? "var(--palette-status-success)" : terminal ? theme.node.muted : doing ? theme.accent.primary : theme.node.muted }} />
                                <span className={done ? "min-w-0 break-words line-through opacity-50" : terminal ? "min-w-0 break-words opacity-55" : "min-w-0 break-words"}>{entry.title}</span>
                            </li>
                        );
                    })}
                </ul>
            )}
        </div>
    );
}

export function AgentQuestionBar({ question, theme, onAnswer, disabled = false }: { question: CloudAgentUserQuestion; theme: (typeof canvasThemes)[keyof typeof canvasThemes]; onAnswer: (label: string) => void; disabled?: boolean }) {
    return (
        <div className="agent-question-bar mx-3 mb-2 overflow-hidden rounded-xl" style={{ color: theme.node.text }}>
            <div className="flex items-start gap-2 px-3 pt-2.5">
                <HelpCircle className="mt-[1px] size-3.5 shrink-0" style={{ color: theme.accent.primary }} />
                <span className="min-w-0 flex-1 text-xs font-semibold leading-5">{question.question}</span>
            </div>
            <div className="flex flex-wrap gap-2 px-3 pb-2 pt-2">
                {question.options.map((option) => (
                    <button
                        key={option.label}
                        type="button"
                        disabled={disabled}
                        title={option.detail || option.label}
                        className="max-w-full rounded-md border-0 px-3 py-1.5 text-left text-xs transition focus-visible:outline focus-visible:outline-2 disabled:cursor-not-allowed disabled:opacity-50"
                        style={{ background: theme.toolbar.itemHover }}
                        onMouseDown={(event) => event.stopPropagation()}
                        onPointerDown={(event) => event.stopPropagation()}
                        onClick={(event) => {
                            event.stopPropagation();
                            onAnswer(option.label);
                        }}
                    >
                        <span className="block break-words font-medium">{option.label}</span>
                        {option.detail ? <span className="mt-0.5 block break-words text-[10px] leading-4 opacity-60">{option.detail}</span> : null}
                    </button>
                ))}
            </div>
            <div className="px-3 pb-2 text-[10px] opacity-50">{question.allowFreeform === false ? "请从上面选一项。" : "点一项即可，也可以在下方输入框里自己说明。"}</div>
        </div>
    );
}

/**
 * 场景起步胶囊：把「我大概想做 X」一步翻译成一组技能。
 * 两组来源，都不限剧典技能：
 *  1) 配方组——只读消费 GET /skills/presets（随二进制内置的手工策展配方）
 *  2) 常用组——用户已装（其中可含已收藏）及自建的技能，按常用度排序（含官方种子库与自定义）
 * 选择只作用于本会话；缺失的技能会持久安装到当前用户的技能库。
 * 挂上之后具体用哪张卡由 Agent 在任务里检索判断，胶囊只负责"把对的技能送到手边"。
 */
export type AgentSceneBucket = {
    key: string;
    label: string;
    presets: SkillPreset[];
    skills: Skill[];
};

/** 场景分类：与 presets.json 的 scene 字段、技能的 tag 字段共用同一套 key。 */
export const AGENT_SCENE_DEFS: Array<{ key: string; label: string; icon: typeof Sparkles }> = [
    { key: "frequent", label: "我的常用", icon: Bookmark },
    { key: "drama", label: "短剧故事", icon: Clapperboard },
    { key: "ecommerce", label: "广告电商", icon: ShoppingBag },
    { key: "creative", label: "视觉创意", icon: Palette },
    { key: "social", label: "传播社媒", icon: Share2 },
    { key: "others", label: "其他", icon: Shapes },
];

export function AgentSceneCapsules({
    buckets,
    installedIds,
    theme,
    disabled = false,
    onPick,
    onPickSkill,
}: {
    buckets: AgentSceneBucket[];
    installedIds: Set<string>;
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    disabled?: boolean;
    onPick: (preset: SkillPreset) => void;
    onPickSkill: (skill: Skill) => void;
}) {
    // 始终只占一排：默认显示场景分类，点某个场景后在同一排内就地切换内容。
    const [activeKey, setActiveKey] = useState<string | null>(null);
    const capsuleClass = "agent-scene-capsule shrink-0";
    const stop = {
        onMouseDown: (event: { stopPropagation(): void }) => event.stopPropagation(),
        onPointerDown: (event: { stopPropagation(): void }) => event.stopPropagation(),
    };
    const visible = buckets.filter((bucket) => bucket.presets.length + bucket.skills.length > 0);
    const active = activeKey ? visible.find((bucket) => bucket.key === activeKey) || null : null;
    if (!visible.length) return null;
    return (
        <div className="agent-scene-capsules mx-3 mb-2 min-w-0" style={{ color: theme.node.text }}>
            <div className="agent-scene-capsules-heading">
                <Sparkles aria-hidden="true" />
                <span>{active ? active.label : "技能组合推荐"}</span>
            </div>
            <div className="agent-scene-capsules-scroll thin-scrollbar flex gap-2 overflow-x-auto px-1 py-2">
                {active ? (
                    <>
                        <button
                            type="button"
                            disabled={disabled}
                            title="返回全部场景"
                            className={capsuleClass}
                            data-scene="back"
                            {...stop}
                            onClick={(event) => {
                                event.stopPropagation();
                                setActiveKey(null);
                            }}
                        >
                            <ArrowLeft aria-hidden="true" />
                            <span>全部场景</span>
                        </button>
                        {active.presets.map((preset) => {
                            const missing = preset.skillIds.filter((id) => !installedIds.has(id)).length;
                            return (
                                <button
                                    key={preset.presetId}
                                    type="button"
                                    disabled={disabled}
                                    title={preset.rationale}
                                    className={capsuleClass}
                                    data-scene={active.key}
                                    {...stop}
                                    onClick={(event) => {
                                        event.stopPropagation();
                                        onPick(preset);
                                    }}
                                >
                                    <Layers3 aria-hidden="true" />
                                    <span className="agent-scene-capsule-label">{preset.name}</span>
                                    <span className="agent-scene-capsule-meta">{missing > 0 ? `${preset.skillIds.length} 技能 · ${missing} 待装` : `${preset.skillIds.length} 技能`}</span>
                                </button>
                            );
                        })}
                        {active.skills.map((skill) => {
                            const owned = skill.isOwner ? "自建" : skill.isLike ? "已收藏" : installedIds.has(skill.skillId) ? "已装" : "待装";
                            return (
                                <button
                                    key={skill.skillId}
                                    type="button"
                                    disabled={disabled}
                                    title={skill.description}
                                    className={capsuleClass}
                                    data-scene={active.key}
                                    {...stop}
                                    onClick={(event) => {
                                        event.stopPropagation();
                                        onPickSkill(skill);
                                    }}
                                >
                                    <Sparkles aria-hidden="true" />
                                    <span className="agent-scene-capsule-label">{skill.skillName}</span>
                                    <span className="agent-scene-capsule-meta">{owned}</span>
                                </button>
                            );
                        })}
                    </>
                ) : (
                    visible.map((bucket) => {
                        const count = bucket.presets.length + bucket.skills.length;
                        const Icon = AGENT_SCENE_DEFS.find((definition) => definition.key === bucket.key)?.icon || Shapes;
                        return (
                            <button
                                key={bucket.key}
                                type="button"
                                disabled={disabled}
                                title={`查看「${bucket.label}」下的技能组合`}
                                className={capsuleClass}
                                data-scene={bucket.key}
                                {...stop}
                                onClick={(event) => {
                                    event.stopPropagation();
                                    setActiveKey(bucket.key);
                                }}
                            >
                                <Icon aria-hidden="true" />
                                <span className="agent-scene-capsule-label">{bucket.label}</span>
                                <span className="agent-scene-capsule-meta">{count} 项</span>
                            </button>
                        );
                    })
                )}
            </div>
            <p className="agent-scene-capsules-note">仅本会话生效 · 缺失技能将加入技能库 · Agent 按任务调用</p>
        </div>
    );
}

export type AgentInterjectionStatus = "sent" | "delivered" | "undelivered" | "withdrawn";
/** 与上游面板一致的插话状态标签；「已送达 / 已撤回」分开显示。 */
export const AGENT_INTERJECTION_LABELS: Record<AgentInterjectionStatus, string> = {
    sent: "插话 · 等待送达",
    delivered: "插话已送达",
    undelivered: "插话未送达",
    withdrawn: "插话已撤回",
};

export function AgentChatComposer({
    visible = true,
    prompt,
    attachments = [],
    disabled,
    sending,
    running,
    placeholder,
    theme,
    onPromptChange,
    onSubmit,
    onAddFiles,
    onRemoveAttachment,
    left,
    submitAccessory,
    onStop,
    stopping,
    references = [],
    slashSkills,
    includeAssetLibrary,
    sendMode,
    sendModeSaving,
    onSendModeSettings,
    queuedMessages,
}: {
    visible?: boolean;
    prompt: string;
    attachments?: CloudAgentChatAttachment[];
    disabled?: boolean;
    sending?: boolean;
    running?: boolean;
    placeholder: string;
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    onPromptChange: (value: string) => void;
    onSubmit: (oppositeMode?: boolean) => void;
    onStop?: () => void | Promise<void>;
    stopping?: boolean;
    onAddFiles?: (files: FileList | File[] | null) => void | Promise<void>;
    onRemoveAttachment?: (id: string) => void;
    left?: ReactNode;
    /** 发送按钮左侧的附属控件，例如上下文用量环。 */
    submitAccessory?: ReactNode;
    /** 供「@」插入的画布节点/素材/技能引用候选（可选，默认空，缺省时退化为普通输入框） */
    references?: CanvasResourceReference[];
    /** 供「/」弹出的技能候选（可选） */
    slashSkills?: Skill[];
    /** 是否在「@」候选里包含素材库资源 */
    includeAssetLibrary?: boolean;
    sendMode?: AgentSendMode;
    sendModeSaving?: boolean;
    onSendModeSettings?: () => void;
    queuedMessages?: ReactNode;
}) {
    const fileInputRef = useRef<HTMLInputElement>(null);
    const [slash, setSlash] = useState<{ start: number; query: string } | null>(null);
    const [slashIndex, setSlashIndex] = useState(0);
    const [previewAttachment, setPreviewAttachment] = useState<CloudAgentChatAttachment | null>(null);
    const [promptHeight, setPromptHeight] = useState(MIN_AGENT_PROMPT_HEIGHT);
    const promptResizeRef = useRef<{ startY: number; startHeight: number; element: HTMLButtonElement; pointerId: number } | null>(null);
    const manualPromptHeightRef = useRef<number | null>(null);
    const availableSlashSkills = slashSkills ?? [];
    const slashCandidates = useMemo(() => {
        const query = slash?.query.trim().toLocaleLowerCase() || "";
        if (!query) return availableSlashSkills;
        return availableSlashSkills.filter((skill) => `${skill.skillName} ${skill.description || ""}`.toLocaleLowerCase().includes(query));
    }, [availableSlashSkills, slash?.query]);
    const attachmentReferences = useMemo(() => agentAttachmentReferences(attachments), [attachments]);
    const skillReferences = useMemo(() => buildSkillMentionReferences(availableSlashSkills), [availableSlashSkills]);
    const composerReferences = useMemo(() => [...references, ...skillReferences, ...attachmentReferences].filter((reference, index, all) => all.findIndex((item) => item.id === reference.id) === index), [attachmentReferences, references, skillReferences]);
    const canStop = Boolean(running && onStop);
    const canSubmit = !disabled && !sending && Boolean(prompt.trim() || attachments.length);
    const reducedMotion = useReducedMotion();
    const activeSlashIndex = Math.min(Math.max(slashIndex, 0), Math.max(slashCandidates.length - 1, 0));

    const handlePromptContentSizeChange = useCallback((naturalHeight: number) => {
        const nextHeight = clampAgentPromptHeight(naturalHeight);
        // 用户开始拖动后，面板高度由用户掌控；超出部分交给内部滚动区，
        // 避免输入新内容时又把手动缩小的面板强行撑开。
        setPromptHeight((currentHeight) => (manualPromptHeightRef.current === null ? nextHeight : currentHeight));
    }, []);

    useEffect(() => {
        if (prompt.trim()) return;
        manualPromptHeightRef.current = null;
        setPromptHeight(MIN_AGENT_PROMPT_HEIGHT);
    }, [prompt]);

    const startPromptResize = (event: ReactPointerEvent<HTMLButtonElement>) => {
        if (!visible || disabled || event.button !== 0) return;
        event.preventDefault();
        manualPromptHeightRef.current = promptHeight;
        promptResizeRef.current = { startY: event.clientY, startHeight: promptHeight, element: event.currentTarget, pointerId: event.pointerId };
        event.currentTarget.setPointerCapture(event.pointerId);
    };

    const resizePrompt = (event: ReactPointerEvent<HTMLButtonElement>) => {
        const resize = promptResizeRef.current;
        if (!resize || resize.pointerId !== event.pointerId) return;
        setPromptHeight(clampAgentPromptHeight(resize.startHeight + resize.startY - event.clientY));
    };

    const finishPromptResize = (event?: ReactPointerEvent<HTMLButtonElement>) => {
        const resize = promptResizeRef.current;
        if (event && resize?.pointerId !== event.pointerId) return;
        promptResizeRef.current = null;
        if (resize?.element.hasPointerCapture(resize.pointerId)) resize.element.releasePointerCapture(resize.pointerId);
    };

    useLayoutEffect(() => {
        if (visible) return;
        finishPromptResize();
        setSlash(null);
        setPreviewAttachment(null);
    }, [visible]);

    const resizePromptByKeyboard = (event: ReactKeyboardEvent<HTMLButtonElement>) => {
        if (event.key !== "ArrowUp" && event.key !== "ArrowDown") return;
        event.preventDefault();
        manualPromptHeightRef.current = promptHeight;
        setPromptHeight(clampAgentPromptHeight(promptHeight + (event.key === "ArrowUp" ? 20 : -20)));
    };

    // 在输入值末尾检测「/ 或 、+ 关键词」打开技能候选：中文输入法下 "/" 会打成 "、"，两者等价，且都必须紧跟行首或空白，避免中文顿号误触发。选中后写入稳定 token，编辑器再把它渲染为技能 chip。
    const handlePromptChange = (value: string) => {
        onPromptChange(value);
        const match = /(^|\s)[/、]([^\s/、]*)$/.exec(value);
        if (match && availableSlashSkills.length) {
            const next = { start: match.index + match[1].length, query: match[2] };
            setSlash((current) => (current && current.start === next.start && current.query === next.query ? current : next));
            setSlashIndex(0);
        } else if (slash) {
            setSlash(null);
        }
    };

    const applySlashSkill = (skill: Skill) => {
        const token = `@[skill:${skill.skillId}] `;
        // 触发符 "/" 与 "、" 都是单字符，替换长度固定为 1。
        const next = slash ? `${prompt.slice(0, slash.start)}${token}${prompt.slice(slash.start + 1 + slash.query.length)}` : prompt ? `${prompt.replace(/\s+$/u, "")} ${token}` : token;
        setSlash(null);
        setSlashIndex(0);
        onPromptChange(next);
    };

    // slash 菜单的键盘控制在 capture 阶段拦截（contentEditable/textarea 内部先消费 Enter，外层冒泡拿不到）
    const handleSlashKeyCapture = (event: ReactKeyboardEvent) => {
        if (!slash || !slashCandidates.length) return;
        if (event.nativeEvent.isComposing || event.keyCode === 229) return;
        if (event.key === "ArrowDown") {
            event.preventDefault();
            event.stopPropagation();
            setSlashIndex((index) => Math.min(index + 1, slashCandidates.length - 1));
        } else if (event.key === "ArrowUp") {
            event.preventDefault();
            event.stopPropagation();
            setSlashIndex((index) => Math.max(index - 1, 0));
        } else if (event.key === "Enter" || event.key === "Tab") {
            event.preventDefault();
            event.stopPropagation();
            applySlashSkill(slashCandidates[activeSlashIndex]);
        } else if (event.key === "Escape") {
            event.preventDefault();
            event.stopPropagation();
            setSlash(null);
        }
    };

    // 保留粘贴图片成附件（contentEditable 模式内部会把粘贴转纯文本，capture 阶段先拦截图片）
    const handlePasteCapture = (event: ReactClipboardEvent) => {
        if (!onAddFiles) return;
        const images = Array.from(event.clipboardData.files).filter((file) => file.type.startsWith("image/"));
        if (!images.length) return;
        event.preventDefault();
        event.stopPropagation();
        void onAddFiles(images);
    };

    const insertAttachmentMention = (item: CloudAgentChatAttachment) => {
        const token = `@[attachment:${item.id}] `;
        if (prompt.includes(`@[attachment:${item.id}]`)) return;
        onPromptChange(prompt ? `${prompt.replace(/\s+$/u, "")} ${token}` : token);
    };

    return (
        <div className="agent-composer-wrap min-w-0 shrink-0" onWheelCapture={(event) => event.stopPropagation()}>
            {/* 上游输入区外壳：有待发消息时外壳托住队列与输入卡片，形成一张整卡。 */}
            <div className="agent-composer-shell relative">
                {queuedMessages}
                <div
                    className="agent-composer-surface group/composer relative transition-[background-color,box-shadow] duration-200"
                    style={{
                        color: theme.accent.primary,
                    }}
                >
                    {sending && !reducedMotion ? <WorkingGlow active color={theme.accent.primary} radius={22} /> : null}
                    {attachments.length ? (
                        <div className="thin-scrollbar mb-2 flex gap-2 overflow-x-auto pb-1">
                            {attachments.map((item, index) => (
                                <div key={item.id} className="group relative w-20 shrink-0">
                                    <button
                                        type="button"
                                        className="relative block size-20 overflow-hidden rounded-lg"
                                        title="点击放大预览"
                                        aria-label={`预览 ${item.name || `图片${index + 1}`}`}
                                        onClick={() => setPreviewAttachment(item)}
                                        onDoubleClick={() => setPreviewAttachment(item)}
                                    >
                                        <img src={item.url} alt={item.name} className="size-full object-cover" />
                                    </button>
                                    <div className="mt-1 flex min-w-0 items-center justify-between gap-1">
                                        <button type="button" className="flex min-w-0 items-center gap-0.5 truncate text-[var(--fs-tiny)] opacity-80 hover:opacity-100" title={`插入 @图片${index + 1}`} onClick={() => insertAttachmentMention(item)}>
                                            <AtSign className="size-2.5 shrink-0" />
                                            <span className="truncate">图片{index + 1}</span>
                                        </button>
                                        {onRemoveAttachment ? (
                                            <button
                                                type="button"
                                                className="grid size-4 shrink-0 place-items-center rounded-full opacity-70 hover:opacity-100"
                                                style={{ background: theme.toolbar.panel, color: theme.node.text }}
                                                onClick={() => onRemoveAttachment(item.id)}
                                                aria-label="移除图片"
                                            >
                                                <X className="size-3" />
                                            </button>
                                        ) : null}
                                    </div>
                                </div>
                            ))}
                        </div>
                    ) : null}
                    <div className="relative" onKeyDownCapture={handleSlashKeyCapture} onPasteCapture={handlePasteCapture}>
                        <button
                            type="button"
                            role="separator"
                            aria-orientation="horizontal"
                            aria-label="调整提示词面板高度"
                            aria-valuemin={MIN_AGENT_PROMPT_HEIGHT}
                            aria-valuemax={MAX_AGENT_PROMPT_HEIGHT}
                            aria-valuenow={promptHeight}
                            className="agent-composer-resize-handle"
                            style={{ color: theme.node.muted }}
                            onPointerDown={startPromptResize}
                            onPointerMove={resizePrompt}
                            onPointerUp={finishPromptResize}
                            onPointerCancel={finishPromptResize}
                            onLostPointerCapture={finishPromptResize}
                            onKeyDown={resizePromptByKeyboard}
                        >
                            <span />
                        </button>
                        <div className="agent-composer-prompt-scroll" style={{ height: promptHeight }}>
                            <CanvasResourceMentionTextarea
                                visible={visible}
                                value={prompt}
                                references={composerReferences}
                                includeAssetLibrary={includeAssetLibrary}
                                sendOnEnter={canSubmit ? "both" : false}
                                disabled={disabled}
                                onChange={handlePromptChange}
                                onSubmit={({ ctrlKey, metaKey }) => {
                                    if (canSubmit) onSubmit(ctrlKey || metaKey);
                                }}
                                onContentSizeChange={handlePromptContentSizeChange}
                                className="w-full resize-none border-0 bg-transparent px-1 py-1 text-sm leading-5 outline-none placeholder:opacity-45"
                                containerClassName="min-h-[60px] h-full"
                                style={{ color: theme.node.text }}
                                placeholder={placeholder}
                                aria-label="Agent 输入"
                            />
                        </div>
                        {visible && slash && slashCandidates.length ? (
                            <div
                                data-agent-slash-menu
                                className="absolute bottom-full left-0 z-[var(--z-toolbar)] mb-2 w-full max-w-xs overflow-hidden rounded-2xl p-1.5 shadow-2xl"
                                style={{ background: theme.toolbar.panel, boxShadow: `0 18px 44px ${theme.spatial.shadow}` }}
                                onMouseDown={(event) => event.preventDefault()}
                            >
                                {slashCandidates.map((skill, index) => (
                                    <button
                                        key={skill.skillId}
                                        type="button"
                                        className="flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs"
                                        style={{ background: index === activeSlashIndex ? theme.toolbar.itemHover : "transparent", color: theme.node.text }}
                                        onMouseEnter={() => setSlashIndex(index)}
                                        onClick={() => applySlashSkill(skill)}
                                    >
                                        <Sparkles className="size-3.5 shrink-0 opacity-70" />
                                        <span className="min-w-0 truncate font-medium">{skill.skillName}</span>
                                        {skill.description ? <span className="min-w-0 flex-1 truncate opacity-50">{skill.description}</span> : null}
                                    </button>
                                ))}
                            </div>
                        ) : null}
                    </div>
                    <div className="agent-composer-toolbar mt-2">
                        <div className="agent-composer-controls flex min-w-0 items-center gap-1">
                            {onAddFiles ? (
                                <>
                                    <input
                                        ref={fileInputRef}
                                        hidden
                                        type="file"
                                        accept="image/*"
                                        multiple
                                        onChange={(event) => {
                                            void onAddFiles(event.target.files);
                                            event.target.value = "";
                                        }}
                                    />
                                    <Tooltip title="上传图片">
                                        <Button
                                            type="text"
                                            shape="circle"
                                            className="!h-8 !w-8 !min-w-8 !transition-transform hover:!scale-105 active:!scale-95"
                                            disabled={sending}
                                            style={{ color: theme.node.muted }}
                                            icon={<ImagePlus className="size-4" />}
                                            onClick={() => fileInputRef.current?.click()}
                                        />
                                    </Tooltip>
                                </>
                            ) : null}
                            {left}
                        </div>
                        <div className="agent-composer-submit flex items-center gap-2">
                            {onSendModeSettings ? <AgentSendModeButton value={sendMode} saving={sendModeSaving} onOpenSettings={onSendModeSettings} /> : null}
                            {submitAccessory}
                            {canStop ? (
                                <motion.button
                                    type="button"
                                    disabled={stopping}
                                    aria-label="停止本轮"
                                    title="停止当前 Agent 运行"
                                    onClick={() => void onStop?.()}
                                    whileHover={!reducedMotion && !stopping ? { scale: 1.06, y: -1 } : undefined}
                                    whileTap={!reducedMotion && !stopping ? { scale: 0.9, y: 1 } : undefined}
                                    animate={stopping && !reducedMotion ? { scale: [1, 0.94, 1] } : { scale: 1 }}
                                    transition={{ type: "spring", stiffness: 420, damping: 24 }}
                                    className="grid size-7 shrink-0 place-items-center rounded-full p-0 outline-none transition-[background-color,box-shadow,color,transform] duration-200 focus-visible:ring-2 focus-visible:ring-current/35 disabled:cursor-not-allowed"
                                    style={{ background: theme.accent.danger, color: theme.accent.onPrimary }}
                                >
                                    {stopping ? <LoaderCircle className="size-4 animate-spin" /> : <Square className="size-3.5" fill="currentColor" />}
                                </motion.button>
                            ) : null}
                            <motion.button
                                type="button"
                                disabled={!canSubmit}
                                aria-label={sending ? "发送中" : canStop ? "插话" : "发送"}
                                title={canStop ? "插话：Agent 下一次开口时看到它" : "点击发送；Enter 或 ⌘/Ctrl+Enter 发送"}
                                onClick={() => onSubmit()}
                                whileHover={canSubmit && !reducedMotion ? { scale: 1.06, y: -1 } : undefined}
                                whileTap={canSubmit && !reducedMotion ? { scale: 0.9, y: 1 } : undefined}
                                animate={stopping && !reducedMotion ? { scale: [1, 0.94, 1] } : { scale: 1, rotate: 0 }}
                                transition={sending && !reducedMotion ? { duration: 0.42, ease: "easeOut" } : { type: "spring", stiffness: 420, damping: 24 }}
                                data-icon-only
                                className="agent-composer-send grid size-7 shrink-0 place-items-center rounded-full p-0 outline-none transition-[background-color,box-shadow,color,transform] duration-200 focus-visible:ring-2 focus-visible:ring-current/35 disabled:cursor-not-allowed"
                                style={{
                                    background: canSubmit || sending ? theme.accent.primary : theme.spatial.surface,
                                    color: canSubmit || sending ? theme.accent.onPrimary : theme.node.muted,
                                }}
                            >
                                <motion.span
                                    key={stopping ? "stopping" : sending ? "sending" : "ready"}
                                    initial={reducedMotion ? false : { opacity: 0, scale: 0.65, rotate: sending ? -25 : 25 }}
                                    animate={{ opacity: 1, scale: 1, rotate: 0 }}
                                    transition={{ duration: reducedMotion ? 0 : 0.18, ease: "easeOut" }}
                                    className="grid place-items-center"
                                >
                                    {sending ? <LoaderCircle className="size-3.5 animate-spin" /> : <ArrowUp className="size-3.5" />}
                                </motion.span>
                            </motion.button>
                        </div>
                    </div>
                </div>
            </div>
            {visible && previewAttachment ? <AgentImagePreview attachment={previewAttachment} onClose={() => setPreviewAttachment(null)} /> : null}
        </div>
    );
}

function AgentContextCompactionNotice({ item, theme }: { item: CloudAgentChatMessage; theme: (typeof canvasThemes)[keyof typeof canvasThemes] }) {
    const running = objectField(item.detail, "status") === "running";
    const fallback = objectField(item.detail, "mode") === "fallback";
    return (
        <div role="status" aria-live="polite" className="flex items-start gap-3">
            <AgentTimelineMarker theme={theme} tone={running ? "agent" : "muted"} icon={running ? <LoaderCircle className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />} />
            <div className="min-w-0 flex-1 py-0.5">
                <div className="flex flex-wrap items-center gap-2 text-[13px] font-medium" style={{ color: theme.node.text }}>
                    <span>{running ? "正在整理上下文" : "上下文整理完成"}</span>
                    <span className="rounded-full px-2 py-0.5 text-[var(--fs-label)]" style={{ color: running ? theme.accent.primary : theme.node.muted, background: running ? theme.accent.primarySoft : theme.node.fill }}>
                        {running ? "压缩中" : fallback ? "保底检查点" : "结构化检查点"}
                    </span>
                </div>
                <div className="mt-0.5 text-xs leading-5" style={{ color: theme.node.muted }}>
                    {item.text}
                </div>
                {item.meta ? (
                    <div className="mt-0.5 text-[var(--fs-label)] opacity-65" style={{ color: theme.node.muted }}>
                        {item.meta}
                    </div>
                ) : null}
            </div>
        </div>
    );
}

export function AgentPanelTabs<T extends string>({
    value,
    items,
    theme,
    right,
    onChange,
}: {
    value: T;
    items: { value: T; label: string; icon?: ReactNode; count?: number }[];
    theme: (typeof canvasThemes)[keyof typeof canvasThemes];
    right?: ReactNode;
    onChange: (value: T) => void;
}) {
    return (
        <div className="shrink-0 px-3 pb-1">
            <div className="flex min-h-8 items-center justify-between gap-2 rounded-lg px-0.5 py-0.5" style={{ background: "transparent" }}>
                <nav className="grid min-w-0 flex-1 grid-flow-col auto-cols-fr items-center gap-0.5 text-[var(--fs-label)]" role="tablist" aria-label="Agent 面板">
                    {items.map((item) => (
                        <button
                            key={item.value}
                            type="button"
                            role="tab"
                            aria-selected={value === item.value}
                            className={`inline-flex h-7 min-w-0 items-center justify-center gap-1 rounded-md px-1.5 transition-colors ${value === item.value ? "font-medium" : "font-normal"}`}
                            style={{ background: value === item.value ? theme.node.fill : "transparent", color: value === item.value ? theme.node.text : theme.node.muted, boxShadow: value === item.value ? `0 2px 8px ${theme.spatial.shadow}` : "none" }}
                            onClick={() => onChange(item.value)}
                        >
                            <span className="shrink-0">{item.icon}</span>
                            <span className="min-w-0 truncate">{item.label}</span>
                            {item.count ? <span className="shrink-0 tabular-nums opacity-60">{item.count}</span> : null}
                        </button>
                    ))}
                </nav>
                {right ? <div className="flex shrink-0 items-center gap-1">{right}</div> : null}
            </div>
        </div>
    );
}

/**
 * 时间线标记：图标由调用方给（系统行给 Sparkles、错误与审批给 CircleAlert…），不再有默认
 * 的模型品牌 glyph —— 那个圆环标志会被读成"这条是模型/OpenAI 出品"，与它实际表达的
 * "这是谁的一行"无关。
 */
function AgentTimelineMarker({ theme, tone, icon }: { theme: (typeof canvasThemes)[keyof typeof canvasThemes]; tone: "agent" | "muted" | "approval" | "error"; icon: ReactNode }) {
    const color = tone === "error" ? "#ef4444" : tone === "approval" ? "var(--palette-status-warning)" : tone === "agent" ? theme.accent.primary : theme.node.muted;
    return (
        <span className="agent-timeline-marker relative" aria-hidden="true">
            <span className="relative grid size-5 place-items-center rounded-full" style={{ background: tone === "agent" ? theme.accent.primarySoft : theme.node.fill, color }}>
                {icon}
            </span>
        </span>
    );
}

function AgentMessageAttachments({ attachments }: { attachments: CloudAgentChatAttachment[] }) {
    const [preview, setPreview] = useState<CloudAgentChatAttachment | null>(null);
    return (
        <>
            <div className="mt-2 grid grid-cols-3 gap-1.5">
                {attachments.map((item) => (
                    <button key={item.id} type="button" className="group relative overflow-hidden rounded-lg" onClick={() => setPreview(item)} onDoubleClick={() => setPreview(item)} aria-label={`查看图片 ${item.name}`}>
                        <img src={item.url} alt={item.name} className="aspect-square w-full object-cover transition-transform group-hover:scale-105" />
                    </button>
                ))}
            </div>
            {preview ? <AgentImagePreview attachment={preview} onClose={() => setPreview(null)} /> : null}
        </>
    );
}

export function AgentImagePreview({ attachment, onClose }: { attachment: CloudAgentChatAttachment; onClose: () => void }) {
    return (
        <div className="fixed inset-0 z-[var(--z-dialog-popover)] grid place-items-center bg-black/80 p-6" role="dialog" aria-label={attachment.name} onClick={onClose}>
            <img src={attachment.url} alt={attachment.name} className="max-h-[90vh] max-w-[92vw] rounded-xl object-contain shadow-2xl" onClick={(event) => event.stopPropagation()} />
            <button type="button" className="absolute right-5 top-5 rounded-full bg-black/60 p-2 text-white" onClick={onClose} aria-label="关闭图片预览">
                <X className="size-5" />
            </button>
        </div>
    );
}

function agentAttachmentReferences(attachments: CloudAgentChatAttachment[]): CanvasResourceReference[] {
    return attachments.map((item, index) => ({
        id: `attachment:${item.id}`,
        nodeId: "",
        kind: "image",
        label: `图片${index + 1}`,
        title: item.name || `图片${index + 1}`,
        previewUrl: item.url,
        active: true,
        mentionToken: `@[attachment:${item.id}]`,
    }));
}

function toolCardState(title: string, text: string, detail?: unknown) {
    const status = agentToolStatus(title, text, detail);
    // 失败时优先显示稳定归类（"参数不符合契约"/"画布状态已变化"/"模型输出问题"…）：
    // 它比"执行失败"更能说明下一步该做什么（handoff 工作项 B 的分类）。
    const errorClassLabel = agentToolErrorClassLabel(detail);
    if (status === "completed") return { label: "已完成", color: "var(--palette-status-success)", softBg: "color-mix(in srgb, var(--palette-status-success) 4%, transparent)", icon: <CheckCircle2 className="size-4" />, isError: false };
    if (status === "failed") return { label: errorClassLabel ?? "执行失败", color: "var(--palette-status-error)", softBg: "color-mix(in srgb, var(--palette-status-error) 4%, transparent)", icon: <XCircle className="size-4" />, isError: true };
    if (status === "noop") return { label: "未生效", color: "var(--palette-status-warning)", softBg: "color-mix(in srgb, var(--palette-status-warning) 4%, transparent)", icon: <CircleAlert className="size-4" />, isError: false };
    if (status === "rejected") return { label: errorClassLabel ?? "拒绝执行", color: "var(--palette-status-error)", softBg: "color-mix(in srgb, var(--palette-status-error) 4%, transparent)", icon: <XCircle className="size-4" />, isError: true };
    return { label: "处理中", color: "var(--muted-foreground)", softBg: "color-mix(in srgb, var(--muted-foreground) 4%, transparent)", icon: <CircleDot className="size-4" />, isError: false };
}

function objectField(value: unknown, key: string) {
    return value && typeof value === "object" ? (value as Record<string, unknown>)[key] : undefined;
}
