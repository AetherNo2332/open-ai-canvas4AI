import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { Alert, App, Button, Dropdown, Input, InputNumber, Popover, Select, Tag } from "antd";
import { Archive, ChevronDown, ChevronRight, CircleStop, History, LoaderCircle, MessageSquarePlus, Paperclip, RotateCcw, Send, Settings2, Sparkles, X } from "lucide-react";
import { AIMessageMarkdown } from "@/components/ai/ai-message-markdown";
import { ModelPicker } from "@/components/model-picker";
import { ThinkingLevelSlider } from "@/components/thinking-level-slider";
import { FluidOrb } from "@/components/ui/fluid-orb";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { pendingAgentQuestion } from "@/lib/agent/conversation-events";
import { emptyAgentContextUsage, presentAgentContextUsage, type AgentContextUsage } from "@/lib/agent/context-usage";
import { createClientUUID } from "@/lib/client-id";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { resolveThinkingLevel, thinkingLevelsFor, type ThinkingLevel, type ThinkingRequest } from "@/lib/text-thinking";
import { getActiveUserScope } from "@/lib/user-scope";
import { buildSkillMentionReferences, resolveSkillMentions } from "@/services/skill-runtime";
import { addSkill, listAddedSkills, listSkills, type Skill, type SkillCategory } from "@/services/api/skills";
import { agentPanelControlEnabled, type AgentPanelConfig } from "@/services/agent/panel-config";
import type { AgentPanelAdapter, AgentPanelBindings, AgentPanelPrefill } from "@/services/agent/panel-adapters";
import type { AgentConversationMessage, AgentConversationRecord } from "@/services/agent/conversation-types";
import type { AgentPageContext } from "@/services/agent/business-context";
import type { AgentApprovalDecision, AgentCapabilities, AgentPermissionMode, AgentRunSnapshot } from "@/sdk/agent";
import type { createAgentClient } from "@/sdk/agent/client";
import { logicalModelIDForConfig, modelOptionName, resolveModelRequestConfig, selectableModelsByCapability, useEffectiveConfig } from "@/stores/use-config-store";
import { useAgentTheme } from "@/lib/agent/use-agent-theme";
import { useAgentConversation } from "./use-agent-conversation";
import { useAgentPanelLayout } from "./use-agent-panel-layout";
import { AGENT_ATTACHMENT_MAX, appendAgentNodeReferences, checkAgentAttachmentFiles, removeAgentNodeReference, type AgentAttachmentState } from "@/lib/agent/agent-attachments";
import type { CloudAgentChatAttachment } from "./agent-attachments";
import { AgentContextRing, AgentApprovalCard, AgentComposerControls } from "./agent-panel-controls";
import { AgentHistoryView, AgentLauncher, AgentPanelHeader } from "./agent-panel-chrome";
import { AgentPanelSettings } from "./agent-panel-settings";
import { AgentImageApprovalSettings } from "./agent-image-approval-settings";
import { AgentMessageQueueList } from "./agent-message-queue";
import { CanvasAgentSkillLibraryModal as AgentSkillLibraryModal } from "./agent-skill-library-modal";
import { AgentChatComposer, AgentMessageList, AgentPlanBar, AgentQuestionBar, AgentRunUndoAction, AgentWorkingMessage, type CloudAgentChatMessage } from "./agent-chat-ui";
import { AgentMediaBatchBar } from "./agent-media-batch-bar";
import { AgentWelcome } from "./agent-welcome";
import { DEFAULT_CANVAS_APPEARANCE } from "@/lib/agent/agent-appearance";
import { latestAgentMediaBatch } from "@/lib/agent/cloud-agent-media-batch";
import { agentRunUndoOffer } from "@/lib/agent/agent-run-undo";
import { agentModelFilter } from "@/lib/agent/model-requirements";
import { agentPanelDefaultModel, agentPanelModelRoute } from "@/services/agent/panel-model-selection";
import { agentImageApproval } from "@/lib/agent/agent-media-approval";
import { useAppearanceStore } from "@/stores/use-appearance-store";
import "./agent-panel.css";

type ScopedClient = ReturnType<typeof createAgentClient>;
type PanelView = "chat" | "history" | "settings";

const EMPTY_BINDINGS: AgentPanelBindings = {};
const EMPTY_REFERENCES: NonNullable<AgentPanelBindings["references"]> = [];
const TERMINAL = new Set<AgentRunSnapshot["status"]>(["completed", "failed", "cancelled", "rejected"]);

function useAgentPanelBindings(adapter: AgentPanelAdapter) {
    return adapter.useBindings ? adapter.useBindings() : EMPTY_BINDINGS;
}

function statusLabel(run: AgentRunSnapshot | null) {
    if (!run) return "待命";
    if (run.contextCompaction) return "压缩上下文";
    if (run.status === "waiting_approval") return "等待审批";
    if (run.status === "queued") return run.waitReason || "排队中";
    if (run.status === "running") {
        if (run.runtimePhase === "waiting_model") return "等待模型响应";
        if (run.runtimePhase === "waiting_tool") return "等待工具结果";
        if (run.runtimePhase === "waiting_resource") return run.waitReason || "等待运行容量";
        return "运行中";
    }
    if (run.status === "completed") return "已完成";
    if (run.status === "failed") return "异常";
    if (run.status === "cancelled") return "已停止";
    if (run.status === "rejected") return "已拒绝";
    return "待命";
}

function runRunning(run: AgentRunSnapshot | null | undefined) {
    return Boolean(run && (run.cleanupPending || ["queued", "running", "waiting_approval"].includes(run.status)));
}

function ContextRing({ usage }: { usage: AgentContextUsage }) {
    const view = presentAgentContextUsage(usage);
    const background = `conic-gradient(currentColor ${Math.round(view.ring * 360)}deg, color-mix(in srgb, currentColor 16%, transparent) 0deg)`;
    return (
        <Popover
            placement="topRight"
            content={
                <div className="max-w-72 text-xs leading-5">
                    <strong>{view.label}</strong>
                    <p className="mt-1 text-neutral-600">{view.detail}</p>
                    {view.breakdown.length ? (
                        <ul className="mt-2 space-y-1 border-t border-black/10 pt-2">
                            {view.breakdown.map((item) => (
                                <li key={item.key} className="flex justify-between gap-4">
                                    <span>{item.label}</span>
                                    <span>{item.tokens.toLocaleString("zh-CN")}</span>
                                </li>
                            ))}
                        </ul>
                    ) : null}
                </div>
            }
        >
            <button type="button" className="grid size-7 shrink-0 place-items-center rounded-full" aria-label={`上下文用量：${view.label}`} title={view.detail} data-agent-context-ring>
                <span className="size-4 rounded-full p-[2px]" style={{ background }} aria-hidden="true">
                    <span className="block size-full rounded-full bg-white" />
                </span>
            </button>
        </Popover>
    );
}

function ReasoningGroup({ items }: { items: AgentConversationMessage[] }) {
    const [open, setOpen] = useState(false);
    return (
        <div className="rounded-xl border border-black/10 bg-black/[0.025] px-3 py-2">
            <button type="button" className="flex w-full items-center gap-2 text-left text-xs text-neutral-600" aria-expanded={open} onClick={() => setOpen((value) => !value)}>
                {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                <Sparkles className="size-3.5" />
                <span>{items.some((item) => item.streaming) ? "正在思考" : "思考过程"}</span>
                {!open ? <span className="min-w-0 flex-1 truncate opacity-60">{items.at(-1)?.text.slice(0, 120)}</span> : null}
            </button>
            {open ? (
                <div className="mt-2 space-y-2 border-t border-black/10 pt-2 text-xs leading-5 text-neutral-600">
                    {items.map((item) => (
                        <p key={item.id} className="whitespace-pre-wrap break-words">
                            {item.text}
                        </p>
                    ))}
                </div>
            ) : null}
        </div>
    );
}

function ToolMessage({ item }: { item: AgentConversationMessage }) {
    const [open, setOpen] = useState(false);
    const detail = item.detail && typeof item.detail === "object" ? JSON.stringify(item.detail, null, 2) : "";
    return (
        <details className="rounded-xl border border-black/10 bg-black/[0.018] px-3 py-2 text-xs" open={open} onToggle={(event) => setOpen(event.currentTarget.open)}>
            <summary className="flex cursor-pointer list-none items-center gap-2 text-neutral-600">
                <ChevronRight className={`size-3.5 transition-transform ${open ? "rotate-90" : ""}`} />
                <span className="font-medium text-neutral-800">{item.title || "工具执行"}</span>
                <span className="min-w-0 flex-1 truncate opacity-70">{item.text}</span>
            </summary>
            {item.text ? <p className="mt-2 whitespace-pre-wrap break-words text-neutral-600">{item.text}</p> : null}
            {detail ? <pre className="mt-2 max-h-56 overflow-auto rounded-lg bg-black/[0.04] p-2 text-[11px]">{detail}</pre> : null}
        </details>
    );
}

function MessageList({ messages, busy }: { messages: AgentConversationMessage[]; busy: boolean }) {
    const blocks: ReactNode[] = [];
    let reasoning: AgentConversationMessage[] = [];
    const flushReasoning = () => {
        if (reasoning.length) blocks.push(<ReasoningGroup key={`reasoning-${reasoning[0].id}`} items={reasoning} />);
        reasoning = [];
    };
    for (const item of messages) {
        if (item.reasoning) {
            reasoning.push(item);
            continue;
        }
        flushReasoning();
        if (item.role === "tool") {
            if (!item.planItems?.length) blocks.push(<ToolMessage key={item.id} item={item} />);
            continue;
        }
        if (item.role === "user") {
            blocks.push(
                <div key={item.id} className="ml-auto max-w-[88%] rounded-2xl bg-neutral-900 px-3.5 py-2.5 text-sm leading-6 text-white">
                    <p className="whitespace-pre-wrap break-words">{item.text}</p>
                    {item.interjection ? <span className="mt-1 block text-[10px] opacity-65">{item.interjection === "delivered" ? "已送达" : item.interjection === "sent" ? "插话已发送" : "插话状态未确认"}</span> : null}
                </div>,
            );
            continue;
        }
        if (item.role === "error") {
            blocks.push(<Alert key={item.id} type="error" showIcon message="Agent 暂时无法继续" description={item.text} />);
            continue;
        }
        if (item.question) {
            continue;
        }
        if (item.role === "assistant") {
            blocks.push(
                <div key={item.id} className="agent-panel-message min-w-0 text-sm leading-6">
                    {item.streaming ? <span className="mr-1 inline-block size-1.5 animate-pulse rounded-full bg-current align-middle" /> : null}
                    <AIMessageMarkdown streamingAnimation={item.streaming && busy ? "word" : "none"}>{item.text}</AIMessageMarkdown>
                </div>,
            );
            continue;
        }
        blocks.push(
            <div key={item.id} className={`agent-panel-message text-xs leading-5 ${item.role === "system" ? "text-neutral-500" : "text-neutral-700"}`}>
                <span className="whitespace-pre-wrap">{item.text}</span>
                {item.meta ? <span className="ml-2 opacity-60">{item.meta}</span> : null}
            </div>,
        );
    }
    flushReasoning();
    return <div className="space-y-3">{blocks}</div>;
}

function PlanBar({ messages }: { messages: AgentConversationMessage[] }) {
    const plan = [...messages].reverse().find((message) => message.planItems?.length)?.planItems;
    if (!plan?.length) return null;
    return (
        <div className="rounded-xl border border-black/10 bg-white/80 px-3 py-2">
            <div className="flex items-center gap-2 text-xs font-medium">
                <Sparkles className="size-3.5" />
                执行计划
            </div>
            <ol className="mt-1.5 space-y-1 text-xs text-neutral-600">
                {plan.map((item) => (
                    <li key={item.id} className="flex gap-2">
                        <span className="w-4 shrink-0">{item.status === "done" ? "✓" : item.status === "doing" ? "•" : "○"}</span>
                        <span className={item.status === "done" ? "opacity-60 line-through" : ""}>{item.title}</span>
                    </li>
                ))}
            </ol>
        </div>
    );
}

function QueuePanel({
    queue,
    busy,
    onEdit,
    onPromote,
    onRemove,
    onResume,
    onPause,
}: {
    queue: ReturnType<typeof useAgentConversation>["state"]["queue"];
    busy: boolean;
    onEdit: (item: NonNullable<typeof queue>["items"][number], prompt: string) => Promise<boolean>;
    onPromote: (item: NonNullable<typeof queue>["items"][number]) => Promise<boolean>;
    onRemove: (item: NonNullable<typeof queue>["items"][number]) => Promise<boolean>;
    onResume: () => Promise<boolean>;
    onPause: () => Promise<boolean>;
}) {
    const [editing, setEditing] = useState<{ id: string; value: string } | null>(null);
    if (!queue?.items.length && !queue?.paused) return null;
    return (
        <div className="rounded-xl border border-black/10 bg-white/75 px-3 py-2 text-xs">
            <div className="flex items-center justify-between gap-2">
                <strong>待发消息 {queue.items.length}</strong>
                <div className="flex items-center gap-2 text-neutral-500">
                    {queue.awaitingQuestionId ? <span>等待回答</span> : null}
                    {queue.paused ? (
                        <Button size="small" type="text" disabled={busy} onClick={() => void onResume()}>
                            继续
                        </Button>
                    ) : (
                        <Button size="small" type="text" disabled={busy} onClick={() => void onPause()}>
                            暂停
                        </Button>
                    )}
                </div>
            </div>
            {queue.pauseReason ? <p className="mt-1 text-neutral-500">{queue.pauseReason}</p> : null}
            <div className="mt-2 space-y-1.5">
                {queue.items.map((item) => (
                    <div key={item.id} className="rounded-lg bg-black/[0.035] px-2 py-1.5">
                        {editing?.id === item.id ? (
                            <div className="flex gap-1">
                                <Input size="small" value={editing.value} onChange={(event) => setEditing({ ...editing, value: event.target.value })} />
                                <Button size="small" type="text" disabled={busy || !editing.value.trim()} onClick={() => void onEdit(item, editing.value).then((ok) => ok && setEditing(null))}>
                                    保存
                                </Button>
                            </div>
                        ) : (
                            <p className="line-clamp-2 whitespace-pre-wrap break-words">{item.prompt}</p>
                        )}
                        <div className="mt-1 flex gap-1">
                            <Button size="small" type="text" disabled={busy} onClick={() => setEditing({ id: item.id, value: item.prompt })}>
                                编辑
                            </Button>
                            <Button size="small" type="text" disabled={busy} onClick={() => void onPromote(item)}>
                                插话
                            </Button>
                            <Button size="small" type="text" danger disabled={busy} onClick={() => void onRemove(item)}>
                                移除
                            </Button>
                        </div>
                    </div>
                ))}
            </div>
        </div>
    );
}

function LegacyAgentPanel({
    context,
    config,
    adapter,
    client,
    capabilities,
    open,
    onOpen,
    onClose,
    initialRunId,
}: {
    context: AgentPageContext;
    config: AgentPanelConfig;
    adapter: AgentPanelAdapter;
    client: ScopedClient;
    capabilities: AgentCapabilities;
    open: boolean;
    onOpen: () => void;
    onClose: () => void;
    initialRunId?: string;
}) {
    const { message } = App.useApp();
    const bindings = useAgentPanelBindings(adapter);
    const bindingsRef = useRef(bindings);
    bindingsRef.current = bindings;
    const userScope = getActiveUserScope();
    const configStore = useEffectiveConfig();
    const appearance = useAppearanceStore((state) => state.appearance.canvas);
    const [view, setView] = useState<PanelView>("chat");
    const [draft, setDraft] = useState("");
    const [sending, setSending] = useState(false);
    const [stopping, setStopping] = useState(false);
    const [skills, setSkills] = useState<Skill[]>([]);
    const [skillsLoading, setSkillsLoading] = useState(false);
    const [slash, setSlash] = useState<{ start: number; query: string } | null>(null);
    const [episodeId, setEpisodeId] = useState(context.target?.dramaEpisodeId || "");
    const [maxCredits, setMaxCredits] = useState(20);
    const [permissionMode, setPermissionMode] = useState<AgentPermissionMode>(capabilities.permissionModes.includes("auto") ? "auto" : capabilities.permissionModes[0] || "read_only");
    const [approvalReason, setApprovalReason] = useState("");
    const [approvalMediaSettings, setApprovalMediaSettings] = useState<NonNullable<AgentApprovalDecision["mediaSettings"]>>();
    const [approvalBusy, setApprovalBusy] = useState(false);
    const prepareRef = useRef<() => Promise<void>>(async () => {});
    prepareRef.current = async () => {
        await bindingsRef.current.prepareSubmission?.();
    };
    const { controller, state } = useAgentConversation({
        userScope,
        scope: context.scope || null,
        agentDefinitionId: context.agentDefinitionId || config.agentDefinitionId,
        client: context.scope ? client : null,
        prepare: useCallback(() => prepareRef.current(), []),
    });
    const running = runRunning(state.run);
    const activeRecord = state.conversations.find((item) => item.id === state.activeConversationId) || null;
    const textModels = useMemo(() => {
        const models = selectableModelsByCapability(configStore, "text");
        const requirements = capabilities.modelRequirements;
        if (!requirements) return models;
        return models.filter((model) => {
            let text: ReturnType<typeof modelCapabilityConfigFor>["text"];
            try {
                text = modelCapabilityConfigFor(configStore, model).text;
            } catch {
                return true;
            }
            if (requirements.toolCalling && text?.strictTools === false) return false;
            if (requirements.vision && text?.visionSupported === false) return false;
            if (requirements.streamOutput && text?.streaming === false) return false;
            return true;
        });
    }, [capabilities.modelRequirements, configStore]);
    const targetModel = context.target?.model;
    const fallbackModel = targetModel?.modelKey || targetModel?.label || configStore.textModel || configStore.model || textModels[0] || "";
    const selectedModel = state.model || fallbackModel;
    const selectedTextCapability = useMemo(() => {
        try {
            return modelCapabilityConfigFor(configStore, selectedModel).text;
        } catch {
            return undefined;
        }
    }, [configStore, selectedModel]);
    const thinkingLevels = useMemo(() => thinkingLevelsFor(selectedTextCapability?.thinking), [selectedTextCapability]);
    const reasoningSupported = thinkingLevels.length > 0;
    const selectedSkillIds = state.skillIds;
    const allowUserSkills = capabilities.allowUserSkills === true;
    const defaultSkillIds = useMemo(() => new Set(capabilities.defaultSkills?.skills.map((skill) => skill.skillId) || []), [capabilities.defaultSkills?.skills]);
    const slashSkills = useMemo(() => (allowUserSkills ? skills : skills.filter((skill) => defaultSkillIds.has(skill.skillId))), [allowUserSkills, defaultSkillIds, skills]);
    const slashCandidates = useMemo(() => {
        if (!slash) return [];
        const query = slash.query.trim().toLocaleLowerCase();
        return slashSkills.filter((skill) => !query || `${skill.skillName} ${skill.description || ""}`.toLocaleLowerCase().includes(query)).slice(0, 8);
    }, [slash, slashSkills]);
    const question = pendingAgentQuestion(state.messages, state.queue?.awaitingQuestionId);
    const imageApproval = state.approval ? agentImageApproval({ call: state.approval.call }) : null;
    const dock = bindings.dock;
    const docked = Boolean(open && dock?.active && dock.available);

    const updateDraft = useCallback(
        (value: string) => {
            setDraft(value);
            controller?.setDraft(value);
            const match = /(^|\s)[/、]([^\s/、]*)$/.exec(value);
            setSlash(match && slashSkills.length ? { start: match.index + match[1].length, query: match[2] } : null);
        },
        [controller, slashSkills.length],
    );

    useEffect(() => {
        if (state.status !== "ready") return;
        if (state.draft !== draft) setDraft(state.draft);
        // Only mirror cross-conversation/restored drafts; typing stays synchronous.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [state.activeConversationId, state.draft, state.status]);

    useEffect(() => {
        if (state.status !== "ready" || state.model || !fallbackModel) return;
        controller?.setModel(fallbackModel);
    }, [controller, fallbackModel, state.model, state.status]);

    useEffect(() => {
        if (!reasoningSupported && state.reasoningMode !== "off") controller?.setReasoningMode("off");
    }, [controller, reasoningSupported, state.reasoningMode]);

    useEffect(() => {
        if (!capabilities.permissionModes.includes(permissionMode)) setPermissionMode(capabilities.permissionModes[0] || "read_only");
    }, [capabilities.permissionModes, permissionMode]);

    useEffect(() => {
        if (state.run?.permissionMode && capabilities.permissionModes.includes(state.run.permissionMode)) setPermissionMode(state.run.permissionMode);
    }, [capabilities.permissionModes, state.run?.permissionMode]);

    useEffect(() => {
        setEpisodeId(state.run?.dramaEpisodeId || context.target?.dramaEpisodeId || "");
    }, [context.target?.dramaEpisodeId, state.run?.dramaEpisodeId]);

    useEffect(() => {
        setApprovalMediaSettings(undefined);
    }, [state.approval?.approvalId]);

    useEffect(() => {
        if (!capabilities.skills) {
            setSkills([]);
            return;
        }
        let active = true;
        setSkillsLoading(true);
        void listAddedSkills()
            .then((result) => {
                if (active) setSkills(result.skills);
            })
            .catch(() => {
                if (active) setSkills([]);
            })
            .finally(() => {
                if (active) setSkillsLoading(false);
            });
        return () => {
            active = false;
        };
    }, [capabilities.skills]);

    useEffect(() => {
        if (!controller) return;
        return controller.subscribeEvent((event) => bindingsRef.current.onEvent?.(event));
    }, [controller]);

    const openedRun = useRef("");
    useEffect(() => {
        if (!controller || !initialRunId || state.status !== "ready" || openedRun.current === initialRunId) return;
        openedRun.current = initialRunId;
        void controller.openRun(initialRunId).catch((cause) => {
            openedRun.current = "";
            void message.error(cause instanceof Error ? cause.message : "历史运行读取失败");
        });
    }, [controller, initialRunId, message, state.status]);

    const route = useMemo(() => {
        if (!selectedModel) return { logicalModelId: "", channelId: "", channelModelKey: "" };
        if (targetModel && [targetModel.modelKey, targetModel.label, targetModel.logicalModelId].filter(Boolean).some((value) => value === selectedModel || value === modelOptionName(selectedModel))) {
            return {
                logicalModelId: targetModel.logicalModelId || "",
                channelId: targetModel.channelId || "",
                channelModelKey: targetModel.modelKey || "",
            };
        }
        const resolved = resolveModelRequestConfig({ ...configStore, model: selectedModel }, selectedModel);
        return {
            logicalModelId: logicalModelIDForConfig({ ...configStore, model: selectedModel }),
            channelId: resolved.channelId || "",
            channelModelKey: modelOptionName(selectedModel) || "",
        };
    }, [configStore, selectedModel, targetModel]);

    const submitPrompt = useCallback(
        async (raw: string, options: { preserveDraft?: boolean; replyToQuestionId?: string; focusNodeIds?: string[] } = {}) => {
            if (!controller || !raw.trim() || sending || state.busy || state.status !== "ready") return false;
            if (!capabilities.availability.configured) {
                void message.error(capabilities.availability.reason || "Agent Worker 当前不可用");
                return false;
            }
            const prompt = raw.trim();
            const mentionSkills = resolveSkillMentions(prompt, skills, selectedSkillIds).map((skill) => skill.skillId);
            const skillIds = [...new Set([...selectedSkillIds, ...mentionSkills])];
            if (skillIds.length && !capabilities.skills) {
                void message.error("当前业务未开放技能选择");
                return false;
            }
            if (skillIds.some((id) => !allowUserSkills && !defaultSkillIds.has(id))) {
                void message.error("当前业务只允许使用默认技能");
                return false;
            }
            setSending(true);
            try {
                const idempotencyKey = createClientUUID();
                const request = adapter.buildRequest({
                    context,
                    conversation: activeRecord,
                    capabilities,
                    prompt,
                    idempotencyKey,
                    selection: {
                        model: modelOptionName(selectedModel) || selectedModel,
                        logicalModelId: route.logicalModelId || undefined,
                        channelId: route.channelId || undefined,
                        channelModelKey: route.channelModelKey || undefined,
                        reasoningMode: reasoningSupported ? state.reasoningMode : "off",
                        skillIds,
                        permissionMode,
                        episodeId: episodeId || undefined,
                        maxCredits,
                        focusNodeIds: options.focusNodeIds,
                    },
                });
                const ok = await controller.submit({
                    request,
                    preserveDraft: options.preserveDraft,
                    ...(options.replyToQuestionId ? { mode: "interject" as const, replyToQuestionId: options.replyToQuestionId } : running ? { mode: state.sendMode } : {}),
                });
                if (ok && !options.preserveDraft) {
                    setDraft("");
                    controller.setDraft("");
                }
                if (!ok && state.pending?.request) void message.warning("上一条请求结果未确认，请先原样重试");
                return ok;
            } catch (cause) {
                void message.error(cause instanceof Error ? cause.message : "无法准备本次请求");
                return false;
            } finally {
                setSending(false);
            }
        },
        [
            activeRecord,
            adapter,
            allowUserSkills,
            capabilities,
            context,
            controller,
            defaultSkillIds,
            episodeId,
            maxCredits,
            message,
            reasoningSupported,
            route.channelId,
            route.channelModelKey,
            route.logicalModelId,
            running,
            selectedModel,
            selectedSkillIds,
            sending,
            skills,
            state.busy,
            state.pending?.request,
            state.reasoningMode,
            state.sendMode,
            state.status,
            permissionMode,
        ],
    );

    const handledPrefills = useRef(new Set<string | number>());
    useEffect(() => {
        if (!bindings.takePrefill || state.status !== "ready") return;
        const request: AgentPanelPrefill | null = bindings.takePrefill();
        if (!request || handledPrefills.current.has(request.id)) return;
        handledPrefills.current.add(request.id);
        if (request.autoSubmit) {
            void submitPrompt(request.text, { preserveDraft: true, focusNodeIds: request.focusNodeIds }).then((ok) => {
                request.settle(ok ? (running ? "queued" : "sent") : false);
            });
        } else {
            updateDraft(draft.trim() ? `${draft}\n\n${request.text}` : request.text);
            request.settle("prefilled");
        }
    }, [bindings, draft, running, state.status, submitPrompt, updateDraft]);

    const applySkill = (skill: Skill) => {
        if (!slash) return;
        const token = `@[skill:${skill.skillId}] `;
        updateDraft(`${draft.slice(0, slash.start)}${token}${draft.slice(slash.start + 1 + slash.query.length)}`);
        setSlash(null);
        controller?.setSkillIds([...selectedSkillIds, skill.skillId]);
    };

    const stop = async () => {
        if (!controller || stopping) return;
        setStopping(true);
        try {
            await controller.cancelRun();
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "停止失败");
        } finally {
            setStopping(false);
        }
    };

    const decideApproval = async (decision: "approve" | "reject") => {
        if (!controller || approvalBusy) return;
        setApprovalBusy(true);
        try {
            if (decision === "approve") await bindingsRef.current.prepareSubmission?.();
            await controller.decideApproval(decision, {
                ...(approvalReason.trim() ? { reason: approvalReason.trim() } : {}),
                ...(decision === "approve" && approvalMediaSettings ? { mediaSettings: approvalMediaSettings } : {}),
            });
            setApprovalReason("");
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "审批失败");
        } finally {
            setApprovalBusy(false);
        }
    };

    const retryPending = async () => {
        try {
            await controller?.retryPending();
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "原请求重试失败");
        }
    };

    const openConversation = (record: AgentConversationRecord) => {
        setView("chat");
        void controller?.openConversation(record.id);
    };

    const newConversation = () => {
        setView("chat");
        setDraft("");
        void controller?.newConversation();
    };

    const sendModeControl = agentPanelControlEnabled(config, "sendMode") ? (
        <Select
            size="small"
            value={state.sendMode}
            aria-label="发送方式"
            options={[
                { value: "queue", label: "排队" },
                { value: "interject", label: "插话" },
            ]}
            onChange={(value) => controller?.setSendMode(value)}
            popupMatchSelectWidth={false}
        />
    ) : null;

    const modelControl = agentPanelControlEnabled(config, "model") ? (
        <ModelPicker config={configStore} value={selectedModel} onChange={(value) => controller?.setModel(value)} capability="text" variant="default" showSelectedPrice={false} popoverClassName="z-[var(--z-agent-overlay)]" />
    ) : null;

    const reasoningControl =
        agentPanelControlEnabled(config, "reasoning") && reasoningSupported ? (
            <ThinkingLevelSlider
                levels={thinkingLevels}
                value={state.reasoningMode as ThinkingRequest}
                resolvedLevel={resolveThinkingLevel(selectedTextCapability?.thinking, state.reasoningMode as ThinkingRequest) as ThinkingLevel | ""}
                onChange={(value) => controller?.setReasoningMode(value)}
            />
        ) : null;

    const skillsControl = agentPanelControlEnabled(config, "skills") ? (
        <Popover
            placement="topLeft"
            content={
                <div className="w-72 text-xs">
                    <div className="flex items-center justify-between gap-2">
                        <strong>技能</strong>
                        <Tag color={allowUserSkills ? "blue" : "default"}>{allowUserSkills ? "可选择" : "只读"}</Tag>
                    </div>
                    {skillsLoading ? <p className="mt-2 text-neutral-500">正在读取技能…</p> : null}
                    {!skillsLoading && !skills.length ? <p className="mt-2 text-neutral-500">当前没有已安装技能</p> : null}
                    <div className="mt-2 max-h-64 space-y-1 overflow-y-auto">
                        {skills.map((skill) => {
                            const selected = selectedSkillIds.includes(skill.skillId);
                            const allowed = allowUserSkills || defaultSkillIds.has(skill.skillId);
                            return (
                                <label key={skill.skillId} className={`flex items-start gap-2 rounded-lg px-2 py-1.5 ${allowed ? "cursor-pointer hover:bg-black/5" : "opacity-55"}`}>
                                    <input
                                        type="checkbox"
                                        checked={selected}
                                        disabled={!allowed}
                                        onChange={() => {
                                            controller?.setSkillIds(selected ? selectedSkillIds.filter((id) => id !== skill.skillId) : [...selectedSkillIds, skill.skillId]);
                                        }}
                                    />
                                    <span className="min-w-0">
                                        <span className="block truncate font-medium">{skill.skillName}</span>
                                        <span className="block line-clamp-2 text-neutral-500">{skill.description}</span>
                                    </span>
                                </label>
                            );
                        })}
                    </div>
                </div>
            }
        >
            <button type="button" className="flex items-center gap-1 rounded-full border border-black/10 px-2 py-1 text-[11px]" aria-label="选择技能">
                <Sparkles className="size-3.5" />
                技能{selectedSkillIds.length ? ` ${selectedSkillIds.length}` : ""}
            </button>
        </Popover>
    ) : null;

    const permissionControl =
        capabilities.permissionModes.length > 1 ? (
            <Select
                size="small"
                value={permissionMode}
                aria-label="执行权限"
                options={capabilities.permissionModes.map((mode) => ({ value: mode, label: mode === "read_only" ? "只读" : mode === "auto" ? "自动执行" : "请求审批" }))}
                onChange={(mode) => setPermissionMode(mode)}
                popupMatchSelectWidth={false}
            />
        ) : null;

    const panelStyle: CSSProperties = docked ? { top: 0, right: 0, width: dock?.width || 420, height: "100dvh", zIndex: 70 } : { right: 16, bottom: 16, width: "min(440px, calc(100vw - 32px))", height: "min(720px, calc(100dvh - 32px))", zIndex: 70 };

    if (!open) {
        return (
            <button
                type="button"
                onClick={onOpen}
                className="fixed flex size-16 items-center justify-center rounded-full border border-black/10 bg-white shadow-xl"
                style={{ right: 20, bottom: 20, zIndex: 70 }}
                aria-label={`打开${appearance?.agentName || config.title}`}
                data-agent-launcher
            >
                <FluidOrb size={52} color="#7164f6" />
                {state.approval ? <span className="absolute right-0 top-0 size-3 rounded-full border-2 border-white bg-amber-500" aria-label="待审批" /> : null}
            </button>
        );
    }

    return (
        <aside className="agent-panel-surface fixed flex min-h-0 flex-col overflow-hidden rounded-2xl border border-black/10" style={panelStyle} aria-label={config.title} data-agent-open data-docked={docked || undefined} data-agent-panel>
            <header className="flex shrink-0 items-center gap-2 border-b border-black/10 px-4 py-3" data-agent-drag-handle={docked ? undefined : ""}>
                <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-semibold">{appearance?.panelTitle || config.title}</div>
                    <div className="truncate text-[11px] text-neutral-500">
                        {statusLabel(state.run)}
                        {activeRecord?.title ? ` · ${activeRecord.title}` : ""}
                    </div>
                </div>
                {bindings.headerAccessory}
                {agentPanelControlEnabled(config, "contextUsage") ? <ContextRing usage={state.contextUsage || emptyAgentContextUsage("")} /> : null}
                {config.docking === "floating-and-docked" && dock?.available && dock.onToggle ? (
                    <Button type="text" size="small" icon={<Settings2 className="size-4" />} aria-label={dock.active ? "取消停靠" : "停靠到右侧"} title={dock.active ? "取消停靠" : "停靠到右侧"} onClick={dock.onToggle} />
                ) : null}
                <Dropdown
                    trigger={["click"]}
                    menu={{
                        items: [
                            { key: "settings", label: "设置", icon: <Settings2 className="size-4" />, onClick: () => setView("settings") },
                            { key: "history", label: "历史对话", icon: <History className="size-4" />, onClick: () => setView("history") },
                            { key: "new", label: "新建对话", icon: <MessageSquarePlus className="size-4" />, onClick: newConversation },
                            ...(dock?.onReset ? [{ key: "reset", label: "恢复默认窗口", icon: <RotateCcw className="size-4" />, onClick: dock.onReset }] : []),
                        ],
                    }}
                >
                    <Button type="text" size="small" icon={<Settings2 className="size-4" />} aria-label="更多操作" />
                </Dropdown>
                <Button type="text" size="small" icon={<X className="size-4" />} aria-label="收起 Agent" onClick={onClose} />
            </header>

            {view === "history" ? (
                <div className="min-h-0 flex-1 overflow-y-auto p-3">
                    <div className="mb-2 flex items-center gap-2">
                        <Button size="small" onClick={() => setView("chat")}>
                            返回
                        </Button>
                        <Button size="small" icon={<MessageSquarePlus className="size-3.5" />} onClick={newConversation}>
                            新对话
                        </Button>
                    </div>
                    <div className="space-y-1.5">
                        {state.conversations.map((record) => (
                            <div key={record.id} className={`rounded-xl border px-3 py-2 ${record.id === state.activeConversationId ? "border-black/25 bg-black/[0.045]" : "border-black/10"}`}>
                                <button type="button" className="w-full text-left" onClick={() => openConversation(record)}>
                                    <span className="block truncate text-sm">{record.title || "新对话"}</span>
                                    <span className="block truncate text-[11px] text-neutral-500">
                                        {record.run ? statusLabel(record.run) : "本地草稿"} · {new Date(record.updatedAt).toLocaleString("zh-CN")}
                                    </span>
                                </button>
                                {record.serverConversationId ? (
                                    <Button size="small" type="text" danger icon={<Archive className="size-3.5" />} onClick={() => void controller?.archiveConversation(record.id)}>
                                        归档
                                    </Button>
                                ) : null}
                            </div>
                        ))}
                    </div>
                </div>
            ) : view === "settings" ? (
                <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-4 text-sm">
                    <div className="flex items-center justify-between">
                        <strong>Agent 设置</strong>
                        <Button size="small" onClick={() => setView("chat")}>
                            返回
                        </Button>
                    </div>
                    <section className="space-y-2">
                        <div className="text-xs font-medium text-neutral-500">模型与推理</div>
                        {modelControl}
                        {reasoningControl || <p className="text-xs text-neutral-500">当前模型不支持推理档位。</p>}
                    </section>
                    <section className="space-y-2">
                        <div className="text-xs font-medium text-neutral-500">发送与技能</div>
                        <div className="flex flex-wrap items-center gap-2">
                            {sendModeControl}
                            {permissionControl}
                            {skillsControl}
                        </div>
                        {capabilities.defaultSkills?.skills.length ? <p className="text-xs text-neutral-500">默认技能：{capabilities.defaultSkills.skills.map((skill) => skill.skillName).join("、")}</p> : null}
                    </section>
                    <section className="space-y-1 rounded-xl border border-black/10 p-3 text-xs text-neutral-600">
                        <p>
                            业务范围：{context.scope?.type}/{context.scope?.id}
                        </p>
                        <p>Agent 定义：{context.agentDefinitionId || config.agentDefinitionId}</p>
                        <p>写入能力：{capabilities.writeTools ? "已授权" : "只读"}</p>
                        <p>用户自选技能：{allowUserSkills ? "允许" : "不可用"}</p>
                        <p>
                            模型要求：
                            {[
                                capabilities.modelRequirements?.text !== false ? "文本" : "",
                                capabilities.modelRequirements?.toolCalling ? "工具调用" : "",
                                capabilities.modelRequirements?.streamOutput ? "流式输出" : "",
                                capabilities.modelRequirements?.vision ? "图片输入" : "",
                            ]
                                .filter(Boolean)
                                .join(" / ") || "服务端未声明"}
                        </p>
                    </section>
                </div>
            ) : (
                <>
                    <div className="agent-panel-scroll min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-4">
                        {!capabilities.availability.configured ? <Alert type="warning" showIcon message="Agent Worker 暂不可用" description={capabilities.availability.reason || capabilities.availability.queueReason} /> : null}
                        {state.status === "error" ? <Alert type="error" showIcon message={state.error || "会话加载失败"} /> : null}
                        {activeRecord?.historyState?.status === "unavailable" ? <Alert type="warning" showIcon message={activeRecord.historyState.message} /> : null}
                        {bindings.targetControl || config.extensions.includes("episode-target") ? (
                            <div className="agent-panel-target flex flex-wrap items-end gap-2 rounded-xl border border-black/10 bg-white/70 px-3 py-2 text-xs">
                                {bindings.targetControl}
                                {config.extensions.includes("episode-target") && context.target?.episodeOptions?.length ? (
                                    <>
                                        <label className="min-w-0">
                                            <span className="mb-1 block text-neutral-500">本轮剧集</span>
                                            <select className="max-w-56 rounded-md border border-black/15 bg-white px-2 py-1" value={episodeId} onChange={(event) => setEpisodeId(event.target.value)}>
                                                {context.target.episodeOptions.map((option) => (
                                                    <option key={option.id} value={option.id}>
                                                        {option.label}
                                                    </option>
                                                ))}
                                            </select>
                                        </label>
                                        <label>
                                            <span className="mb-1 block text-neutral-500">积分上限</span>
                                            <InputNumber min={0.01} max={1_000_000} value={maxCredits} onChange={(value) => setMaxCredits(value ?? 20)} className="w-24" />
                                        </label>
                                    </>
                                ) : null}
                            </div>
                        ) : null}
                        {!state.messages.length && state.status === "ready" ? (
                            <div className="rounded-2xl border border-dashed border-black/15 p-5">
                                <Sparkles className="size-5 opacity-60" />
                                <p className="mt-2 text-sm font-medium">{config.welcome}</p>
                                {bindings.welcomeAccessory}
                            </div>
                        ) : null}
                        <MessageList messages={state.messages} busy={state.busy || running} />
                        <PlanBar messages={state.messages} />
                        {bindings.references?.length ? (
                            <div className="flex flex-wrap gap-1.5">
                                {bindings.references.slice(0, 12).map((reference) => (
                                    <button
                                        key={reference.id}
                                        type="button"
                                        className="rounded-full border border-black/10 bg-white/70 px-2 py-1 text-[11px] text-neutral-600"
                                        title={reference.title}
                                        onClick={() => bindingsRef.current.focusReference?.(reference)}
                                    >
                                        {reference.label}
                                    </button>
                                ))}
                            </div>
                        ) : null}
                    </div>
                    <div className="shrink-0 border-t border-black/10 p-3">
                        {question ? (
                            <div className="mb-2 rounded-xl border border-amber-300/70 bg-amber-50 p-3 text-sm">
                                <p className="font-medium">{question.question}</p>
                                <div className="mt-2 flex flex-wrap gap-2">
                                    {question.options.map((option) => (
                                        <Button key={option.label} size="small" onClick={() => void submitPrompt(option.label, { replyToQuestionId: question.id })}>
                                            {option.label}
                                        </Button>
                                    ))}
                                </div>
                            </div>
                        ) : null}
                        {state.approval ? (
                            <div className="mb-2 rounded-xl border border-sky-300/70 bg-sky-50 p-3 text-sm">
                                <div className="flex items-start justify-between gap-2">
                                    <div>
                                        <p className="font-medium">{state.approval.preview?.title || "Agent 请求审批"}</p>
                                        <p className="mt-1 text-xs text-neutral-600">{state.approval.preview?.description || "批准后才会执行本次业务写入。"}</p>
                                    </div>
                                    <Tag color="gold">写入</Tag>
                                </div>
                                {state.approval.call ? (
                                    <pre className="mt-2 max-h-32 overflow-auto rounded-lg bg-black/[0.05] p-2 text-[11px]">
                                        {state.approval.call.function.name}
                                        {"\n"}
                                        {state.approval.call.function.arguments}
                                    </pre>
                                ) : null}
                                {bindings.approvalAccessory}
                                <Input className="mt-2" size="small" value={approvalReason} onChange={(event) => setApprovalReason(event.target.value)} placeholder="可选：审批说明" />
                                <div className="mt-2 flex gap-2">
                                    <Button type="primary" size="small" loading={approvalBusy} onClick={() => void decideApproval("approve")}>
                                        批准
                                    </Button>
                                    <Button size="small" loading={approvalBusy} onClick={() => void decideApproval("reject")}>
                                        拒绝
                                    </Button>
                                </div>
                            </div>
                        ) : null}
                        <QueuePanel
                            queue={state.queue}
                            busy={state.busy}
                            onEdit={(item, prompt) => controller?.editQueued(item, prompt) || Promise.resolve(false)}
                            onPromote={(item) => controller?.interjectQueued(item) || Promise.resolve(false)}
                            onRemove={(item) => controller?.cancelQueued(item) || Promise.resolve(false)}
                            onResume={() => controller?.updateQueue("resume") || Promise.resolve(false)}
                            onPause={() => controller?.updateQueue("pause") || Promise.resolve(false)}
                        />
                        {state.pending?.request && !state.busy ? (
                            <div className="mt-2 flex items-center justify-between rounded-lg bg-amber-50 px-2 py-1.5 text-xs text-amber-800">
                                <span>上一条请求结果未确认</span>
                                <Button size="small" type="text" icon={<RotateCcw className="size-3.5" />} onClick={() => void retryPending()}>
                                    原样重试
                                </Button>
                            </div>
                        ) : null}
                        <div className="relative mt-2">
                            {slash && slashCandidates.length ? (
                                <div className="absolute bottom-full left-0 z-10 mb-2 w-full max-w-xs overflow-hidden rounded-xl border border-black/10 bg-white p-1.5 shadow-2xl">
                                    {slashCandidates.map((skill) => (
                                        <button key={skill.skillId} type="button" className="block w-full rounded-lg px-2 py-1.5 text-left text-xs hover:bg-black/5" onClick={() => applySkill(skill)}>
                                            <span className="block truncate font-medium">{skill.skillName}</span>
                                            {skill.description ? <span className="block truncate text-neutral-500">{skill.description}</span> : null}
                                        </button>
                                    ))}
                                </div>
                            ) : null}
                            <Input.TextArea
                                value={draft}
                                onChange={(event) => updateDraft(event.target.value)}
                                onKeyDown={(event) => {
                                    if (event.key === "Escape") {
                                        setSlash(null);
                                        return;
                                    }
                                    if (event.key === "Enter" && !event.shiftKey) {
                                        event.preventDefault();
                                        if (slash && slashCandidates[0]) applySkill(slashCandidates[0]);
                                        else void submitPrompt(draft, question?.id ? { replyToQuestionId: question.id } : {});
                                    }
                                }}
                                autoSize={{ minRows: 2, maxRows: 8 }}
                                placeholder={config.placeholder}
                                disabled={state.status !== "ready" || Boolean(activeRecord?.historyState?.status === "unavailable")}
                                aria-label="Agent 输入"
                            />
                        </div>
                        <div className="mt-2 flex flex-wrap items-center justify-between gap-2">
                            <div className="flex min-w-0 flex-wrap items-center gap-2">
                                {bindings.addFiles ? (
                                    <label className="flex cursor-pointer items-center gap-1 rounded-full border border-black/10 px-2 py-1 text-[11px]" title="添加图片到当前业务">
                                        <Paperclip className="size-3.5" />
                                        <span>附件</span>
                                        <input
                                            type="file"
                                            hidden
                                            multiple
                                            accept="image/png,image/jpeg,image/webp,image/gif"
                                            onChange={(event) => {
                                                void bindings.addFiles?.(event.target.files);
                                                event.target.value = "";
                                            }}
                                        />
                                    </label>
                                ) : null}
                                {modelControl}
                                {reasoningControl}
                                {sendModeControl}
                                {permissionControl}
                                {skillsControl}
                            </div>
                            <div className="flex items-center gap-2">
                                {running ? (
                                    <Button size="small" icon={stopping ? <LoaderCircle className="size-3.5 animate-spin" /> : <CircleStop className="size-3.5" />} disabled={stopping} onClick={() => void stop()}>
                                        停止
                                    </Button>
                                ) : null}
                                <Button
                                    type="primary"
                                    size="small"
                                    icon={sending || state.busy ? <LoaderCircle className="size-3.5 animate-spin" /> : <Send className="size-3.5" />}
                                    loading={sending || state.busy}
                                    disabled={!draft.trim() || state.status !== "ready"}
                                    onClick={() => void submitPrompt(draft, question?.id ? { replyToQuestionId: question.id } : {})}
                                >
                                    {running ? (state.sendMode === "queue" ? "排队" : "插话") : "发送"}
                                </Button>
                            </div>
                        </div>
                    </div>
                </>
            )}
        </aside>
    );
}

export function AgentPanel({
    userId,
    context,
    config,
    adapter,
    client,
    capabilities,
    open,
    onOpen,
    onClose,
    initialRunId,
}: {
    userId?: string;
    context: AgentPageContext;
    config: AgentPanelConfig;
    adapter: AgentPanelAdapter;
    client: ScopedClient;
    capabilities: AgentCapabilities;
    open: boolean;
    onOpen: () => void;
    onClose: () => void;
    initialRunId?: string;
}) {
    const { message } = App.useApp();
    const bindings = useAgentPanelBindings(adapter);
    const bindingsRef = useRef(bindings);
    bindingsRef.current = bindings;
    const userScope = userId || "";
    const configStore = useEffectiveConfig();
    const theme = useAgentTheme();
    const reducedMotion = useReducedMotion();
    const appearance = useAppearanceStore((state) => state.appearance.canvas) || DEFAULT_CANVAS_APPEARANCE;
    const [view, setView] = useState<PanelView>("chat");
    const [draft, setDraft] = useState("");
    const [sending, setSending] = useState(false);
    const [stopping, setStopping] = useState(false);
    const [skills, setSkills] = useState<Skill[]>([]);
    const [skillsLoading, setSkillsLoading] = useState(false);
    const [marketSkills, setMarketSkills] = useState<Skill[]>([]);
    const [skillCategories, setSkillCategories] = useState<SkillCategory[]>([]);
    const [skillSearch, setSkillSearch] = useState("");
    const [skillTag, setSkillTag] = useState("all");
    const [skillHasMore, setSkillHasMore] = useState(false);
    const [skillPage, setSkillPage] = useState(1);
    const [skillsOpen, setSkillsOpen] = useState(false);
    const [episodeId, setEpisodeId] = useState(context.target?.dramaEpisodeId || "");
    const [maxCredits, setMaxCredits] = useState(20);
    const [permissionMode, setPermissionMode] = useState<AgentPermissionMode>(capabilities.permissionModes.includes("auto") ? "auto" : capabilities.permissionModes[0] || "read_only");
    const [approvalReason, setApprovalReason] = useState("");
    const [approvalMediaSettings, setApprovalMediaSettings] = useState<NonNullable<AgentApprovalDecision["mediaSettings"]>>();
    const [approvalBusy, setApprovalBusy] = useState(false);
    const [planMinimized, setPlanMinimized] = useState(false);
    const [exporting, setExporting] = useState(false);
    const [uiSettings, setUiSettings] = useState<{ defaultSendMode: "queue" | "interject"; revision: number } | null>(null);
    const [sendModeSaving, setSendModeSaving] = useState(false);
    const [sendModeError, setSendModeError] = useState("");
    const [undoingRunId, setUndoingRunId] = useState("");
    const [localAttachments, setLocalAttachments] = useState<Array<CloudAgentChatAttachment & { file?: File }>>([]);
    const localAttachmentsRef = useRef(localAttachments);
    localAttachmentsRef.current = localAttachments;
    const prepareRef = useRef<() => Promise<void>>(async () => {});
    prepareRef.current = async () => {
        await bindingsRef.current.prepareSubmission?.();
    };
    const { controller, state } = useAgentConversation({
        userScope,
        scope: context.scope || null,
        agentDefinitionId: context.agentDefinitionId || config.agentDefinitionId,
        client: context.scope ? client : null,
        prepare: useCallback(() => prepareRef.current(), []),
    });

    useEffect(
        () => () => {
            for (const item of localAttachmentsRef.current) URL.revokeObjectURL(item.url);
        },
        [],
    );
    const running = runRunning(state.run);
    const activeRecord = state.conversations.find((item) => item.id === state.activeConversationId) || null;
    const modelFilter = useMemo(() => agentModelFilter(configStore, capabilities.modelRequirements), [capabilities.modelRequirements, configStore]);
    const textModels = useMemo(() => {
        const models = selectableModelsByCapability(configStore, "text");
        return modelFilter ? models.filter((model) => modelFilter(model)) : models;
    }, [configStore, modelFilter]);
    const targetModel = context.target?.model;
    const fallbackModel = agentPanelDefaultModel(configStore, textModels, targetModel);
    const selectedModel = textModels.includes(state.model) ? state.model : fallbackModel;
    const selectedTextCapability = useMemo(() => {
        try {
            return modelCapabilityConfigFor(configStore, selectedModel).text;
        } catch {
            return undefined;
        }
    }, [configStore, selectedModel]);
    const thinkingLevels = useMemo(() => thinkingLevelsFor(selectedTextCapability?.thinking), [selectedTextCapability]);
    const reasoningSupported = thinkingLevels.length > 0;
    const allowUserSkills = capabilities.allowUserSkills === true;
    const defaultSkills = capabilities.defaultSkills?.skills || [];
    const defaultSkillIds = useMemo(() => new Set(defaultSkills.map((skill) => skill.skillId)), [defaultSkills]);
    const selectedSkillIds = state.skillIds;
    const slashSkills = allowUserSkills ? skills : [];
    const question = pendingAgentQuestion(state.messages, state.queue?.awaitingQuestionId);
    const imageApproval = state.approval ? agentImageApproval({ call: state.approval.call }) : null;
    const planMessage = [...state.messages].reverse().find((item) => item.planItems?.length);
    const planItems = planMessage?.planItems || [];
    const planTerminal = Boolean((planMessage as { planTerminal?: boolean } | undefined)?.planTerminal || (state.run && TERMINAL.has(state.run.status)));
    const docked = Boolean(open && bindings.dock?.active && bindings.dock.available);
    const panelLayout = useAgentPanelLayout({
        active: docked,
        width: bindings.dock?.width || 420,
        onWidthChange: bindings.dock?.onResize,
    });
    const panelRef = useRef<HTMLElement>(null);
    const status = state.run?.status || "idle";
    const statusColor = status === "failed" ? "#e66b6b" : status === "rejected" || status === "cancelled" ? theme.node.muted : status === "waiting_approval" ? "#d6a24a" : status === "running" || status === "queued" ? "#69c29b" : theme.node.muted;
    const messages = state.messages as CloudAgentChatMessage[];
    const mediaBatch = latestAgentMediaBatch(state.messages);
    const undoOffer = agentRunUndoOffer(state.run, state.messages);
    const references = useMemo(() => {
        const availableSkills = allowUserSkills ? skills : skills.filter((skill) => defaultSkillIds.has(skill.skillId));
        return [...(bindings.references || []), ...buildSkillMentionReferences(availableSkills)];
    }, [allowUserSkills, bindings.references, defaultSkillIds, skills]);

    useEffect(() => {
        if (state.status !== "ready") return;
        if (state.draft !== draft) setDraft(state.draft);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [state.activeConversationId, state.draft, state.status]);

    useEffect(() => {
        if (state.status !== "ready" || !selectedModel || state.model === selectedModel) return;
        controller?.setModel(selectedModel);
    }, [controller, selectedModel, state.model, state.status]);

    useEffect(() => {
        if (!reasoningSupported && state.reasoningMode !== "off") controller?.setReasoningMode("off");
    }, [controller, reasoningSupported, state.reasoningMode]);

    useEffect(() => {
        if (!capabilities.permissionModes.includes(permissionMode)) setPermissionMode(capabilities.permissionModes[0] || "read_only");
    }, [capabilities.permissionModes, permissionMode]);

    useEffect(() => {
        setEpisodeId(state.run?.dramaEpisodeId || context.target?.dramaEpisodeId || "");
    }, [context.target?.dramaEpisodeId, state.run?.dramaEpisodeId]);

    useEffect(() => {
        setApprovalMediaSettings(undefined);
    }, [state.approval?.approvalId]);

    useEffect(() => {
        if (typeof client.getUISettings !== "function") return;
        let active = true;
        setSendModeError("");
        setUiSettings(null);
        void client
            .getUISettings()
            .then((value) => {
                if (!active) return;
                setUiSettings(value);
                controller?.setSendMode(value.defaultSendMode);
            })
            .catch(() => {
                if (active) setSendModeError("发送方式加载失败，请重试");
            });
        return () => {
            active = false;
        };
    }, [client, controller]);

    useEffect(() => {
        if (!capabilities.skills) {
            setSkills([]);
            return;
        }
        let active = true;
        setSkillsLoading(true);
        void listAddedSkills()
            .then((result) => {
                if (active) setSkills(result.skills);
            })
            .catch(() => {
                if (active) setSkills([]);
            })
            .finally(() => {
                if (active) setSkillsLoading(false);
            });
        const refresh = () => {
            void listAddedSkills().then((result) => {
                if (active) setSkills(result.skills);
            });
        };
        window.addEventListener("canvas-skills-changed", refresh);
        window.addEventListener("focus", refresh);
        return () => {
            active = false;
            window.removeEventListener("canvas-skills-changed", refresh);
            window.removeEventListener("focus", refresh);
        };
    }, [capabilities.skills, userScope]);

    useEffect(() => {
        if (!open || (!skillsOpen && view !== "settings") || !capabilities.skills) return;
        let active = true;
        setSkillsLoading(true);
        void listSkills({
            scope: "public",
            search: skillSearch.trim() || undefined,
            tag: skillTag !== "all" ? skillTag : undefined,
            pageSize: 20,
            sort: "popular",
        })
            .then((result) => {
                if (!active) return;
                setMarketSkills(result.skills);
                setSkillHasMore(result.hasMore);
                setSkillPage(result.page);
                if (result.categories.length) setSkillCategories(result.categories);
            })
            .catch(() => {
                if (active) setMarketSkills([]);
            })
            .finally(() => {
                if (active) setSkillsLoading(false);
            });
        return () => {
            active = false;
        };
    }, [capabilities.skills, open, skillSearch, skillTag, skillsOpen, view]);

    useEffect(() => {
        if (!allowUserSkills && controller) {
            const filtered = selectedSkillIds.filter((id) => defaultSkillIds.has(id));
            if (filtered.length !== selectedSkillIds.length) controller.setSkillIds(filtered);
        }
    }, [allowUserSkills, controller, defaultSkillIds, selectedSkillIds]);

    const updateDraft = useCallback(
        (value: string) => {
            setDraft(value);
            controller?.setDraft(value);
        },
        [controller],
    );

    const addAttachments = async (input: FileList | File[] | null) => {
        const upload = bindingsRef.current.addFiles;
        if (!upload) return;
        const files = Array.from(input || []);
        const accepted = checkAgentAttachmentFiles(files, {
            existingCount: localAttachments.length,
            existingKeys: new Set(
                localAttachments
                    .map((item) => item.file)
                    .filter((file): file is File => Boolean(file))
                    .map((file) => `${file.name}:${file.size}:${file.lastModified}`),
            ),
            maxBytes: 50 * 1024 * 1024,
        });
        if (accepted.problems.length) void message.warning(accepted.problems.join("；"));
        if (!accepted.accepted.length) return;
        const pending = accepted.accepted.map((file, index) => ({
            id: `local-${Date.now()}-${index}`,
            name: file.name,
            url: URL.createObjectURL(file),
            kind: "image" as const,
            status: "uploading" as AgentAttachmentState["status"],
            file,
        }));
        setLocalAttachments((current) => [...current, ...pending]);
        try {
            const result = await upload(accepted.accepted);
            const nodeIds = Array.isArray(result) ? result : [];
            setLocalAttachments((current) =>
                current.map((item) => {
                    const index = pending.findIndex((entry) => entry.id === item.id);
                    return index >= 0 ? { ...item, nodeId: nodeIds[index], status: nodeIds[index] ? ("ready" as const) : ("error" as const), error: nodeIds[index] ? undefined : "未返回业务对象 ID" } : item;
                }),
            );
            if (nodeIds.length) updateDraft(appendAgentNodeReferences(draft, nodeIds));
        } catch (cause) {
            setLocalAttachments((current) => current.map((item) => (pending.some((entry) => entry.id === item.id) ? { ...item, status: "error" as const, error: cause instanceof Error ? cause.message : "附件保存失败" } : item)));
        }
    };

    const removeAttachment = (id: string) => {
        const item = localAttachments.find((attachment) => attachment.id === id);
        if (item) {
            URL.revokeObjectURL(item.url);
            setLocalAttachments((current) => current.filter((attachment) => attachment.id !== id));
            if (item.nodeId) updateDraft(removeAgentNodeReference(draft, item.nodeId));
            return;
        }
        bindingsRef.current.removeAttachment?.(id);
    };

    const retryAttachment = (id: string) => {
        const item = localAttachments.find((attachment) => attachment.id === id);
        if (item?.file) {
            setLocalAttachments((current) => current.filter((attachment) => attachment.id !== id));
            void addAttachments([item.file]);
            return;
        }
        bindingsRef.current.retryAttachment?.(id);
    };

    const loadMoreSkills = async () => {
        if (skillsLoading || !skillHasMore || !capabilities.skills) return;
        setSkillsLoading(true);
        try {
            const result = await listSkills({
                scope: "public",
                search: skillSearch.trim() || undefined,
                tag: skillTag !== "all" ? skillTag : undefined,
                page: skillPage + 1,
                pageSize: 20,
                sort: "popular",
            });
            setMarketSkills((current) => [...current, ...result.skills.filter((skill) => !current.some((item) => item.skillId === skill.skillId))]);
            setSkillPage(result.page);
            setSkillHasMore(result.hasMore);
        } finally {
            setSkillsLoading(false);
        }
    };

    const installSkill = async (skill: Skill) => {
        if (skill.isAdded) return;
        try {
            const result = await addSkill(skill.skillId);
            setSkills((current) => [...current.filter((item) => item.skillId !== skill.skillId), result.skill]);
            setMarketSkills((current) => current.map((item) => (item.skillId === skill.skillId ? result.skill : item)));
            if (allowUserSkills) controller?.setSkillIds([...selectedSkillIds, skill.skillId]);
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "添加 Skill 失败");
        }
    };

    const changeSendMode = async (mode: "queue" | "interject") => {
        if (sendModeSaving) return;
        if (typeof client.updateUISettings !== "function") return;
        setSendModeSaving(true);
        setSendModeError("");
        controller?.setSendMode(mode);
        try {
            const value = await client.updateUISettings({ ...(uiSettings || { defaultSendMode: mode, revision: 0 }), defaultSendMode: mode });
            setUiSettings(value);
            controller?.setSendMode(value.defaultSendMode);
        } catch {
            setSendModeError("发送方式保存失败，已尝试恢复服务端设置");
            try {
                const value = await client.getUISettings();
                setUiSettings(value);
                controller?.setSendMode(value.defaultSendMode);
            } catch {
                // Keep the local preference visible; the next change retries the write.
            }
        } finally {
            setSendModeSaving(false);
        }
    };
    const retrySendMode = async () => {
        if (typeof client.getUISettings !== "function") return;
        try {
            const value = await client.getUISettings();
            setUiSettings(value);
            controller?.setSendMode(value.defaultSendMode);
            setSendModeError("");
        } catch {
            setSendModeError("发送方式读取失败，请稍后重试");
        }
    };

    const route = useMemo(() => agentPanelModelRoute(configStore, selectedModel, targetModel), [configStore, selectedModel, targetModel]);

    const submitPrompt = useCallback(
        async (
            raw: string,
            options: {
                preserveDraft?: boolean;
                replyToQuestionId?: string;
                focusNodeIds?: string[];
                oppositeMode?: boolean;
                mode?: "queue" | "interject";
            } = {},
        ) => {
            if (!controller || !raw.trim() || sending || state.busy || state.status !== "ready") return false;
            if (!capabilities.availability.configured) {
                void message.error(capabilities.availability.reason || "Agent Worker 当前不可用");
                return false;
            }
            const prompt = raw.trim();
            const mentionSkills = resolveSkillMentions(prompt, skills, selectedSkillIds).map((skill) => skill.skillId);
            const skillIds = [...new Set([...selectedSkillIds, ...mentionSkills])];
            if (!allowUserSkills && skillIds.some((id) => !defaultSkillIds.has(id))) {
                void message.error("当前业务只允许使用默认技能");
                return false;
            }
            if (supportSkillSelection() && skillIds.length && !capabilities.skills) {
                void message.error("当前业务未开放技能选择");
                return false;
            }
            setSending(true);
            try {
                const idempotencyKey = createClientUUID();
                const request = adapter.buildRequest({
                    context,
                    conversation: activeRecord,
                    capabilities,
                    prompt,
                    idempotencyKey,
                    selection: {
                        model: modelOptionName(selectedModel) || selectedModel,
                        logicalModelId: route.logicalModelId || undefined,
                        channelId: route.channelId || undefined,
                        channelModelKey: route.channelModelKey || undefined,
                        reasoningMode: reasoningSupported ? state.reasoningMode : "off",
                        skillIds,
                        permissionMode,
                        episodeId: episodeId || undefined,
                        maxCredits,
                        focusNodeIds: options.focusNodeIds?.slice(0, 8) || bindingsRef.current.selectedNodeIds?.slice(0, 8),
                    },
                });
                const mode = options.mode || (options.oppositeMode ? (state.sendMode === "queue" ? "interject" : "queue") : state.sendMode);
                const ok = await controller.submit({
                    request,
                    preserveDraft: options.preserveDraft,
                    ...(options.replyToQuestionId ? { mode: options.mode || "interject", replyToQuestionId: options.replyToQuestionId } : running ? { mode } : {}),
                });
                if (!ok && state.pending?.request) void message.warning("上一条请求结果未确认，请先原样重试");
                return ok;
            } catch (cause) {
                void message.error(cause instanceof Error ? cause.message : "无法准备本次请求");
                return false;
            } finally {
                setSending(false);
            }
        },
        [
            activeRecord,
            adapter,
            allowUserSkills,
            capabilities,
            context,
            controller,
            defaultSkillIds,
            episodeId,
            maxCredits,
            message,
            permissionMode,
            reasoningSupported,
            route.channelId,
            route.channelModelKey,
            route.logicalModelId,
            running,
            selectedModel,
            selectedSkillIds,
            sending,
            skills,
            state.busy,
            state.pending?.request,
            state.reasoningMode,
            state.sendMode,
            state.status,
        ],
    );

    const handledPrefills = useRef(new Set<string | number>());
    useEffect(() => {
        if (!bindings.takePrefill || state.status !== "ready") return;
        const request: AgentPanelPrefill | null = bindings.takePrefill();
        if (!request || handledPrefills.current.has(request.id)) return;
        handledPrefills.current.add(request.id);
        if (request.autoSubmit) {
            void submitPrompt(request.text, { preserveDraft: true, focusNodeIds: request.focusNodeIds }).then((ok) => {
                request.settle(ok ? (running ? "queued" : "sent") : false);
            });
        } else {
            updateDraft(draft.trim() ? `${draft}\n\n${request.text}` : request.text);
            request.settle("prefilled");
        }
    }, [bindings, draft, running, state.status, submitPrompt, updateDraft]);

    const openedRun = useRef("");
    useEffect(() => {
        if (!controller || !initialRunId || state.status !== "ready" || openedRun.current === initialRunId) return;
        openedRun.current = initialRunId;
        void controller.openRun(initialRunId).catch((cause) => {
            openedRun.current = "";
            void message.error(cause instanceof Error ? cause.message : "历史运行读取失败");
        });
    }, [controller, initialRunId, message, state.status]);

    useEffect(() => {
        if (!controller) return;
        return controller.subscribeEvent((event) => bindingsRef.current.onEvent?.(event));
    }, [controller]);

    useEffect(() => {
        if (!open) {
            setSkillsOpen(false);
            panelLayout.cancelGesture(panelRef.current!);
            const focused = document.activeElement;
            if (focused instanceof HTMLElement && panelRef.current?.contains(focused)) focused.blur();
        }
    }, [open, panelLayout]);

    const stop = async () => {
        if (!controller || stopping) return;
        setStopping(true);
        try {
            await controller.cancelRun();
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "停止失败");
        } finally {
            setStopping(false);
        }
    };

    const undoRun = async (runId: string) => {
        if (!bindingsRef.current.undoRun || undoingRunId) return;
        setUndoingRunId(runId);
        try {
            await bindingsRef.current.undoRun(runId);
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "撤销失败");
        } finally {
            setUndoingRunId("");
        }
    };

    const changePermission = async (mode: AgentPermissionMode) => {
        setPermissionMode(mode);
        if (!controller || !running) return;
        try {
            const changed = await controller.changePermission(mode);
            if (!changed) void message.warning("运行中权限未变更，将从下一次运行生效");
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "运行中权限变更失败");
        }
    };

    const decideApproval = async (decision: "approve" | "reject") => {
        if (!controller || approvalBusy) return;
        setApprovalBusy(true);
        try {
            if (decision === "approve") await bindingsRef.current.prepareSubmission?.();
            await controller.decideApproval(decision, {
                ...(approvalReason.trim() ? { reason: approvalReason.trim() } : {}),
                ...(decision === "approve" && approvalMediaSettings ? { mediaSettings: approvalMediaSettings } : {}),
            });
            setApprovalReason("");
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "审批失败");
        } finally {
            setApprovalBusy(false);
        }
    };

    const newConversation = () => {
        setView("chat");
        setDraft("");
        void controller?.newConversation();
    };

    const openConversation = (record: AgentConversationRecord) => {
        setView("chat");
        void controller?.openConversation(record.id);
    };

    const exportDebug = async () => {
        if (exporting) return;
        setExporting(true);
        try {
            const { saveAs } = await import("file-saver");
            const payload = {
                exportedAt: new Date().toISOString(),
                config,
                scope: context.scope,
                agentDefinitionId: context.agentDefinitionId,
                conversationId: state.activeConversationId,
                messages: state.messages,
                run: state.run,
                approval: state.approval,
                queue: state.queue,
                pending: state.pending,
                capabilities,
            };
            saveAs(new Blob([JSON.stringify(payload, null, 2)], { type: "application/json;charset=utf-8" }), `agent-debug-${state.activeConversationId}-${Date.now()}.json`);
        } catch (cause) {
            void message.error(cause instanceof Error ? cause.message : "导出失败");
        } finally {
            setExporting(false);
        }
    };

    const supportSkillSelection = () => capabilities.skills && agentPanelControlEnabled(config, "skills");
    const activeSkills = allowUserSkills ? skills : skills.filter((skill) => defaultSkillIds.has(skill.skillId));
    const userSelectableSkills = skills.filter((skill) => !defaultSkillIds.has(skill.skillId));
    const frozenUserSkillIds = (state.run?.skills || []).filter((skill) => skill.source === "user").map((skill) => skill.id);
    const selectedModelControl = agentPanelControlEnabled(config, "model");
    const reasoningControl = agentPanelControlEnabled(config, "reasoning");
    const contextControl = agentPanelControlEnabled(config, "contextUsage");
    const sendModeControl = agentPanelControlEnabled(config, "sendMode");

    const targetControls =
        bindings.targetControl || (config.extensions.includes("episode-target") && context.target?.episodeOptions?.length) ? (
            <div className="agent-panel-target flex flex-wrap items-end gap-2 rounded-xl border border-black/10 bg-white/70 px-3 py-2 text-xs">
                {bindings.targetControl}
                {config.extensions.includes("episode-target") && context.target?.episodeOptions?.length ? (
                    <>
                        <label className="min-w-0">
                            <span className="mb-1 block" style={{ color: theme.node.muted }}>
                                本轮剧集
                            </span>
                            <select className="max-w-56 rounded-md border border-black/15 bg-white px-2 py-1" value={episodeId} onChange={(event) => setEpisodeId(event.target.value)}>
                                {context.target.episodeOptions.map((option) => (
                                    <option key={option.id} value={option.id}>
                                        {option.label}
                                    </option>
                                ))}
                            </select>
                        </label>
                        <label>
                            <span className="mb-1 block" style={{ color: theme.node.muted }}>
                                积分上限
                            </span>
                            <InputNumber min={0.01} max={1_000_000} value={maxCredits} onChange={(value) => setMaxCredits(value ?? 20)} className="w-24" />
                        </label>
                    </>
                ) : null}
            </div>
        ) : null;

    const queue =
        state.queue || state.pending?.request ? (
            <>
                <AgentMessageQueueList
                    key={state.activeConversationId}
                    queue={state.queue}
                    error={state.error}
                    busy={sending || state.busy}
                    onEdit={(item, prompt) => controller?.editQueued(item, prompt) || Promise.resolve(false)}
                    onPromote={(item) => controller?.interjectQueued(item) || Promise.resolve(false)}
                    onRemove={(item) => controller?.cancelQueued(item) || Promise.resolve(false)}
                    onResume={() => controller?.updateQueue("resume") || Promise.resolve(false)}
                    onRetry={() => controller?.refreshQueue() || Promise.resolve()}
                />
                {state.pending?.request && !state.busy ? (
                    <div className="agent-message-queue agent-message-queue-error">
                        <span>发送结果待确认</span>
                        <button type="button" onClick={() => void controller?.retryPending()}>
                            重试发送
                        </button>
                    </div>
                ) : null}
            </>
        ) : null;

    const chat = (
        <motion.div key="chat" className="flex min-h-0 flex-1 flex-col" initial={{ opacity: 0, x: -12 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -12 }} transition={{ duration: reducedMotion ? 0 : 0.18 }}>
            <AgentPanelHeader
                theme={theme}
                title={config.title}
                hasMessages={state.messages.length > 0}
                statusLabel={statusLabel(state.run)}
                statusColor={statusColor}
                onNew={newConversation}
                onHistory={() => setView("history")}
                exporting={exporting}
                onExport={() => void exportDebug()}
                onSettings={() => setView("settings")}
                onResetLayout={panelLayout.reset}
                docked={docked}
                onToggleDock={bindings.dock?.available && !panelLayout.compact ? bindings.dock.onToggle : undefined}
                onCollapse={onClose}
                skillsOpen={skillsOpen}
                selectedSkillCount={selectedSkillIds.length}
                onSkills={() => supportSkillSelection() && setSkillsOpen(true)}
                skillsAvailable={capabilities.skills}
            />
            {state.run && state.connection !== "connected" ? (
                <div role="status" className="flex items-center justify-between gap-2 px-5 py-2 text-xs" style={{ color: theme.node.muted }}>
                    <span>{state.connection === "disconnected" ? "连接已断开，服务端任务可能仍在执行；运行记录已保留" : "正在连接并校准运行状态…"}</span>
                    {state.connection === "disconnected" ? (
                        <Button size="small" onClick={() => void controller?.reconnect()}>
                            重新连接
                        </Button>
                    ) : null}
                </div>
            ) : null}
            {!capabilities.availability.configured ? (
                <div role="status" className="px-5 py-2 text-xs" style={{ color: theme.node.muted }}>
                    {capabilities.availability.reason || capabilities.availability.queueReason}
                </div>
            ) : null}
            {state.status === "error" ? <Alert className="mx-4 mt-3" type="error" showIcon message={state.error || "会话加载失败"} /> : null}
            {activeRecord?.historyState ? <Alert className="mx-4 mt-3" type="warning" showIcon message={activeRecord.historyState.message} /> : null}
            {targetControls ? <div className="shrink-0 px-3 pt-3">{targetControls}</div> : null}
            <div data-agent-conversation className="agent-conversation thin-scrollbar min-h-0 flex-1 overflow-y-auto">
                {!state.messages.length && state.status === "ready" ? <AgentWelcome appearance={appearance} description={config.welcome} disabled={state.status !== "ready"} onDraftPrompt={(prompt) => updateDraft(prompt)} /> : null}
                <div className="agent-conversation-messages">
                    <AgentMessageList
                        messages={messages}
                        theme={theme}
                        references={references}
                        busy={state.busy || running}
                        streamingPaused={Boolean(state.approval)}
                        onFocusNode={(nodeId) => {
                            const reference = bindingsRef.current.references?.find((item) => item.nodeId === nodeId);
                            if (reference) bindingsRef.current.focusReference?.(reference);
                        }}
                        onReplyOption={question ? undefined : (text) => void submitPrompt(text)}
                        replyOptionsDisabled={sending || state.busy}
                    />
                    {state.approval ? (
                        <AgentApprovalCard
                            approval={state.approval}
                            theme={theme}
                            submitting={approvalBusy}
                            reason={approvalReason}
                            onReasonChange={setApprovalReason}
                            onApprove={() => void decideApproval("approve")}
                            onReject={() => void decideApproval("reject")}
                            accessory={
                                <>
                                    {bindings.approvalAccessory}
                                    {imageApproval ? <AgentImageApprovalSettings initial={imageApproval} value={approvalMediaSettings} onChange={setApprovalMediaSettings} theme={theme} disabled={approvalBusy} /> : null}
                                </>
                            }
                            onFocusNode={(nodeId) => {
                                const reference = bindingsRef.current.references?.find((item) => item.nodeId === nodeId);
                                if (reference) bindingsRef.current.focusReference?.(reference);
                            }}
                        />
                    ) : null}
                    {state.busy && !state.approval && state.messages.at(-1)?.role !== "assistant" ? <AgentWorkingMessage label="正在发送消息" /> : null}
                    {bindings.conversationFooter}
                    {undoOffer && bindings.undoRun && !state.busy ? <AgentRunUndoAction theme={theme} pending={undoingRunId === undoOffer} onUndo={() => void undoRun(undoOffer)} /> : null}
                </div>
            </div>
            {planItems.length ? <AgentPlanBar items={planItems} theme={theme} minimized={planMinimized} terminal={planTerminal} onToggle={() => setPlanMinimized((value) => !value)} /> : null}
            {mediaBatch ? (
                <AgentMediaBatchBar
                    batch={mediaBatch}
                    theme={theme}
                    onFocusNode={(nodeId) => {
                        const reference = bindingsRef.current.references?.find((item) => item.nodeId === nodeId);
                        if (reference) bindingsRef.current.focusReference?.(reference);
                    }}
                />
            ) : null}
            {question ? (
                <AgentQuestionBar
                    question={question}
                    theme={theme}
                    disabled={approvalBusy || state.busy || !state.queue?.awaitingQuestionId}
                    onAnswer={(label) => void submitPrompt(label, { mode: "interject", replyToQuestionId: state.queue?.awaitingQuestionId })}
                />
            ) : null}
            <AgentChatComposer
                visible={open}
                queuedMessages={queue}
                prompt={draft}
                attachments={[...(bindings.attachments || []), ...localAttachments]}
                onAddFiles={addAttachments}
                onRemoveAttachment={removeAttachment}
                onRetryAttachment={retryAttachment}
                blockReason={localAttachments.some((item) => item.status === "uploading" || item.status === "pending") ? "附件正在保存，请稍候" : bindings.attachmentBlockReason}
                addMenu={{
                    selectionCount: bindings.selectionCount || bindings.selectedNodeIds?.length || 0,
                    onReferenceSelection: () => {
                        const ids = bindingsRef.current.selectedNodeIds || [];
                        if (ids.length) updateDraft([draft.trim(), ...ids.map((id) => `@[node:${id}]`)].filter(Boolean).join("\n"));
                    },
                }}
                disabled={state.status !== "ready" || activeRecord?.historyState?.status === "unavailable"}
                sending={sending || state.busy}
                running={running}
                placeholder={config.placeholder}
                theme={theme}
                onPromptChange={updateDraft}
                onSubmit={(oppositeMode) => void submitPrompt(draft, { oppositeMode })}
                onStop={state.run?.id && running ? stop : undefined}
                stopping={stopping}
                sendMode={state.sendMode}
                sendModeSaving={sendModeSaving || !sendModeControl}
                onSendModeSettings={sendModeControl ? () => setView("settings") : undefined}
                references={references}
                slashSkills={activeSkills}
                includeAssetLibrary={false}
                submitAccessory={contextControl ? <AgentContextRing usage={state.contextUsage || emptyAgentContextUsage("")} /> : null}
                PromptEditor={bindings.promptEditor}
                left={
                    <AgentComposerControls
                        config={configStore}
                        selectedModel={selectedModel}
                        permissionMode={permissionMode}
                        theme={theme}
                        onModelChange={(model) => controller?.setModel(model)}
                        onPermissionChange={(mode) => void changePermission(mode)}
                        reasoningMode={state.reasoningMode as ThinkingRequest}
                        onReasoningModeChange={(value) => controller?.setReasoningMode(value)}
                        modelEnabled={selectedModelControl}
                        reasoningEnabled={reasoningControl}
                        permissionModes={capabilities.permissionModes}
                        modelFilter={modelFilter}
                    />
                }
            />
        </motion.div>
    );

    const content =
        view === "settings" ? (
            <motion.div key="settings" className="flex min-h-0 flex-1" initial={{ opacity: 0, x: 18 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: 18 }} transition={{ duration: reducedMotion ? 0 : 0.18 }}>
                <AgentPanelSettings
                    theme={theme}
                    config={configStore}
                    selectedModel={selectedModel}
                    onModelChange={(model) => controller?.setModel(model)}
                    reasoningMode={state.reasoningMode as ThinkingRequest}
                    onReasoningModeChange={(value) => controller?.setReasoningMode(value)}
                    permissionMode={permissionMode}
                    onPermissionChange={(mode) => void changePermission(mode)}
                    capabilities={capabilities}
                    sendMode={state.sendMode}
                    sendModeSaving={sendModeSaving}
                    sendModeError={sendModeError}
                    onSendModeChange={(mode) => void changeSendMode(mode)}
                    onSendModeRetry={() => void retrySendMode()}
                    skills={userSelectableSkills}
                    defaultSkills={defaultSkills}
                    selectedSkillIds={selectedSkillIds}
                    allowUserSkills={allowUserSkills}
                    onSkillToggle={(id) => controller?.setSkillIds(selectedSkillIds.includes(id) ? selectedSkillIds.filter((item) => item !== id) : [...selectedSkillIds, id])}
                    onOpenSkillLibrary={() => setSkillsOpen(true)}
                    onBack={() => setView("chat")}
                    extension={bindings.settingsExtension}
                    modelFilter={modelFilter}
                />
            </motion.div>
        ) : view === "history" ? (
            <motion.div key="history" className="flex min-h-0 flex-1" initial={{ opacity: 0, x: 18 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: 18 }} transition={{ duration: reducedMotion ? 0 : 0.18 }}>
                <AgentHistoryView
                    conversations={state.conversations}
                    activeConversationId={state.activeConversationId}
                    theme={theme}
                    onBack={() => setView("chat")}
                    onNew={newConversation}
                    onOpen={openConversation}
                    onDelete={(id) => void controller?.archiveConversation(id)}
                />
            </motion.div>
        ) : (
            chat
        );

    return (
        <>
            {!open ? <AgentLauncher theme={theme} statusColor={statusColor} approvalPending={Boolean(state.approval)} status={status} onOpen={onOpen} /> : null}
            {bindings.launcherNotice}
            <motion.aside
                ref={panelRef}
                initial={false}
                animate={open ? { opacity: 1, x: 0, y: 0 } : reducedMotion ? { opacity: 0 } : { opacity: 0, y: 12 }}
                transition={{ duration: reducedMotion ? 0 : 0.26, ease: [0.16, 1, 0.3, 1] }}
                className={`canvas-agent-panel fixed flex min-h-0 min-w-0 flex-col overflow-hidden${docked ? " is-docked" : ""}`}
                style={
                    {
                        ...panelLayout.style,
                        "--agent-surface-base": theme.node.panel,
                        "--agent-ink": theme.node.text,
                        "--agent-accent": theme.accent.primary,
                        "--agent-shadow-color": theme.spatial.shadow,
                    } as CSSProperties & Record<`--${string}`, string>
                }
                aria-label={config.title}
                aria-hidden={!open}
                inert={!open}
                data-agent-open={open}
                data-agent-docked={docked || undefined}
                data-agent-panel
                data-agent-compact={panelLayout.compact || undefined}
                data-popover-anchor-container
                data-canvas-no-zoom
                data-canvas-wheel-scroll
                {...panelLayout.pointerHandlers}
                onWheel={(event) => event.stopPropagation()}
            >
                {panelLayout.compact ? null : docked ? (
                    <div
                        role="separator"
                        tabIndex={0}
                        aria-orientation="vertical"
                        aria-label="调整 Agent 面板宽度"
                        aria-valuenow={bindings.dock?.width}
                        aria-valuemin={bindings.dock?.width ? 320 : undefined}
                        aria-valuemax={bindings.dock?.width ? 900 : undefined}
                        data-agent-resize="dock"
                        className="agent-panel-dock-resizer"
                        onKeyDown={panelLayout.onResizeKeyDown}
                    />
                ) : (
                    <>
                        <div data-agent-resize="north" className="agent-panel-resize-edge is-north" />
                        <div data-agent-resize="west" className="agent-panel-resize-edge is-west" />
                        <button type="button" aria-label="调整 Agent 面板大小" title="拖动调整宽高，也可用方向键调整" data-agent-resize="northwest" className="agent-panel-resize-corner" onKeyDown={panelLayout.onResizeKeyDown}>
                            <span className="size-3" aria-hidden="true" />
                        </button>
                    </>
                )}
                <div className="canvas-agent-panel-content flex min-h-0 flex-1 flex-col">
                    <AnimatePresence mode="wait" initial={false}>
                        {content}
                    </AnimatePresence>
                </div>
            </motion.aside>
            {open ? (
                <AgentSkillLibraryModal
                    open={skillsOpen}
                    theme={theme}
                    installedSkills={activeSkills}
                    marketSkills={marketSkills}
                    selectedSkillIds={selectedSkillIds}
                    frozenUserSkillIds={frozenUserSkillIds}
                    allowUserSkills={allowUserSkills}
                    globalDefaultSkillIds={defaultSkills.map((skill) => skill.skillId)}
                    globalDefaultSkills={defaultSkills}
                    categories={skillCategories}
                    category={skillTag}
                    search={skillSearch}
                    loading={skillsLoading}
                    hasMore={skillHasMore}
                    onClose={() => setSkillsOpen(false)}
                    onCategoryChange={setSkillTag}
                    onSearch={setSkillSearch}
                    onToggle={(id) => (allowUserSkills ? controller?.setSkillIds(selectedSkillIds.includes(id) ? selectedSkillIds.filter((item) => item !== id) : [...selectedSkillIds, id]) : undefined)}
                    onInstall={installSkill}
                    onLoadMore={loadMoreSkills}
                />
            ) : null}
        </>
    );
}
