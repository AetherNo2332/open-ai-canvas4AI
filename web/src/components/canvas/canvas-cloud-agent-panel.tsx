import { createContext, useCallback, useContext, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode, type Ref } from "react";
import { App, Button, Dropdown, Input, Popover } from "antd";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ArrowLeft, Check, ChevronRight, CircleDot, Clock3, Download, History, LoaderCircle, MessageSquarePlus, Settings2, ShieldCheck, Trash2, Sparkles, X } from "lucide-react";
import { saveAs } from "file-saver";
import { buildAgentDebugExport } from "@/lib/canvas/agent-debug-export";
import { markdownPlainText } from "@/lib/markdown-plain-text";
import { sameJsonData } from "@/lib/same-json-data";
import { applyAgentEvent, appendUniqueMessage } from "@/lib/canvas/cloud-agent-events";
import { refreshCloudAgentConversations, mergeCloudAgentConversations } from "@/services/cloud-agent-history";
import { agentPlanVisible, latestAgentPlanItems, latestAgentPlanTerminal, pendingAgentQuestion } from "@/lib/canvas/cloud-agent-plan";
import { continueAgentContextUsage, emptyAgentContextUsage, presentAgentContextUsage, reduceAgentContextUsage, sameAgentContextSelection, type AgentContextPhase, type AgentContextUsage, type AgentContextUsageView } from "@/lib/canvas/agent-context-usage";
import { nanoid } from "nanoid";

import { ModelPicker } from "@/components/model-picker";
import { FluidOrb } from "@/components/ui/fluid-orb";
import { cn } from "@/lib/utils";
import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { canvasThemes, type CanvasTheme } from "@/lib/canvas-theme";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { agentErrorPresentation, agentSubmissionErrorTitle } from "@/lib/canvas/agent-error-presentation";
import { effectiveAgentDefaultSkills } from "@/lib/canvas/agent-effective-skill-defaults";
import {
    archiveAgentConversation,
    cancelAgentRun,
    getAgentCapabilities,
    getAgentMessageQueue,
    listAgentSkillDefaults,
    getAgentRun,
    createAgentRun,
    decideAgentApproval,
    sendAgentConversationMessage,
    sendAgentMessage,
    subscribeAgentEvents,
    undoAgentRun,
    type AgentEvent,
    type AgentPermissionMode,
    type AgentReasoningMode,
    type AgentRun,
    type AgentSkillDefaultsSummary,
    type AgentSendMode,
    type AgentMessageReceipt,
} from "@/services/api/agent";
import { AGENT_APPROVAL_OPERATION_LABELS, agentApprovalPresentation } from "@/lib/canvas/agent-approval-presentation";
import { buildAgentFeedSegments } from "@/lib/canvas/agent-operation-feed";
import { agentApprovalMatchesSettings, agentImageApproval } from "@/lib/canvas/agent-media-approval";
import type { AgentMediaSettings } from "@/services/api/agent";
import { CanvasAgentImageApprovalSettings } from "./canvas-agent-image-approval-settings";
import { addSkill, listAddedSkills, listSkills, listSkillPresets, type Skill, type SkillCategory, type SkillPreset } from "@/services/api/skills";
import {
    clearCloudAgentPendingSubmission,
    cloudAgentConversationServerId,
    cloudAgentConversationTitle,
    loadCloudAgentConversations,
    loadCloudAgentPendingSubmission,
    saveCloudAgentConversations,
    saveCloudAgentPendingSubmission,
    type CloudAgentConversation,
    type CloudAgentPendingSubmission,
} from "@/services/cloud-agent-conversations";
import { logicalModelIDForConfig, modelOptionName, resolveModelRequestConfig, selectableModelsByCapability, useConfigStore, useEffectiveConfig } from "@/stores/use-config-store";
import { useActiveTheme } from "@/stores/canvas/use-canvas-theme-store";
import { useAppearanceStore } from "@/stores/use-appearance-store";
import { useUserStore } from "@/stores/use-user-store";
import { getActiveUserScope } from "@/lib/user-scope";
import { applyAgentCanvasPatches, refreshCanvasAfterAgent, saveRemoteUserDataNow } from "@/services/user-data-sync";
import { createAgentCanvasSync } from "@/services/agent-canvas-sync";
import { canvasRealtimeConnected } from "@/services/canvas-realtime";
import { buildSkillMentionReferences, resolveSkillMentions } from "@/services/skill-runtime";
import { notifyAgentApproval } from "@/lib/canvas/agent-browser-notifications";
import { agentRunUndoOffer, agentRunUndoneMessageId } from "@/lib/canvas/agent-run-undo";
import { ApiError } from "@/services/api/request";
import { API_ERROR_REASONS } from "@/services/api/error-codes";
import {
    AgentChatComposer,
    AgentChatMessage,
    AgentOperationFeed,
    AgentPlanBar,
    AgentQuestionBar,
    AgentReasoningFeed,
    AgentSceneCapsules,
    AgentRunUndoAction,
    AgentWorkingMessage,
    AGENT_SCENE_DEFS,
    type AgentSceneBucket,
    type CloudAgentChatMessage,
} from "./canvas-cloud-agent-chat-ui";
import { CanvasAgentSkillLibraryModal } from "./canvas-agent-skill-library-modal";
import { AgentSubagentList, type AgentSubagentAvatarItem } from "./canvas-agent-subagent-list";
import { AgentSubagentConversation } from "./canvas-agent-subagent-conversation";
import { CanvasCloudAgentSettings, agentPermissionLabel, agentPermissionMenuItems, agentPermissionVisual, type AgentContextKey } from "./canvas-cloud-agent-settings";
import { useAgentPanelLayout } from "./use-agent-panel-layout";
import { AgentWelcome } from "./canvas-agent-welcome";
import { DEFAULT_CANVAS_APPEARANCE, agentAvatarClipForState, agentAvatarStateForRunStatus, agentCopy, isAgentAvatarBundleManifest, type AgentAvatarBundleManifest } from "@/lib/canvas/agent-appearance";
import { agentAvatarBundleFileURL, live2DModelURL } from "@/services/api/appearance";
import { AgentAvatarVideo } from "./agent-avatar-video";
import { Live2DAvatar } from "./live2d-avatar";
import { createPortal } from "react-dom";
import "./canvas-cloud-agent.css";
import { createClientUUID } from "@/lib/client-id";
import { getAgentSubagentPolicy, listAgentSubagents, updateAgentSubagentPolicy, type AgentSubagentPolicy, type AgentSubagent } from "@/services/api/agent-subagent";
import { useAgentMessageQueue } from "./use-agent-message-queue";
import { AgentMessageQueueList } from "./canvas-agent-message-queue";

export type CanvasCloudAgentPanelHandle = {
    sendPrompt: (request: { canvasId: string; userScope: string; prompt: string; focusNodeIds?: string[] }) => Promise<"sent" | "queued" | false>;
};
type AgentSubmitOptions = { preserveDraft?: boolean; focusNodeIds?: string[]; mode?: AgentSendMode; oppositeMode?: boolean; replyToQuestionId?: string };
const AgentPanelVisibilityContext = createContext(true);

type CloudAgentPanelProps = {
    ref?: Ref<CanvasCloudAgentPanelHandle>;
    canvasId: string;
    nodeCount: number;
    selectedNodeIds: string[];
    references: CanvasResourceReference[];
    open: boolean;
    floating?: boolean;
    /** 是否显示悬浮唤起按钮;宿主另有入口(如导演台顶栏)时关闭。 */
    launcher?: boolean;
    prefillPrompt?: string;
    onOpen: () => void;
    onCollapse: () => void;
    onFocusNode?: (nodeId: string) => void;
};
type ApprovalState = { approvalId: string; detail: Record<string, unknown>; reason: string };
type AgentPanelView = "chat" | "history" | "settings";

export function CanvasCloudAgentPanel({ canvasId, nodeCount, selectedNodeIds, references, open, floating = false, launcher = true, prefillPrompt, ref, onOpen, onCollapse, onFocusNode }: CloudAgentPanelProps) {
    const userId = useUserStore((state) => state.user?.id);
    const userScope = getActiveUserScope();
    const subagentsAvailable = useUserStore((state) => state.features.agentSubagentsEnabled);
    const [subagentPolicy, setSubagentPolicy] = useState<AgentSubagentPolicy | null>(null);
    const [subagentPolicySaving, setSubagentPolicySaving] = useState(false);
    const [subagentPolicyError, setSubagentPolicyError] = useState("");
    const [subagentPolicyReload, setSubagentPolicyReload] = useState(0);
    const [loadedPolicyScope, setLoadedPolicyScope] = useState("");
    const policyScope = `${userId || ""}:${canvasId}:${subagentsAvailable}`;
    const policyScopeRef = useRef(policyScope);
    policyScopeRef.current = policyScope;
    const policyEpochRef = useRef({ scope: policyScope, epoch: 0 });
    if (policyEpochRef.current.scope !== policyScope) policyEpochRef.current = { scope: policyScope, epoch: policyEpochRef.current.epoch + 1 };
    useEffect(() => {
        const controller = new AbortController();
        setSubagentPolicy(null);
        setLoadedPolicyScope("");
        setSubagentPolicyError("");
        setSubagentPolicySaving(false);
        if (userId && subagentsAvailable)
            void getAgentSubagentPolicy(canvasId, controller.signal)
                .then((next) => {
                    if (!controller.signal.aborted && policyScopeRef.current === policyScope) {
                        setSubagentPolicy(next);
                        setLoadedPolicyScope(policyScope);
                    }
                })
                .catch(() => {
                    if (!controller.signal.aborted && policyScopeRef.current === policyScope) setSubagentPolicyError("子代理授权加载失败，请重试");
                });
        return () => controller.abort();
    }, [canvasId, userId, subagentsAvailable, policyScope, subagentPolicyReload]);
    const toggleSubagents = async () => {
        if (!subagentsAvailable || !subagentPolicy || loadedPolicyScope !== policyScope || subagentPolicySaving || running) return;
        const scope = policyScope;
        const epoch = policyEpochRef.current.epoch;
        const isCurrent = () => policyScopeRef.current === scope && policyEpochRef.current.epoch === epoch;
        setSubagentPolicySaving(true);
        setSubagentPolicyError("");
        try {
            const next = await updateAgentSubagentPolicy({ canvasId, enabled: !subagentPolicy.enabled, expectedRevision: subagentPolicy.revision });
            if (isCurrent()) setSubagentPolicy(next);
        } catch {
            if (!isCurrent()) return;
            setSubagentPolicyError("授权保存失败，已尝试读取最新设置，请确认后重试");
            try {
                const latest = await getAgentSubagentPolicy(canvasId);
                if (isCurrent()) setSubagentPolicy(latest);
            } catch {
                if (isCurrent()) setSubagentPolicy(null);
            }
        } finally {
            if (isCurrent()) setSubagentPolicySaving(false);
        }
    };
    const appearance = useAppearanceStore((state) => state.appearance.canvas) || DEFAULT_CANVAS_APPEARANCE;
    const theme = canvasThemes[useActiveTheme()];
    const config = useEffectiveConfig();
    const updateConfig = useConfigStore((state) => state.updateConfig);
    const reducedMotion = useReducedMotion();
    const [view, setView] = useState<AgentPanelView>("chat");
    const [workerAvailability, setWorkerAvailability] = useState<{ queueReason: string; reason: string }>();
    useEffect(() => {
        if (!open) return;
        let active = true;
        const refresh = () => { void getAgentCapabilities().then((result) => { if (active) setWorkerAvailability(result.availability); }).catch(() => {}); };
        refresh();
        const timer = window.setInterval(refresh, 15000);
        return () => { active = false; window.clearInterval(timer); };
    }, [open]);
    const [run, setRun] = useState<AgentRun | null>(null);
    const messageQueue = useAgentMessageQueue(userScope, canvasId, run ? run.conversationId || run.id : undefined);
    const currentRunIdRef = useRef(run?.id);
    currentRunIdRef.current = run?.id;
    const [contextUsage, setContextUsage] = useState<AgentContextUsage>(() => emptyAgentContextUsage(""));
    const [connectionStatus, setConnectionStatus] = useState<"connecting" | "connected" | "reconnecting" | "disconnected">("connecting");
    const [connectionEpoch, setConnectionEpoch] = useState(0);
    const [messages, setMessages] = useState<CloudAgentChatMessage[]>([]);
    const [prompt, setPrompt] = useState("");
    const lastPrefillPromptRef = useRef("");
    const [reasoningMode, setReasoningMode] = useState<AgentReasoningMode>("off");
    const [skills, setSkills] = useState<Skill[]>([]);
    const [selectedSkillIds, setSelectedSkillIds] = useState<string[]>([]);
    const [skillDefaults, setSkillDefaults] = useState<AgentSkillDefaultsSummary>({ count: 0, skills: [] });
    const globalDefaultSkills = useMemo(() => effectiveAgentDefaultSkills(run, skillDefaults), [run, skillDefaults]);
    const globalDefaultSkillIds = useMemo(() => globalDefaultSkills.map((skill) => skill.skillId), [globalDefaultSkills]);
    const [dynamicSubagents, setDynamicSubagents] = useState<AgentSubagentAvatarItem[]>([]);
    const [subagentDetails, setSubagentDetails] = useState<AgentSubagent[]>([]);
    const [selectedSubagentId, setSelectedSubagentId] = useState<string | null>(null);
    const subagentScope = `${userId || ""}:${canvasId}:${run?.id || ""}`;
    const [loadedSubagentScope, setLoadedSubagentScope] = useState("");
    const selectedSubagent = loadedSubagentScope === subagentScope ? subagentDetails.find((item) => item.id === selectedSubagentId) : undefined;
    const collaboratorsRef = useRef<HTMLDivElement>(null);
    const returnSubagentFocus = useRef<string | null>(null);
    useEffect(() => {
        if (!selectedSubagentId && returnSubagentFocus.current) {
            const button = Array.from(collaboratorsRef.current?.querySelectorAll<HTMLButtonElement>("[data-subagent-id]") || []).find((item) => item.dataset.subagentId === returnSubagentFocus.current);
            button?.focus();
            returnSubagentFocus.current = null;
        }
    }, [selectedSubagentId]);
    const [subagentDetailsError, setSubagentDetailsError] = useState("");
    const subagentListRequestRef = useRef(0);
    const acceptSubagents = (items: AgentSubagent[]) => {
        setLoadedSubagentScope(subagentScope);
        setSubagentDetails(items);
        setSubagentDetailsError("");
        setDynamicSubagents(
            items.map((item) => ({
                id: item.id,
                name: item.displayName,
                state: item.status === "completed" ? "done" : item.status === "failed" || item.status === "cancelled" ? "failed" : item.status === "queued" || item.status.startsWith("waiting") ? "waiting" : "running",
            })),
        );
    };
    useEffect(() => {
        const controller = new AbortController();
        const request = ++subagentListRequestRef.current;
        setDynamicSubagents([]);
        setSelectedSubagentId(null);
        setSubagentDetails([]);
        setSubagentDetailsError("");
        if (run?.id)
            void listAgentSubagents(run.id, controller.signal)
                .then((items) => {
                    if (!controller.signal.aborted && request === subagentListRequestRef.current) acceptSubagents(items);
                })
                .catch(() => {
                    if (!controller.signal.aborted && request === subagentListRequestRef.current) setSubagentDetailsError("子代理记录加载失败");
                });
        return () => controller.abort();
    }, [run?.id, subagentScope]);
    const allSubagents = loadedSubagentScope === subagentScope ? dynamicSubagents : [];
    const [marketSkills, setMarketSkills] = useState<Skill[]>([]);
    const [skillSearch, setSkillSearch] = useState("");
    const [debouncedSkillSearch, setDebouncedSkillSearch] = useState("");
    const [skillsLoading, setSkillsLoading] = useState(false);
    const [skillHasMore, setSkillHasMore] = useState(false);
    const [skillPage, setSkillPage] = useState(1);
    const [skillCategories, setSkillCategories] = useState<SkillCategory[]>([]);
    const [skillTag, setSkillTag] = useState("all");
    const [skillsOpen, setSkillsOpen] = useState(false);
    const [busy, setBusy] = useState(false);
    const [approvalSubmitting, setApprovalSubmitting] = useState(false);
    const [exporting, setExporting] = useState(false);
    const [stopping, setStopping] = useState(false);
    const [approval, setApproval] = useState<ApprovalState | null>(null);
    const [permissionMode, setPermissionMode] = useState<AgentPermissionMode>("auto");
    const [contextScope, setContextScope] = useState<AgentContextKey[]>(["canvas"]);
    const [maxCredits, setMaxCredits] = useState("200");
    const [maxGenerationTasks, setMaxGenerationTasks] = useState("0");
    const [maxVideoSeconds, setMaxVideoSeconds] = useState("0");
    const [conversations, setConversations] = useState<CloudAgentConversation[]>([]);
    const [activeConversationId, setActiveConversationId] = useState(() => nanoid());
    const activeHistoryState = conversations.find((conversation) => conversation.id === activeConversationId)?.historyState;
    const [loadedHistoryScope, setLoadedHistoryScope] = useState("");
    const historyHydrated = loadedHistoryScope === `${userScope}:${canvasId}`;
    const conversationsRef = useRef(conversations);
    conversationsRef.current = conversations;
    const autoRestoreRef = useRef(true);
    const acceptedSubmissionGenerationRef = useRef(0);
    const submissionControllerRef = useRef<AbortController | null>(null);
    useEffect(() => () => {
        submissionControllerRef.current?.abort();
        submissionControllerRef.current = null;
    }, [canvasId, userId]);
    const [pendingHydrated, setPendingHydrated] = useState(false);
    const [planMinimized, setPlanMinimized] = useState(false);
    const planItems = useMemo(() => latestAgentPlanItems(messages), [messages]);
    const planTerminal = useMemo(() => latestAgentPlanTerminal(messages), [messages]);
    const planVisible = agentPlanVisible(planItems);
    const pendingQuestion = useMemo(() => pendingAgentQuestion(messages), [messages]);
    const [scenePresets, setScenePresets] = useState<SkillPreset[]>([]);
    const [presetApplyingId, setPresetApplyingId] = useState("");
    const presetApplyingRef = useRef<string | null>(null);
    const panelLayout = useAgentPanelLayout();
    const panelRef = useRef<HTMLElement>(null);
    const resizeCaptureRef = useRef<{ element: HTMLDivElement; pointerId: number } | null>(null);
    useLayoutEffect(() => {
        if (open) return;
        setSkillsOpen(false);
        const capture = resizeCaptureRef.current;
        if (capture?.element.hasPointerCapture(capture.pointerId)) capture.element.releasePointerCapture(capture.pointerId);
        resizeCaptureRef.current = null;
        const focused = document.activeElement;
        if (focused instanceof HTMLElement && panelRef.current?.contains(focused)) focused.blur();
    }, [open]);
    const lastSeqRef = useRef(0);
    const lastSeqRunIdRef = useRef<string | null>(null);
    const canvasSyncRef = useRef<ReturnType<typeof createAgentCanvasSync> | null>(null);
    useEffect(() => {
        const sync = createAgentCanvasSync({
            canvasId,
            applyPatches: (patches) => applyAgentCanvasPatches(canvasId, patches),
            refresh: () => refreshCanvasAfterAgent(canvasId),
            realtimeConnected: () => canvasRealtimeConnected(canvasId),
            onError: (cause) => setMessages((current) => appendAgentError(current, `canvas-sync-${canvasId}`, cause, "画布同步冲突")),
        });
        canvasSyncRef.current = sync;
        return () => {
            sync.dispose();
            if (canvasSyncRef.current === sync) canvasSyncRef.current = null;
        };
    }, [canvasId]);
    const { message: appMessage } = App.useApp();
    const [undoingRunId, setUndoingRunId] = useState("");
    const undoOffer = useMemo(() => agentRunUndoOffer(run, messages), [run, messages]);
    const undoRun = async (runId: string) => {
        if (undoingRunId) return;
        setUndoingRunId(runId);
        try {
            await undoAgentRun(runId);
            if (currentRunIdRef.current === runId) setMessages((current) => appendUniqueMessage(current, { id: agentRunUndoneMessageId(runId), role: "system", text: "已撤销本次修改" }));
            // 实时通道在线时由 canvas_changed 增量合并；离线时退回现有的整份刷新。
            if (!canvasRealtimeConnected(canvasId)) canvasSyncRef.current?.reconcile();
        } catch (cause) {
            if (cause instanceof ApiError && cause.reason === API_ERROR_REASONS.agentUndoConflict) {
                appMessage.warning(cause.message || "Agent 修改过的内容之后又被编辑过，未执行撤销");
            } else {
                appMessage.error(cause instanceof Error && cause.message ? cause.message : "撤销失败，请稍后重试");
            }
        } finally {
            setUndoingRunId("");
        }
    };
    const skillPageRequestRef = useRef(false);
    const approvalRequestRef = useRef<string | null>(null);
    const pendingSubmission = useRef<CloudAgentPendingSubmission | null>(null);
    const submissionRequestRef = useRef(false);
    const conversationScope = `${userScope}:${canvasId}:${activeConversationId}`;
    const currentScope = useRef(conversationScope);
    currentScope.current = conversationScope;
    const historyStateRef = useRef({ activeConversationId, messages, run, prompt, busy });
    historyStateRef.current = { activeConversationId, messages, run, prompt, busy };
    const running = Boolean(run?.cleanupPending) || run?.status === "running" || run?.status === "queued" || run?.status === "waiting_approval";
    const selectedModel = useMemo(() => {
        const textModels = selectableModelsByCapability(config, "text");
        const preferred = config.textModel || config.model || "";
        return textModels.includes(preferred) ? preferred : textModels[0] || "";
    }, [config]);
    // 能力查询失败（渠道未配置、模型未选中、渠道结构不完整）不能让整个面板崩溃：
    // 面板是画布提示的入口，这里退化成"不支持推理"而不是抛 ReferenceError。
    const selectedTextCapability = useMemo(() => {
        try {
            return modelCapabilityConfigFor(config, selectedModel).text;
        } catch {
            return undefined;
        }
    }, [config, selectedModel]);
    const reasoningSupported = Boolean(selectedTextCapability?.thinking);
    useEffect(() => {
        if (!reasoningSupported && reasoningMode !== "off") setReasoningMode("off");
    }, [reasoningSupported, reasoningMode]);
    const installedSkills = useMemo(() => skills.filter((skill) => skill.isAdded), [skills]);
    const enabledSkills = useMemo(() => installedSkills.filter((skill) => selectedSkillIds.includes(skill.skillId)), [installedSkills, selectedSkillIds]);
    const installedSkillIds = useMemo(() => new Set(installedSkills.map((skill) => skill.skillId)), [installedSkills]);

    const [createdSkills, setCreatedSkills] = useState<Skill[]>([]);

    // 用户切换后重新加载，旧请求不得把其他账号的数据写回当前面板。
    useEffect(() => {
        let active = true;
        presetApplyingRef.current = null;
        setPresetApplyingId("");
        setScenePresets([]);
        listSkillPresets()
            .then((result) => {
                if (active) setScenePresets(result.presets || []);
            })
            .catch(() => {
                if (active) setScenePresets([]);
            });
        return () => {
            active = false;
        };
    }, [userId]);

    // 用户自建技能也要能出现在推荐里：官方种子库与剧典走「已装」，自建走 scope=created。
    useEffect(() => {
        let active = true;
        setCreatedSkills([]);
        listSkills({ scope: "created", pageSize: 50 })
            .then((result) => {
                if (active) setCreatedSkills(result.skills || []);
            })
            .catch((cause) => {
                if (active) setMessages((current) => appendAgentError(current, "created-skills-error", cause, "自建技能读取失败"));
            });
        return () => {
            active = false;
        };
    }, [userId]);

    // 场景分桶：把「常用 / 推荐配方 / 场景技能」收进同一个维度，一级只显示分类。
    // 常用度：自建 > 已收藏 > 已装，同级按市场热度降序。
    const sceneBuckets = useMemo<AgentSceneBucket[]>(() => {
        const merged = new Map<string, Skill>();
        for (const skill of [...installedSkills, ...createdSkills]) {
            if (!merged.has(skill.skillId)) merged.set(skill.skillId, skill);
        }
        const rank = (skill: Skill) => (skill.isOwner ? 0 : skill.isLike ? 1 : 2);
        const frequent = [...merged.values()].sort((a, b) => rank(a) - rank(b) || (b.addedCount || 0) - (a.addedCount || 0));
        const pool = frequent.slice(0, 24);
        const sceneOf = (skill: Skill) => skill.tag || "others";
        return AGENT_SCENE_DEFS.map((definition) => ({
            key: definition.key,
            label: definition.label,
            presets: definition.key === "frequent" ? [] : scenePresets.filter((preset) => preset.scene === definition.key),
            skills: definition.key === "frequent" ? frequent.slice(0, 8) : pool.filter((skill) => sceneOf(skill) === definition.key),
        }));
    }, [installedSkills, createdSkills, scenePresets]);

    const applyScenePreset = useCallback(
        async (preset: SkillPreset) => {
            if (running || busy || presetApplyingRef.current || !historyHydrated || !pendingHydrated) return;
            const scope = conversationScope;
            const account = getActiveUserScope();
            const token = crypto.randomUUID();
            const isCurrent = () => presetApplyingRef.current === token && currentScope.current === scope && getActiveUserScope() === account;
            const missing = preset.skillIds.filter((id) => !installedSkillIds.has(id));
            presetApplyingRef.current = token;
            setPresetApplyingId(preset.presetId);
            try {
                // 安装是持久写操作；任何失败都不能谎称整个配方已挂载。
                for (const id of missing) {
                    if (!isCurrent()) return;
                    await addSkill(id);
                    if (!isCurrent()) return;
                }
                const refreshed = await listAddedSkills();
                if (!isCurrent()) return;
                if (preset.skillIds.some((id) => !refreshed.skills.some((skill) => skill.skillId === id && skill.isAdded))) {
                    throw new Error("技能库未确认全部预设技能已安装，请刷新后重试");
                }
                setSkills(refreshed.skills);
                setSelectedSkillIds(preset.skillIds);
                setMessages((current) =>
                    appendUniqueMessage(current, {
                        id: `preset-${preset.presetId}-${Date.now()}`,
                        role: "system",
                        text: `已按「${preset.name}」挂上 ${preset.skillIds.length} 个技能${missing.length ? `（新装 ${missing.length} 个）` : ""}。${preset.rationale}`,
                    }),
                );
            } catch (cause) {
                if (isCurrent()) setMessages((current) => appendAgentError(current, `preset-${preset.presetId}`, cause, `「${preset.name}」挂载失败（已安装的技能仍在技能库中）`));
            } finally {
                if (presetApplyingRef.current === token) {
                    presetApplyingRef.current = null;
                    setPresetApplyingId("");
                }
            }
        },
        [busy, conversationScope, historyHydrated, installedSkillIds, pendingHydrated, running],
    );

    // 单个技能（含用户自建）挂载到本会话；未装的先补装，已挂的不重复追加。
    const applySingleSkill = useCallback(
        async (skill: Skill) => {
            if (running || busy || presetApplyingRef.current || !historyHydrated || !pendingHydrated) return;
            const scope = conversationScope;
            const account = getActiveUserScope();
            const token = crypto.randomUUID();
            const isCurrent = () => presetApplyingRef.current === token && currentScope.current === scope && getActiveUserScope() === account;
            presetApplyingRef.current = token;
            setPresetApplyingId(skill.skillId);
            try {
                if (!installedSkillIds.has(skill.skillId)) {
                    await addSkill(skill.skillId);
                    if (!isCurrent()) return;
                }
                const refreshed = await listAddedSkills();
                if (!isCurrent()) return;
                if (!refreshed.skills.some((item) => item.skillId === skill.skillId && item.isAdded)) {
                    throw new Error("技能库未确认该技能已安装，请刷新后重试");
                }
                setSkills(refreshed.skills);
                setSelectedSkillIds((current) => (current.includes(skill.skillId) ? current : [...current, skill.skillId]));
                setMessages((current) =>
                    appendUniqueMessage(current, {
                        id: `skill-${skill.skillId}-${Date.now()}`,
                        role: "system",
                        text: `已把「${skill.skillName}」挂到本会话。用哪张卡交给 Agent 按任务检索。`,
                    }),
                );
            } catch (cause) {
                if (isCurrent()) setMessages((current) => appendAgentError(current, `skill-${skill.skillId}`, cause, `「${skill.skillName}」挂载失败`));
            } finally {
                if (presetApplyingRef.current === token) {
                    presetApplyingRef.current = null;
                    setPresetApplyingId("");
                }
            }
        },
        [busy, conversationScope, historyHydrated, installedSkillIds, pendingHydrated, running],
    );
    const status = run?.status || "idle";
    const compacting = Boolean(run?.contextCompaction);
    const statusLabel = compacting
        ? "压缩上下文"
        : status === "waiting_approval"
          ? "等待审批"
          : status === "queued"
            ? run?.waitReason || "排队中"
            : status === "running" && run?.runtimePhase === "waiting_model"
              ? "等待模型响应"
              : status === "running" && run?.runtimePhase === "waiting_tool"
                ? "等待工具结果"
                : status === "running" && run?.runtimePhase === "waiting_resource"
                  ? run.waitReason || "等待运行容量"
                  : status === "running" && run?.runtimePhase === "waiting_compaction"
                    ? "压缩上下文"
                    : status === "running"
                      ? "运行中"
                      : status === "completed"
                        ? "已完成"
                        : status === "failed"
                          ? "异常"
                          : status === "cancelled"
                            ? "已停止"
                            : status === "rejected"
                              ? "已拒绝"
                              : "待命";
    const statusColor = status === "failed" ? "#e66b6b" : status === "rejected" || status === "cancelled" ? theme.node.muted : status === "waiting_approval" ? "#d6a24a" : status === "running" || status === "queued" ? "#69c29b" : theme.node.muted;

    useEffect(() => {
        const value = prefillPrompt?.trim();
        if (!value || value === lastPrefillPromptRef.current) return;
        lastPrefillPromptRef.current = value;
        setPrompt(value);
        setView("chat");
    }, [prefillPrompt]);

    useEffect(() => {
        if (!open || view !== "chat") setSkillsOpen(false);
    }, [open, view]);

    useEffect(() => {
        const timer = window.setTimeout(() => setDebouncedSkillSearch(skillSearch.trim()), 250);
        return () => window.clearTimeout(timer);
    }, [skillSearch]);

    useEffect(() => {
        let active = true;
        let requestSequence = 0;
        setSkills([]);
        setSkillDefaults({ count: 0, skills: [] });
        const refresh = () => {
            const sequence = ++requestSequence;
            void listAgentSkillDefaults()
                .then((result) => {
                    if (active && sequence === requestSequence) {
                        setSkillDefaults(result);
                        setMessages((current) => current.filter((message) => message.id !== "skill-defaults-load-error"));
                    }
                })
                .catch((cause) => {
                    if (active && sequence === requestSequence) setMessages((current) => appendAgentError(current, "skill-defaults-load-error", cause, "默认技能读取失败"));
                });
            void listAddedSkills()
                .then((result) => {
                    if (!active) return;
                    setSkills(result.skills);
                    setMessages((current) => current.filter((message) => message.id !== "skills-load-error"));
                })
                .catch((cause) => {
                    if (active) setMessages((current) => appendAgentError(current, "skills-load-error", cause, "技能库读取失败"));
                });
        };
        refresh();
        window.addEventListener("canvas-skills-changed", refresh);
        window.addEventListener("focus", refresh);
        return () => {
            active = false;
            window.removeEventListener("canvas-skills-changed", refresh);
            window.removeEventListener("focus", refresh);
        };
    }, [open, userId]);

    useEffect(() => {
        if (view !== "settings" && !skillsOpen) return;
        let active = true;
        setSkillsLoading(true);
        void listSkills({
            scope: "public",
            search: debouncedSkillSearch || undefined,
            tag: skillsOpen && skillTag !== "all" ? skillTag : undefined,
            pageSize: 20,
            sort: "popular",
        })
            .then((result) => {
                if (active) {
                    setMarketSkills(result.skills);
                    setSkillHasMore(result.hasMore);
                    setSkillPage(result.page);
                    if (result.categories.length > 0) setSkillCategories(result.categories);
                }
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
    }, [view, skillsOpen, debouncedSkillSearch, skillTag]);

    const loadMoreSkills = async () => {
        if (skillsLoading || skillPageRequestRef.current || !skillHasMore || skillSearch.trim() !== debouncedSkillSearch) return;
        skillPageRequestRef.current = true;
        setSkillsLoading(true);
        try {
            const result = await listSkills({
                scope: "public",
                search: debouncedSkillSearch || undefined,
                tag: skillsOpen && skillTag !== "all" ? skillTag : undefined,
                page: skillPage + 1,
                pageSize: 20,
                sort: "popular",
            });
            setMarketSkills((current) => [...current, ...result.skills.filter((skill) => !current.some((item) => item.skillId === skill.skillId))]);
            setSkillPage(result.page);
            setSkillHasMore(result.hasMore);
        } finally {
            skillPageRequestRef.current = false;
            setSkillsLoading(false);
        }
    };

    useEffect(() => {
        let active = true;
        setLoadedHistoryScope("");
        setPendingHydrated(false);
        pendingSubmission.current = null;
        setBusy(false);
        presetApplyingRef.current = null;
        setPresetApplyingId("");
        setConversations([]);
        setContextUsage(emptyAgentContextUsage(""));
        setRun(null);
        setMessages([]);
        setSelectedSkillIds([]);
        setApproval(null);
        setApprovalSubmitting(false);
        approvalRequestRef.current = null;
        setPrompt("");
        autoRestoreRef.current = true;
        submissionRequestRef.current = false;
        void loadCloudAgentConversations(canvasId, userScope)
            .then(async (document) => {
                if (!active) return;
                const current = document.conversations.find((conversation) => conversation.id === document.activeId) || document.conversations[0];
                setConversations(document.conversations);
                if (current) {
                    setActiveConversationId(current.id);
                    setMessages(current.messages);
                    setRun(current.run);
                    setContextUsage(current.contextUsage || emptyAgentContextUsage(current.run?.id || ""));
                    setPermissionMode(current.permissionMode);
                    setPrompt(current.draft || "");
                    setSelectedSkillIds(current.skillIds || []);
                    if (current.model) setModel(current.model);
                    const pending = await loadCloudAgentPendingSubmission(canvasId, current.id, userScope);
                    if (!active) return;
                    pendingSubmission.current = pending;
                    if (pending?.request) setPrompt((pending.conversationMessage ? current.draft : undefined) ?? pending.composerDraft ?? pending.request.prompt);
                } else {
                    setActiveConversationId(nanoid());
                    pendingSubmission.current = null;
                }
                setPendingHydrated(true);
            })
            .catch((cause) => {
                if (!active) return;
                setMessages((current) => appendAgentError(current, "history-error", cause, "对话恢复失败，已暂停发送；请重新打开对话核对"));
            })
            .finally(() => {
                if (active) setLoadedHistoryScope(`${userScope}:${canvasId}`);
            });
        return () => {
            active = false;
        };
    }, [canvasId, userId]);

    useEffect(() => {
        if (!historyHydrated || (!messages.length && !run && !prompt)) return;
        const now = new Date().toISOString();
        setConversations((current) => {
            const existing = current.find((conversation) => conversation.id === activeConversationId);
            const next: CloudAgentConversation = {
                ...existing,
                id: activeConversationId,
                serverConversationId: run?.conversationId || existing?.serverConversationId,
                lastEventSeq: run?.id === lastSeqRunIdRef.current ? lastSeqRef.current : existing?.lastEventSeq,
                draft: prompt,
                title: cloudAgentConversationTitle(messages),
                messages,
                run,
                contextUsage,
                model: selectedModel || undefined,
                permissionMode,
                skillIds: selectedSkillIds,
                createdAt: existing?.createdAt || now,
                updatedAt: now,
            };
            // Only the timestamp would differ; rewriting it re-saves the whole history.
            if (existing && current[0] === existing && sameJsonData({ ...existing, updatedAt: undefined }, { ...next, updatedAt: undefined })) return current;
            return [next, ...current.filter((conversation) => conversation.id !== activeConversationId)];
        });
    }, [activeConversationId, historyHydrated, messages, permissionMode, run, selectedModel, selectedSkillIds, contextUsage, prompt]);

    useEffect(() => {
        if (!historyHydrated) return;
        const timer = window.setTimeout(() => {
            void saveCloudAgentConversations(canvasId, activeConversationId, conversations, userScope);
        }, 180);
        return () => window.clearTimeout(timer);
    }, [activeConversationId, canvasId, conversations, historyHydrated, userScope]);

    useEffect(() => {
        if (!historyHydrated || !pendingHydrated || !userId) return;
        const controller = new AbortController();
        let loading = false;
        const refresh = async () => {
            if (loading || controller.signal.aborted) return;
            loading = true;
            try {
                const submissionGeneration = acceptedSubmissionGenerationRef.current;
                const remote = await refreshCloudAgentConversations({ canvasId, scope: userScope, signal: controller.signal, existing: conversationsRef.current });
                if (controller.signal.aborted || getActiveUserScope() !== userScope || submissionGeneration !== acceptedSubmissionGenerationRef.current) return;
                const state = historyStateRef.current;
                const protectedSubmission = state.busy || Boolean(pendingSubmission.current) || submissionRequestRef.current;
                const local = conversationsRef.current.map((conversation) => conversation.id === state.activeConversationId
                    ? { ...conversation, messages: state.messages, run: state.run, draft: state.prompt }
                    : conversation);
                const merged = mergeCloudAgentConversations(local, remote);
                // An unknown POST keeps both its request identity and local view
                // until the original key is explicitly reconciled.
                const refreshed = protectedSubmission ? merged.map((conversation) => conversation.id === state.activeConversationId ? local.find((entry) => entry.id === conversation.id)! : conversation) : merged;
                // The 4s poll usually returns an unchanged history. Keeping the current
                // references avoids re-rendering the chat and re-saving every message.
                const next = sameJsonData(refreshed, conversationsRef.current) ? conversationsRef.current : refreshed;
                conversationsRef.current = next;
                setConversations(next);
                const active = local.find((conversation) => conversation.id === state.activeConversationId);
                if (!protectedSubmission && active && remote.some((conversation) => conversation.archived && conversation.serverConversationId === (cloudAgentConversationServerId(active) || active.run?.id))) {
                    newConversation();
                    return;
                }
                const restored = !protectedSubmission && next.find((conversation) => conversation.id === state.activeConversationId);
                const discovered = !protectedSubmission && autoRestoreRef.current && !state.messages.length && !state.run && !state.prompt.trim() ? next.find((conversation) => conversation.run) || next[0] : undefined;
                const selected = restored || discovered;
                if (selected && (selected.run || selected.historyState?.status === "unavailable")) {
                    if (selected.id !== state.activeConversationId) {
                        currentScope.current = `${userScope}:${canvasId}:${selected.id}`;
                        setActiveConversationId(selected.id);
                        setPermissionMode(selected.permissionMode);
                        setSelectedSkillIds(selected.skillIds || []);
                    }
                    const changedRun = state.run?.id !== selected.run?.id;
                    if (changedRun) setContextUsage(selected.contextUsage || emptyAgentContextUsage(selected.run?.id || ""));
                    lastSeqRunIdRef.current = selected.run?.id || "";
                    lastSeqRef.current = Math.max(changedRun ? 0 : lastSeqRef.current, selected.lastEventSeq || 0);
                    setRun((current) => (sameJsonData(current, selected.run) ? current : selected.run));
                    setMessages((current) => (sameJsonData(current, selected.messages) ? current : selected.messages));
                    const snapshotApproval = selected.run?.approval;
                    setApproval((current) => {
                        const next = snapshotApproval && !snapshotApproval.decision
                            ? { approvalId: snapshotApproval.approvalId, detail: snapshotApproval, reason: current?.approvalId === snapshotApproval.approvalId ? current.reason : snapshotApproval.reason || "" }
                            : null;
                        return sameJsonData(current, next) ? current : next;
                    });
                }
            } catch (cause) {
                if (!controller.signal.aborted) setMessages((current) => appendAgentError(current, "cloud-history-error", cause, "云端对话同步失败，将保留本地记录并重试"));
            } finally {
                loading = false;
            }
        };
        void refresh();
        const timer = window.setInterval(() => void refresh(), 4_000);
        const wake = () => {
            void refresh();
            if (connectionStatus === "disconnected") setConnectionEpoch((value) => value + 1);
        };
        window.addEventListener("focus", wake);
        window.addEventListener("online", wake);
        return () => {
            controller.abort();
            window.clearInterval(timer);
            window.removeEventListener("focus", wake);
            window.removeEventListener("online", wake);
        };
    }, [canvasId, userId, userScope, historyHydrated, pendingHydrated]);

    useEffect(() => {
        if (!run?.id) return;
        const scope = conversationScope;
        let active = true;
        if (lastSeqRunIdRef.current !== run.id) {
            lastSeqRunIdRef.current = run.id;
            lastSeqRef.current = conversationsRef.current.find((conversation) => conversation.run?.id === run.id)?.lastEventSeq || 0;
        }
        const unsubscribe = subscribeAgentEvents(
            run.id,
            (event) => {
                if (!active || currentScope.current !== scope || currentRunIdRef.current !== run.id || event.runId !== run.id) return;
                // Only persisted agent events participate in the replay cursor.
                // Snapshot-derived UI events intentionally use seq=0.
                if (event.seq > 0) {
                    if (event.seq <= lastSeqRef.current) return;
                    if (event.seq > lastSeqRef.current + 1) canvasSyncRef.current?.reconcile();
                    lastSeqRef.current = event.seq;
                }
                if (event.type === "approval_requested" && event.seq > 0) {
                    void notifyAgentApproval({
                        approvalId: String(event.payload?.approvalId || "approval"),
                        summary: String(event.payload?.summary || event.payload?.reason || ""),
                    });
                }
                setMessages((current) => current.filter((item) => item.id !== `stream-error-${run.id}`));
                setContextUsage((current) => reduceAgentContextUsage(current, event));
                applyAgentEvent(event, setMessages, setRun, setApproval, setPrompt);
                if (["user_question", "run_completed", "run_failed", "run_cancelled"].includes(event.type)) void messageQueue.refresh();
                if (event.type.startsWith("subagent_") || ["run_completed", "run_failed", "run_cancelled"].includes(event.type)) {
                    const request = ++subagentListRequestRef.current;
                    void listAgentSubagents(run.id)
                        .then((items) => {
                            if (active && currentRunIdRef.current === run.id && request === subagentListRequestRef.current) acceptSubagents(items);
                        })
                        .catch(() => {
                            if (active && currentRunIdRef.current === run.id && request === subagentListRequestRef.current) setSubagentDetailsError("子代理记录更新失败");
                        });
                }
                canvasSyncRef.current?.receive(event);
            },
            {
                after: lastSeqRef.current,
                onConnectionChange: (status) => {
                    if (active && currentScope.current === scope && currentRunIdRef.current === run.id) setConnectionStatus(status);
                },
                onError: (cause) => {
                    if (!active || currentScope.current !== scope || currentRunIdRef.current !== run.id) return;
                    canvasSyncRef.current?.reconcile();
                    setMessages((current) => appendAgentError(current, `stream-error-${run.id}`, cause, "Agent 事件流已断开"));
                    // The observation channel failed, not the durable run. Keep
                    // identity and approval so reconnect/cancel/continue remain available.
                    setConnectionStatus("disconnected");
                },
            },
        );
        return () => {
            active = false;
            unsubscribe();
        };
    }, [run?.id, connectionEpoch, conversationScope]);

    useEffect(() => {
        if (!run?.id || connectionStatus !== "disconnected") return;
        const reconnect = () => setConnectionEpoch((value) => value + 1);
        const timer = window.setTimeout(reconnect, 4_000);
        window.addEventListener("online", reconnect);
        return () => { window.clearTimeout(timer); window.removeEventListener("online", reconnect); };
    }, [run?.id, connectionStatus]);

    const submit = async (override?: string, options: AgentSubmitOptions = {}): Promise<"sent" | "queued" | false> => {
        if (activeHistoryState?.status === "unavailable") return false;
        const value = (override ?? prompt).trim();
        if (!value || busy || submissionRequestRef.current || !historyHydrated || !pendingHydrated || currentScope.current !== conversationScope) return false;
        if (subagentsAvailable && (!subagentPolicy || loadedPolicyScope !== policyScope || subagentPolicy.canvasId !== canvasId || subagentPolicySaving || subagentPolicyError)) {
            setSubagentPolicyError("请先加载并确认子代理授权设置，再发送消息");
            return false;
        }
        const scope = conversationScope;
        submissionRequestRef.current = true;
        const controller = new AbortController();
        submissionControllerRef.current = controller;
        setBusy(true);
        let accepted = false;
        let outcome: "sent" | "queued" | false = false;
        try {
            const pending = pendingSubmission.current;
            // An ambiguous previous POST owns its body/key until reconciled.
            // Editing model settings or prompt must not silently create a new charge.
            if (pending?.request && pending.request.prompt !== value) throw new Error("上一条请求结果待确认，请先原样重试上一条消息，再发送新要求");
            if (!pending?.request) {
                const defaultMode = options.mode ?? await messageQueue.getMode();
                const mode = options.oppositeMode ? (defaultMode === "queue" ? "interject" : "queue") : defaultMode;
                const queueSnapshot = run ? messageQueue.queue || await getAgentMessageQueue(run.conversationId || run.id, controller.signal) : undefined;
                if (currentScope.current !== scope) return false;
                const capabilities = await getAgentCapabilities();
                if (currentScope.current !== scope) return false;
                setWorkerAvailability(capabilities.availability);
                if (!capabilities.availability.configured) throw new Error(capabilities.availability.reason);
                if (!capabilities.permissionModes.includes(permissionMode)) throw new Error("当前后端不支持所选 Agent 权限，请更新后端");
                if (selectedSkillIds.length && !capabilities.skills) throw new Error("当前后端尚未接入技能库");
                await saveRemoteUserDataNow(canvasId);
                if (currentScope.current !== scope) return false;
                const agentConfig = { ...config, model: selectedModel };
                const requestConfig = resolveModelRequestConfig(agentConfig, selectedModel);
                const logicalModelId = logicalModelIDForConfig(agentConfig);
                const input = {
                    canvasId,
                    prompt: value,
                    reasoningMode: reasoningSupported ? reasoningMode : "off",
                    model: modelOptionName(selectedModel) || undefined,
                    ...(logicalModelId ? { logicalModelId } : requestConfig.channelId ? { channelId: requestConfig.channelId, channelModelKey: modelOptionName(selectedModel) || undefined } : {}),
                    skillIds: [...new Set([...selectedSkillIds, ...resolveSkillMentions(value, installedSkills).map((skill) => skill.skillId)])],
                    permissionMode,
                    subagentEnabled: subagentsAvailable && subagentPolicy?.enabled === true,
                    contextScope,
                    focusNodeIds: options.focusNodeIds?.slice(0, 8) ?? (selectedNodeIds.length <= 8 ? selectedNodeIds : []),
                    budget: { maxCredits: positiveNumber(maxCredits), maxGenerationTasks: permissionMode === "read_only" ? 0 : Number(maxGenerationTasks), maxVideoSeconds: permissionMode === "read_only" ? 0 : Number(maxVideoSeconds) },
                };
                const replyToQuestionId = options.replyToQuestionId || (!options.preserveDraft && pendingQuestion ? queueSnapshot?.awaitingQuestionId : undefined);
                const conversationMessage = run ? { conversationId: run.conversationId || run.id, mode, expectedRunId: run.id, ...(mode === "interject" && replyToQuestionId ? { replyToQuestionId } : {}) } : undefined;
                const fingerprint = JSON.stringify({ scope, parent: run?.id, input, conversationMessage });
                if (pending && pending.fingerprint !== fingerprint) throw new Error("上一条请求尚未确认，请恢复原消息与设置后核对，不能覆盖原幂等记录");
                const key = pending?.key || createClientUUID();
                const next = { fingerprint, key, request: { ...input, idempotencyKey: key }, parentRunId: run?.id, messageId: `user-${key}`, ...(options.preserveDraft ? { composerDraft: prompt } : {}), ...(conversationMessage ? { conversationMessage } : {}) };
                // Persist before sending. A failed local save must not submit a request
                // whose recovery identity will disappear on reload.
                await saveCloudAgentPendingSubmission(canvasId, activeConversationId, next, userScope);
                if (currentScope.current !== scope) return false;
                pendingSubmission.current = next;
            }
            const submission = pendingSubmission.current!;
            const request = submission.request!;
            const nextMessages = submission.conversationMessage ? messages : appendUniqueMessage(messages, { id: submission.messageId || `user-${submission.key}`, role: "user", text: request.prompt });
            const now = new Date().toISOString();
            const existing = conversations.find((item) => item.id === activeConversationId);
            // Persist a discoverable conversation before POST as well as its key;
            // otherwise a reload of a brand-new chat can orphan the pending record.
            await saveCloudAgentConversations(canvasId, activeConversationId, [
                {
                    id: activeConversationId,
                    title: cloudAgentConversationTitle(nextMessages),
                    messages: nextMessages,
                    draft: historyStateRef.current.prompt,
                    run,
                    contextUsage,
                    model: selectedModel || undefined,
                    permissionMode,
                    skillIds: selectedSkillIds,
                    createdAt: existing?.createdAt || now,
                    updatedAt: now,
                },
                ...conversations.filter((item) => item.id !== activeConversationId),
            ], userScope);
            if (currentScope.current !== scope) return false;
            let nextRun: AgentRun | undefined;
            let receipt: AgentMessageReceipt | undefined;
            if (submission.conversationMessage) {
                const { conversationId, ...delivery } = submission.conversationMessage;
                receipt = await sendAgentConversationMessage(conversationId, { ...delivery, request }, controller.signal);
            } else {
                const result = submission.parentRunId ? await sendAgentMessage(submission.parentRunId, request, controller.signal) : await createAgentRun(request, controller.signal);
                nextRun = result.run;
            }
            accepted = true;
            outcome = receipt?.delivery === "queued" ? "queued" : receipt?.delivery === "cancelled" ? false : "sent";
            if (currentScope.current === scope) {
                // A read started before this accepted POST cannot describe the
                // current tip, even after several local turns have completed.
                acceptedSubmissionGenerationRef.current += 1;
                if (!options.preserveDraft) setPrompt((current) => current.trim() === value ? (outcome ? "" : value) : current);
                if (outcome === "sent") {
                    setMessages((current) => appendUniqueMessage(current, { id: receipt?.delivery === "interjected" ? receipt.messageId : submission.messageId || `user-${submission.key}`, role: "user", text: request.prompt, ...(receipt?.delivery === "interjected" ? { interjection: "sent" } : {}) }));
                } else if (!outcome) setMessages((current) => appendAgentError(current, `removed-${submission.key}`, new Error("这条待发消息已被移除"), "消息已移除"));
            }
            await clearCloudAgentPendingSubmission(canvasId, activeConversationId, userScope);
            if (currentScope.current === scope) pendingSubmission.current = null;
            if (currentScope.current === scope && receipt?.delivery === "started" && receipt.runId) {
                try { nextRun = (await getAgentRun(receipt.runId, controller.signal)).run; }
                catch (cause) { if (currentScope.current === scope) setMessages((current) => appendAgentError(current, `run-read-${receipt!.messageId}`, cause, "消息已发送，正在同步运行")); }
            }
            if (currentScope.current === scope) {
                if (nextRun) {
                    currentRunIdRef.current = nextRun.id;
                    setContextUsage((current) => (submission.parentRunId && sameAgentContextSelection(run, request) ? continueAgentContextUsage(current, nextRun!.id) : emptyAgentContextUsage(nextRun!.id)));
                    setRun(nextRun);
                }
                await messageQueue.refresh();
            }
        } catch (cause) {
            if (currentScope.current !== scope) return false;
            if (!accepted) {
                const status = (cause as { status?: number }).status;
                if ((status && [400, 401, 403, 404, 422].includes(status)) || ["agent_conversation_stale_parent", "agent_question_pending", "agent_question_answer_pending", "agent_question_stale"].includes((cause as { reason?: string }).reason || "")) {
                    // These admission responses explicitly rejected the write.
                    // Transport errors and conflicts retain the pending identity.
                    try {
                        await clearCloudAgentPendingSubmission(canvasId, activeConversationId, userScope);
                        pendingSubmission.current = null;
                    } catch (storageError) {
                        setMessages((current) => appendAgentError(current, `pending-storage-${activeConversationId}`, storageError, "提交记录更新失败"));
                    }
                }
            }
            setMessages((current) => appendAgentError(current, `submit-error-${activeConversationId}`, cause, agentSubmissionErrorTitle(cause, accepted)));
            void messageQueue.refresh();
        } finally {
            if (submissionControllerRef.current === controller) {
                submissionControllerRef.current = null;
                submissionRequestRef.current = false;
            }
            if (currentScope.current === scope) setBusy(false);
        }
        return accepted && currentScope.current === scope ? outcome : false;
    };

    useImperativeHandle(ref, () => ({
        sendPrompt: async (request) => {
            if (request.canvasId !== canvasId || request.userScope !== userScope || getActiveUserScope() !== userScope) return false;
            setView("chat");
            return submit(request.prompt, { preserveDraft: true, focusNodeIds: request.focusNodeIds });
        },
    }));
    const stop = async () => {
        const activeRun = run;
        if (!activeRun?.id || stopping) return;
        setStopping(true);
        try {
            await cancelAgentRun(activeRun.id);
            await messageQueue.refresh();
            const snapshot = await getAgentRun(activeRun.id, AbortSignal.timeout(5_000));
            if (currentScope.current === conversationScope) {
                setRun((current) => (current?.id === activeRun.id ? snapshot.run : current));
                if (!snapshot.run.approval) setApproval(null);
                setConnectionEpoch((value) => value + 1);
            }
        } catch (cause) {
            if (currentScope.current === conversationScope) {
                // An ambiguous cancellation is not proof of a terminal run. Keep
                // the run and approval until the server confirms their state.
                setConnectionEpoch((value) => value + 1);
                setMessages((current) => appendAgentError(current, `cancel-${activeRun.id}`, cause, "取消结果未确认，正在重新核对运行状态"));
            }
        } finally {
            setStopping(false);
        }
    };

    const submitApproval = async (decision: "approve" | "reject", mediaSettings?: AgentMediaSettings) => {
        if (!run || !approval || connectionStatus !== "connected" || approvalRequestRef.current === approval.approvalId) return;
        const runId = run.id;
        const approvalId = approval.approvalId;
        const scope = conversationScope;
        approvalRequestRef.current = approvalId;
        setApprovalSubmitting(true);
        try {
            if (decision === "approve") await saveRemoteUserDataNow(canvasId);
            if (currentScope.current !== scope) return;
            await decideAgentApproval(runId, approvalId, decision, approval.reason, AbortSignal.timeout(15_000), mediaSettings);
            if (currentScope.current === scope) {
                setApproval((current) => (current?.approvalId === approvalId ? null : current));
                setRun((current) =>
                    current?.id === runId && current.status === "waiting_approval" && (!current.approval || current.approval.approvalId === approvalId)
                        ? { ...current, status: decision === "reject" ? "rejected" : "running", approval: undefined }
                        : current,
                );
            }
        } catch (cause) {
            if (currentScope.current !== scope) return;
            // A timed-out response is ambiguous: query the durable decision before
            // asking the user to retry. The backend treats identical decisions idempotently.
            try {
                const snapshot = await getAgentRun(runId, AbortSignal.timeout(5_000));
                const decided = snapshot.run.events?.some(
                    (event) => event.type === "approval_decided" && event.payload.approvalId === approvalId && event.payload.decision === decision && (!mediaSettings || agentApprovalMatchesSettings(event.payload.arguments, mediaSettings)),
                );
                if (decided) {
                    setApproval((current) => (current?.approvalId === approvalId ? null : current));
                    setRun(snapshot.run);
                    return;
                }
            } catch {
                // Preserve the pending approval so the same decision can be retried.
            }
            setMessages((current) => appendAgentError(current, `approval-error-${Date.now()}`, cause, "审批未确认，请重试"));
        } finally {
            if (approvalRequestRef.current === approvalId) approvalRequestRef.current = null;
            if (currentScope.current === scope) setApprovalSubmitting(false);
        }
    };

    const installSkill = async (skill: Skill) => {
        if (skill.isAdded) return;
        try {
            const result = await addSkill(skill.skillId);
            setSkills((current) => [...current.filter((item) => item.skillId !== skill.skillId), result.skill]);
            setMarketSkills((current) => current.map((item) => (item.skillId === skill.skillId ? result.skill : item)));
            setSelectedSkillIds((current) => (current.includes(skill.skillId) ? current : [...current, skill.skillId]));
        } catch (cause) {
            setMessages((current) => appendAgentError(current, `skill-error-${Date.now()}`, cause, "添加 Skill 失败"));
        }
    };

    const setModel = (model: string) => {
        updateConfig("textModel", model);
        updateConfig("model", model);
    };

    const newConversation = () => {
        const id = nanoid();
        autoRestoreRef.current = false;
        currentScope.current = `${userScope}:${canvasId}:${id}`;
        submissionControllerRef.current?.abort();
        submissionControllerRef.current = null;
        submissionRequestRef.current = false;
        presetApplyingRef.current = null;
        setPresetApplyingId("");
        setPendingHydrated(true);
        setBusy(false);
        pendingSubmission.current = null;
        approvalRequestRef.current = null;
        setApprovalSubmitting(false);
        setActiveConversationId(id);
        setContextUsage(emptyAgentContextUsage(""));
        setRun(null);
        setMessages([]);
        setSelectedSkillIds([]);
        setPrompt("");
        setApproval(null);
        lastSeqRef.current = 0;
        setView("chat");
    };

    const openConversation = (conversation: CloudAgentConversation) => {
        autoRestoreRef.current = false;
        currentScope.current = `${userScope}:${canvasId}:${conversation.id}`;
        submissionControllerRef.current?.abort();
        submissionControllerRef.current = null;
        submissionRequestRef.current = false;
        presetApplyingRef.current = null;
        setPresetApplyingId("");
        setPendingHydrated(false);
        setBusy(false);
        pendingSubmission.current = null;
        approvalRequestRef.current = null;
        setApprovalSubmitting(false);
        setActiveConversationId(conversation.id);
        setContextUsage(conversation.contextUsage || emptyAgentContextUsage(conversation.run?.id || ""));
        setRun(conversation.run);
        setMessages(conversation.messages);
        setPermissionMode(conversation.permissionMode);
        setSelectedSkillIds(conversation.skillIds || []);
        setApproval(null);
        setPrompt(conversation.draft || "");
        if (conversation.model) setModel(conversation.model);
        setView("chat");
        void loadCloudAgentPendingSubmission(canvasId, conversation.id, userScope)
            .then((pending) => {
                if (currentScope.current === `${userScope}:${canvasId}:${conversation.id}`) {
                    pendingSubmission.current = pending;
                    setPendingHydrated(true);
                    if (pending?.request) setPrompt((pending.conversationMessage ? conversation.draft : undefined) ?? pending.composerDraft ?? pending.request.prompt);
                }
            })
            .catch((cause) => {
                if (currentScope.current === `${userScope}:${canvasId}:${conversation.id}`) setMessages((current) => appendAgentError(current, `pending-${conversation.id}`, cause, "待确认请求读取失败"));
            });
    };
    const deleteConversation = async (id: string) => {
        const conversation = conversationsRef.current.find((item) => item.id === id);
        if (!conversation) return;
        const scope = userScope;
        try {
            const serverId = cloudAgentConversationServerId(conversation) || conversation.run?.id;
            if (serverId) await archiveAgentConversation(serverId);
            if (getActiveUserScope() !== scope || !currentScope.current.startsWith(`${scope}:${canvasId}:`)) return;
            // Invalidate directory responses that started before the archive.
            acceptedSubmissionGenerationRef.current++;
            const next = conversationsRef.current.filter((item) => item.id !== id);
            conversationsRef.current = next;
            setConversations(next);
            const active = historyStateRef.current.activeConversationId;
            if (id === active) newConversation();
            await saveCloudAgentConversations(canvasId, id === active ? null : active, next, scope);
        } catch (cause) {
            if (getActiveUserScope() === scope) setMessages((current) => appendAgentError(current, `archive-${id}`, cause, "删除对话失败"));
        }
    };

    const panel = (
        <>
            {!open && launcher ? <AgentLauncher theme={theme} statusColor={statusColor} approvalPending={Boolean(approval)} reducedMotion={Boolean(reducedMotion)} status={status} onOpen={onOpen} /> : null}
            <div className="relative flex min-h-0 shrink-0" style={{ width: open && !floating ? panelLayout.width : 0, flexBasis: open && !floating ? panelLayout.width : 0 }}>
                    <motion.aside
                        ref={panelRef}
                        initial={false}
                        animate={{ opacity: open ? 1 : 0, x: open || reducedMotion ? 0 : "100%" }}
                        transition={{ duration: reducedMotion || panelLayout.resizing ? 0 : 0.42, ease: [0.22, 1, 0.36, 1] }}
                        className="canvas-agent-panel absolute inset-y-0 right-0 flex min-h-0 min-w-0 flex-col overflow-hidden"
                        data-agent-floating={floating || undefined}
                        style={{ width: panelLayout.width, "--agent-panel-width": `${panelLayout.width}px`, "--agent-surface-base": theme.node.panel, "--agent-ink": theme.node.text, "--agent-accent": theme.accent.primary, "--agent-shadow-color": theme.spatial.shadow } as CSSProperties & Record<`--${string}`, string>}
                        aria-label="Agent 工作台"
                        aria-hidden={!open}
                        inert={!open}
                        data-agent-open={open}
                        data-agent-compact={panelLayout.compact || undefined}
                        data-canvas-no-zoom
                        data-canvas-wheel-scroll
                        onWheel={(event) => event.stopPropagation()}
                    >
                        {!panelLayout.compact ? <div
                            role="separator"
                            tabIndex={0}
                            aria-label="调整 Agent 侧栏宽度"
                            aria-orientation="vertical"
                            aria-valuemin={panelLayout.minWidth}
                            aria-valuemax={panelLayout.maxWidth}
                            aria-valuenow={panelLayout.width}
                            aria-valuetext={`${panelLayout.width} 像素`}
                            title="拖动调整宽度，左右方向键微调，双击恢复默认"
                            className="canvas-agent-width-handle"
                            {...panelLayout.resizeHandlers}
                            onPointerDown={(event) => {
                                panelLayout.resizeHandlers.onPointerDown(event);
                                if (event.currentTarget.hasPointerCapture(event.pointerId)) resizeCaptureRef.current = { element: event.currentTarget, pointerId: event.pointerId };
                            }}
                            onLostPointerCapture={(event) => {
                                resizeCaptureRef.current = null;
                                panelLayout.resizeHandlers.onLostPointerCapture(event);
                            }}
                        /> : null}
                        {/* 布局宽度即时切换，内容仅随侧栏平移；容器查询始终使用真实宽度。 */}
                        <div className="canvas-agent-panel-content flex min-h-0 flex-1 flex-col">
                            <AnimatePresence mode="wait" initial={false}>
                                {view === "settings" ? (
                                    <motion.div key="settings" className="flex min-h-0 flex-1" initial={{ opacity: 0, x: 18 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: 18 }} transition={{ duration: reducedMotion ? 0 : 0.18 }}>
                                        <CanvasCloudAgentSettings
                                            key={`${userId}:${canvasId}:${open}`}
                                            sendMode={messageQueue.mode}
                                            sendModeSaving={messageQueue.savingMode}
                                            sendModeError={messageQueue.settingsError}
                                            onSendModeChange={messageQueue.changeMode}
                                            onSendModeRetry={messageQueue.retry}
                                            theme={theme}
                                            config={config}
                                            selectedModel={selectedModel}
                                            permissionMode={permissionMode}
                                            contextScope={contextScope}
                                            nodeCount={nodeCount}
                                            installedSkills={installedSkills}
                                            marketSkills={marketSkills}
                                            selectedSkillIds={selectedSkillIds}
                                            skillSearch={skillSearch}
                                            skillsLoading={skillsLoading}
                                            skillHasMore={skillHasMore}
                                            maxCredits={maxCredits}
                                            maxGenerationTasks={maxGenerationTasks}
                                            maxVideoSeconds={maxVideoSeconds}
                                            onBack={() => setView("chat")}
                                            onModelChange={setModel}
                                            onPermissionChange={setPermissionMode}
                                            reasoningMode={reasoningMode}
                                            onReasoningModeChange={setReasoningMode}
                                            canvasId={canvasId}
                                            subagents={
                                                subagentsAvailable
                                                    ? {
                                                          enabled: loadedPolicyScope === policyScope && subagentPolicy?.enabled === true,
                                                          disabled: !subagentPolicy || loadedPolicyScope !== policyScope || subagentPolicySaving || running,
                                                          saving: subagentPolicySaving,
                                                          error: subagentPolicyError,
                                                          onToggle: () => void toggleSubagents(),
                                                          onReload: () => setSubagentPolicyReload((value) => value + 1),
                                                      }
                                                    : undefined
                                            }
                                            onContextToggle={(value) => setContextScope((current) => (current.includes(value) ? current.filter((item) => item !== value) : [...current, value]))}
                                            onSkillSearch={setSkillSearch}
                                            onSkillToggle={(id) => setSelectedSkillIds((current) => (current.includes(id) ? current.filter((item) => item !== id) : [...current, id]))}
                                            onSkillInstall={installSkill}
                                            onLoadMoreSkills={loadMoreSkills}
                                            onMaxCreditsChange={setMaxCredits}
                                            onMaxGenerationTasksChange={setMaxGenerationTasks}
                                            onMaxVideoSecondsChange={setMaxVideoSeconds}
                                        />
                                    </motion.div>
                                ) : view === "history" ? (
                                    <motion.div key="history" className="flex min-h-0 flex-1" initial={{ opacity: 0, x: 18 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: 18 }} transition={{ duration: reducedMotion ? 0 : 0.18 }}>
                                        <AgentHistory conversations={conversations} activeConversationId={activeConversationId} theme={theme} onBack={() => setView("chat")} onNew={newConversation} onOpen={openConversation} onDelete={deleteConversation} />
                                    </motion.div>
                                ) : selectedSubagent ? (
                                    <AgentSubagentConversation
                                        key={selectedSubagent.id}
                                        agent={selectedSubagent}
                                        theme={theme}
                                        onBack={() => {
                                            returnSubagentFocus.current = selectedSubagent.id;
                                            setSelectedSubagentId(null);
                                        }}
                                    />
                                ) : (
                                    <motion.div key="chat" className="flex min-h-0 flex-1 flex-col" initial={{ opacity: 0, x: -12 }} animate={{ opacity: 1, x: 0 }} exit={{ opacity: 0, x: -12 }} transition={{ duration: reducedMotion ? 0 : 0.18 }}>
                                        <AgentHeader
                                            theme={theme}
                                            hasMessages={messages.length > 0}
                                            statusLabel={statusLabel}
                                            statusColor={statusColor}
                                            nodeCount={nodeCount}
                                            onNew={newConversation}
                                            onHistory={() => setView("history")}
                                            exporting={exporting}
                                            onExport={() => {
                                                if (exporting) return;
                                                setExporting(true);
                                                void (async () => {
                                                    let snapshot = run;
                                                    let snapshotError: string | undefined;
                                                    if (run?.id) {
                                                        try {
                                                            snapshot = (await getAgentRun(run.id, AbortSignal.timeout(15_000))).run;
                                                        } catch (cause) {
                                                            snapshotError = cause instanceof Error ? cause.message : String(cause);
                                                        }
                                                    }
                                                    const text = buildAgentDebugExport({ canvasId, conversationId: activeConversationId, messages, run: snapshot, approval, model: selectedModel, permissionMode, snapshotError });
                                                    saveAs(new Blob([text], { type: "application/json;charset=utf-8" }), `agent-debug-${activeConversationId}-${Date.now()}.json`);
                                                })()
                                                    .catch((cause) => setMessages((current) => appendAgentError(current, `export-${Date.now()}`, cause, "导出失败")))
                                                    .finally(() => setExporting(false));
                                            }}
                                            onSettings={() => setView("settings")}
                                            onCollapse={onCollapse}
                                            skillsOpen={skillsOpen}
                                            selectedSkillCount={selectedSkillIds.length}
                                            onSkills={() => setSkillsOpen(true)}
                                        />
                                        {run && connectionStatus !== "connected" ? (
                                            <div role="status" className="flex items-center justify-between gap-2 px-5 py-2 text-xs" style={{ color: theme.node.muted }}>
                                                <span>{connectionStatus === "disconnected" ? "连接已断开，服务端任务可能仍在执行；运行记录已保留" : "正在连接并校准运行状态…"}</span>
                                                {connectionStatus === "disconnected" ? (
                                                    <Button size="small" onClick={() => setConnectionEpoch((value) => value + 1)}>
                                                        重新连接
                                                    </Button>
                                                ) : null}
                                            </div>
                                        ) : null}
                                        {workerAvailability?.queueReason ? (
                                            <div role="status" className="px-5 py-2 text-xs text-foreground/70">{workerAvailability.reason}</div>
                                        ) : null}
                                        {activeHistoryState ? (
                                            <div role="status" className="px-5 py-2 text-xs" style={{ color: theme.node.muted }}>{activeHistoryState.message}</div>
                                        ) : null}
                                        <AgentConversation
                                            key={activeConversationId}
                                            theme={theme}
                                            messages={messages}
                                            onFocusNode={onFocusNode}
                                            references={[...references, ...buildSkillMentionReferences(installedSkills)]}
                                            busy={busy || running}
                                            approval={approval}
                                            nodeCount={nodeCount}
                                            approvalSubmitting={approvalSubmitting || connectionStatus !== "connected"}
                                            onChooseSkill={() => setSkillsOpen(true)}
                                            onDraftPrompt={(draft) => setPrompt((current) => (current.trim() ? `${current}\n\n${draft}` : draft))}
                                            onApprovalReasonChange={(reason) => setApproval((current) => (current ? { ...current, reason } : current))}
                                            onApprove={(settings) => void submitApproval("approve", settings)}
                                            onReject={() => void submitApproval("reject")}
                                            footer={undoOffer && !busy ? <AgentRunUndoAction theme={theme} pending={undoingRunId === undoOffer} onUndo={() => void undoRun(undoOffer)} /> : null}
                                        />
                                        {planVisible ? (
                                            <AgentPlanBar
                                                items={planItems}
                                                theme={theme}
                                                minimized={planMinimized}
                                                terminal={planTerminal || Boolean(run && ["completed", "failed", "cancelled", "rejected"].includes(run.status))}
                                                onToggle={() => setPlanMinimized((value) => !value)}
                                            />
                                        ) : null}
                                        {allSubagents.length > 0 ? (
                                            <div ref={collaboratorsRef} className="shrink-0">
                                                <AgentSubagentList items={allSubagents} theme={theme} onSelect={setSelectedSubagentId} />
                                            </div>
                                        ) : null}
                                        {subagentDetailsError ? (
                                            <div role="alert" className="px-4 py-2 text-xs">
                                                {subagentDetailsError}
                                            </div>
                                        ) : null}
                                        {subagentPolicyError ? (
                                            <div role="alert" className="flex items-center justify-between gap-2 px-4 py-2 text-xs">
                                                <span>{subagentPolicyError}</span>
                                                <Button size="small" onClick={() => setView("settings")}>
                                                    打开 Agent 设置
                                                </Button>
                                            </div>
                                        ) : null}
                                        {historyHydrated && !messages.some((message) => message.role === "user" || message.role === "assistant") && !run ? (
                                            <AgentSceneCapsules
                                                buckets={sceneBuckets}
                                                installedIds={installedSkillIds}
                                                theme={theme}
                                                disabled={Boolean(busy || running || !pendingHydrated || presetApplyingId)}
                                                onPick={(preset) => void applyScenePreset(preset)}
                                                onPickSkill={(skill) => void applySingleSkill(skill)}
                                            />
                                        ) : null}
                                        {pendingQuestion ? <AgentQuestionBar question={pendingQuestion} theme={theme} disabled={approvalSubmitting || busy || !messageQueue.queue?.awaitingQuestionId} onAnswer={(label) => void submit(label, { mode: "interject", replyToQuestionId: messageQueue.queue?.awaitingQuestionId })} /> : null}
                                        {/* 只重置浮层控件；输入器及待发队列保留 DOM 和未提交的编辑。 */}
                                        <AgentChatComposer
                                            visible={open}
                                            queuedMessages={<>
                                                <AgentMessageQueueList key={conversationScope} queue={messageQueue.queue} error={messageQueue.queueError || messageQueue.settingsError} busy={busy || messageQueue.acting} onEdit={messageQueue.edit} onPromote={messageQueue.promote} onRemove={messageQueue.remove} onResume={messageQueue.resume} onRetry={messageQueue.retry} />
                                                {pendingSubmission.current?.request && !busy ? <div className="agent-message-queue agent-message-queue-error"><span>发送结果待确认</span><button type="button" onClick={() => { const request = pendingSubmission.current?.request; if (request) void submit(request.prompt, { preserveDraft: true }); }}>重试发送</button></div> : null}
                                            </>}
                                            prompt={prompt}
                                            disabled={activeHistoryState?.status === "unavailable" || !historyHydrated || !pendingHydrated}
                                            sending={busy}
                                            running={running}
                                            placeholder="输入消息，用 @ 引用节点，用 / 引用技能"
                                            theme={theme}
                                            onPromptChange={setPrompt}
                                            onSubmit={(oppositeMode) => void submit(undefined, { oppositeMode })}
                                            onStop={run?.id && running ? stop : undefined}
                                            stopping={stopping}
                                            sendMode={messageQueue.mode}
                                            sendModeSaving={messageQueue.savingMode || !messageQueue.mode}
                                            onSendModeSettings={() => setView("settings")}
                                            references={[...references, ...buildSkillMentionReferences(installedSkills)]}
                                            slashSkills={installedSkills}
                                            includeAssetLibrary={false}
                                            submitAccessory={<AgentContextRing key={String(open)} view={presentAgentContextUsage(contextUsage)} />}
                                            left={
                                                <ComposerControls
                                                    key={String(open)}
                                                    reasoningMode={reasoningSupported ? reasoningMode : "off"}
                                                    onReasoningModeChange={(value) => {
                                                        if (reasoningSupported) setReasoningMode(value);
                                                    }}
                                                    config={config}
                                                    selectedModel={selectedModel}
                                                    permissionMode={permissionMode}
                                                    theme={theme}
                                                    onModelChange={setModel}
                                                    onPermissionChange={setPermissionMode}
                                                />
                                            }
                                        />
                                    </motion.div>
                                )}
                            </AnimatePresence>
                        </div>
                    </motion.aside>
            </div>
            {open ? <CanvasAgentSkillLibraryModal
                open={skillsOpen}
                theme={theme}
                installedSkills={installedSkills}
                marketSkills={marketSkills}
                selectedSkillIds={selectedSkillIds}
                globalDefaultSkillIds={globalDefaultSkillIds}
                globalDefaultSkills={globalDefaultSkills}
                categories={skillCategories}
                category={skillTag}
                search={skillSearch}
                loading={skillsLoading}
                hasMore={skillHasMore}
                onClose={() => setSkillsOpen(false)}
                onCategoryChange={setSkillTag}
                onSearch={setSkillSearch}
                onToggle={(id) =>
                    setSelectedSkillIds((current) => {
                        if (current.includes(id)) return current.filter((item) => item !== id);
                        return [...current, id];
                    })
                }
                onInstall={installSkill}
                onLoadMore={loadMoreSkills}
            /> : null}
        </>
    );
    const scopedPanel = <AgentPanelVisibilityContext value={open}>{panel}</AgentPanelVisibilityContext>;
    return floating ? createPortal(scopedPanel, document.body) : scopedPanel;
}

function AgentLauncher({ theme, statusColor, approvalPending, reducedMotion, status, onOpen }: { theme: CanvasTheme; statusColor: string; approvalPending: boolean; reducedMotion: boolean; status: string; onOpen: () => void }) {
    const appearance = useAppearanceStore((state) => state.appearance.canvas) || DEFAULT_CANVAS_APPEARANCE;
    const appearanceResolved = useAppearanceStore((state) => state.resolved);
    const appearanceRevision = useAppearanceStore((state) => state.appearance.revision);
    const live = appearance.avatarType === "live2d" && Boolean(appearance.live2dResourceId && appearance.live2dEntry);
    // 动画包形象（avatar.json + 各状态 MP4）由后台「站点外观」配置，按 Agent 运行状态切换片段。
    const avatarState = agentAvatarStateForRunStatus(status);
    const bundleManifestURL = appearance.avatarBundleResourceId ? agentAvatarBundleFileURL(appearance.avatarBundleResourceId, "avatar.json", false, appearanceRevision) : "/doll/avatar.json";
    const [bundleManifestState, setBundleManifestState] = useState<{ url: string; manifest: AgentAvatarBundleManifest | null } | null>(null);
    useEffect(() => {
        if (appearance.avatarType !== "bundle") {
            setBundleManifestState(null);
            return;
        }
        const controller = new AbortController();
        void (async () => {
            try {
                const response = await fetch(bundleManifestURL, { credentials: "include", signal: controller.signal });
                if (!response.ok) throw new Error(`无法读取 Agent 动画包（HTTP ${response.status}）`);
                const manifest: unknown = await response.json();
                if (!isAgentAvatarBundleManifest(manifest)) throw new Error("avatar.json 格式无效");
                setBundleManifestState({ url: bundleManifestURL, manifest });
            } catch {
                if (!controller.signal.aborted) setBundleManifestState({ url: bundleManifestURL, manifest: null });
            }
        })();
        return () => controller.abort();
    }, [appearance.avatarType, bundleManifestURL]);
    const currentBundleManifestState = bundleManifestState?.url === bundleManifestURL ? bundleManifestState : null;
    const bundleManifest = currentBundleManifestState?.manifest ?? null;
    const bundleManifestLoading = appearance.avatarType === "bundle" && currentBundleManifestState === null;
    const clip = bundleManifest ? agentAvatarClipForState(appearance, avatarState, bundleManifest) : "";
    const clipFile = bundleManifest?.clips[clip] || "";
    const mediaURL = appearance.avatarType === "bundle" && clipFile ? (appearance.avatarBundleResourceId ? agentAvatarBundleFileURL(appearance.avatarBundleResourceId, clipFile, false, appearanceRevision) : `/doll/${encodeURIComponent(clipFile)}`) : "";
    const [mediaError, setMediaError] = useState(false);
    useEffect(() => setMediaError(false), [mediaURL]);
    const [viewport, setViewport] = useState(() => ({ width: window.innerWidth, height: window.innerHeight }));
    useEffect(() => {
        const resize = () => setViewport({ width: window.innerWidth, height: window.innerHeight });
        window.addEventListener("resize", resize);
        return () => window.removeEventListener("resize", resize);
    }, []);
    const media = Boolean(mediaURL && !mediaError);
    const aspectRatio = live ? 0.75 : bundleManifest ? bundleManifest.width / bundleManifest.height : appearance.avatarType === "bundle" ? 480 / 432 : 1;
    const largeAvatar = live || media || bundleManifestLoading;
    const height = largeAvatar ? Math.max(40, Math.min(appearance.avatarHeight, viewport.height - 60, (viewport.width - 40) / aspectRatio)) : 64;
    const width = largeAvatar ? Math.round(height * aspectRatio) : 64;
    return (
        <motion.button
            type="button"
            aria-label={`打开${appearance.agentName}`}
            title={approvalPending ? "Agent 等待你的审批" : "打开 Agent 助手"}
            className={cn("canvas-agent-launcher fixed z-[var(--z-panel-floating)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-current/35", live && "canvas-agent-launcher-live2d", media && "canvas-agent-launcher-media")}
            style={{ right: "var(--canvas-inset-x)", bottom: "var(--canvas-inset-y)", width, height, color: theme.node.text, "--canvas-agent-launcher-shadow": theme.spatial.shadow } as CSSProperties}
            data-canvas-no-zoom
            onClick={onOpen}
            whileHover={reducedMotion ? undefined : { scale: 1.035 }}
            whileTap={reducedMotion ? undefined : { scale: 0.96 }}
            transition={{ duration: reducedMotion ? 0 : 0.18 }}
        >
            {live ? (
                <Live2DAvatar url={live2DModelURL(appearance.live2dResourceId, appearance.live2dEntry)} width={width} height={height} reducedMotion={reducedMotion} fallback={<FluidOrb size={60} color="#7164f6" />} />
            ) : media ? (
                <AgentAvatarVideo src={mediaURL} loop={!["completed", "failed", "cancelled", "rejected"].includes(avatarState)} onError={() => setMediaError(true)} />
            ) : bundleManifestLoading || !appearanceResolved ? (
                <span role="status" aria-busy="true">
                    <LoaderCircle className={reducedMotion ? "size-6" : "size-6 motion-safe:animate-spin"} aria-hidden="true" />
                    <span className="sr-only">正在加载助手形象</span>
                </span>
            ) : (
                <FluidOrb size={60} color="#7164f6" />
            )}
            {appearance.launcherLabel ? <span className="canvas-agent-launcher-label">{appearance.launcherLabel}</span> : null}
            <span className={cn("canvas-agent-launcher-status", approvalPending && "is-pending")} style={{ "--canvas-agent-status-color": statusColor } as CSSProperties} />
            {approvalPending ? <span className="canvas-agent-launcher-badge">待审批</span> : null}
        </motion.button>
    );
}
export function AgentHeader({
    theme,
    hasMessages,
    statusLabel,
    statusColor,
    nodeCount,
    onNew,
    onHistory,
    onSettings,
    onCollapse,
    onExport,
    exporting,
    skillsOpen,
    selectedSkillCount,
    onSkills,
}: {
    theme: CanvasTheme;
    hasMessages: boolean;
    statusLabel: string;
    statusColor: string;
    nodeCount: number;
    onNew: () => void;
    onHistory: () => void;
    onSettings: () => void;
    onCollapse: () => void;
    onExport: () => void;
    exporting: boolean;
    skillsOpen: boolean;
    selectedSkillCount: number;
    onSkills: () => void;
}) {
    const appearance = useAppearanceStore((state) => state.appearance.canvas) || DEFAULT_CANVAS_APPEARANCE;
    return (
        <header className="agent-panel-header flex shrink-0 flex-wrap items-center gap-3">
            <div className="min-w-0 flex-1 basis-36">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="agent-panel-title">
                        {agentCopy(appearance.panelTitle, appearance.agentName)}
                        {hasMessages ? "" : " · 新对话"}
                    </span>
                    <span className="agent-panel-status flex items-center gap-1" style={{ color: statusColor }}>
                        <CircleDot className="size-3" />
                        {statusLabel}
                    </span>
                </div>
                <div className="agent-panel-context">
                    {appearance.agentName} · 当前画布 {nodeCount} 个节点
                </div>
            </div>
            <div className="agent-header-actions ml-auto flex shrink-0 items-center gap-0.5" style={{ color: theme.node.muted }}>
                <Button
                    type="text"
                    className="agent-header-skills"
                    shape="circle"
                    icon={<Sparkles className="size-4" />}
                    style={{ color: selectedSkillCount || skillsOpen ? theme.accent.primary : theme.node.muted }}
                    aria-label={`打开 Skills 技能库${selectedSkillCount ? `，已启用 ${selectedSkillCount} 个` : ""}`}
                    aria-expanded={skillsOpen}
                    aria-haspopup="dialog"
                    title="打开 Skills 技能库"
                    onClick={onSkills}
                />
                <Button type="text" shape="circle" icon={<Download className="size-4" />} loading={exporting} onClick={onExport} aria-label="导出 Agent 调试记录" title="导出对话、工具参数、审批和错误（分享前请检查隐私）" />
                <Button type="text" shape="circle" icon={<MessageSquarePlus className="size-4" />} onClick={onNew} aria-label="新建对话" title="新建对话" />
                <Button type="text" shape="circle" icon={<History className="size-4" />} onClick={onHistory} aria-label="历史对话" title="历史对话" />
                <Button type="text" shape="circle" icon={<Settings2 className="size-4" />} onClick={onSettings} aria-label="Agent 设置" title="Agent 设置" />
                <Button type="text" shape="circle" icon={<X className="size-4" />} onClick={onCollapse} aria-label="收起 Agent" title="收起" />
            </div>
        </header>
    );
}

const CONTEXT_PHASE_LABEL: Record<AgentContextPhase, string> = {
    idle: "尚未测量",
    unknown: "窗口未知",
    ok: "上下文充足",
    watch: "用量较高",
    compress: "即将压缩",
    compacting: "正在压缩",
    stale: "压缩后待刷新",
};

function formatContextCount(tokens: number | undefined) {
    if (tokens === undefined) return "—";
    if (tokens >= 1_000_000) return `${Math.round(tokens / 100_000) / 10}M`;
    if (tokens >= 1_000) return `${Math.round(tokens / 100) / 10}K`;
    return Math.round(tokens).toLocaleString("zh-CN");
}

export function AgentContextRing({ view }: { view: AgentContextUsageView }) {
    const [open, setOpen] = useState(false);
    const theme = canvasThemes[useActiveTheme()];
    const percent = view.ratio === undefined ? view.label : `${Math.round(view.ratio * 100)}%`;
    const meterLabel = view.ratio === undefined ? "—" : percent;
    const used = formatContextCount(view.inputTokens);
    const budget = formatContextCount(view.usableTokens);
    const remaining = formatContextCount(view.remainingTokens);
    const usedRatio = view.ratio === undefined ? 0 : Math.max(0, Math.min(1, view.ratio));
    const phaseLabel = CONTEXT_PHASE_LABEL[view.phase];
    const sourceLabel = view.tokenSource === "provider" ? "模型实测" : view.estimate ? "本地估算" : "未测量";
    const usageHeading = view.ratio !== undefined ? `上下文已用 ${percent}` : view.phase === "idle" ? "上下文用量" : view.phase === "unknown" ? "上下文窗口未知" : `上下文${view.label}`;

    return (
        <Popover
            open={open}
            onOpenChange={setOpen}
            trigger="click"
            placement="topRight"
            autoAdjustOverflow
            align={{ overflow: { adjustX: true, adjustY: true, shiftX: true, shiftY: true } }}
            arrow={false}
            classNames={{ root: "agent-context-popover" }}
            styles={{ root: { "--agent-surface-base": theme.node.panel, "--agent-ink": theme.node.text, "--agent-accent": theme.accent.primary } as CSSProperties }}
            getPopupContainer={() => document.body}
            content={
                <div className="agent-context-panel" data-phase={view.phase} data-canvas-no-zoom data-canvas-wheel-scroll>
                    <span className="agent-context-eyebrow">{view.tokenSource === "provider" ? "最近模型请求" : "上下文估算"}</span>
                    <div className="agent-context-panel-head">
                        <strong>{usageHeading}</strong>
                        {view.phase !== "ok" ? <span className={`agent-context-phase is-${view.phase}`}>{phaseLabel}</span> : null}
                    </div>
                    <div className="agent-context-summary">
                        {view.remainingTokens !== undefined && view.usableTokens !== undefined ? (
                            <>
                                <strong>{used}</strong>
                                <span>/ {budget} Token</span>
                                <em>剩余 {remaining}</em>
                            </>
                        ) : (
                            <>
                                <strong>{used}</strong>
                                <span>Token</span>
                            </>
                        )}
                    </div>
                    <div className="agent-context-progress-head">
                        <span>输入预算占用</span>
                        <strong>{percent}</strong>
                    </div>
                    <div className="agent-context-progress" role="progressbar" aria-label={`上下文已用 ${percent}`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={view.ratio === undefined ? undefined : Math.round(view.ratio * 100)}>
                        <span style={{ width: `${usedRatio * 100}%` }} />
                    </div>
                    <div className="agent-context-panel-foot">
                        <span>
                            {sourceLabel}
                            {view.estimate ? " · 不是计费 Token" : " · 最近一次模型输入"}
                        </span>
                        <span title={view.cacheHitRate === undefined ? "暂无模型缓存用量数据" : "上一请求的缓存读取 Token ÷ 总输入 Token"}>
                            缓存命中率 {view.cacheHitRate === undefined ? "暂无数据" : `${Math.round(view.cacheHitRate * 1000) / 10}%`}
                        </span>
                    </div>
                    {view.lastCompaction ? <p className="agent-context-note">本轮已完成一次上下文压缩，下一次读数会刷新。</p> : null}
                </div>
            }
        >
            <button type="button" className={`agent-context-ring is-${view.phase}`} aria-label={`${usageHeading}，${phaseLabel}。点击查看明细`} aria-expanded={open} title="查看上下文用量" onPointerDown={(event) => event.stopPropagation()}>
                <span
                    className="agent-context-ring-visual"
                    aria-hidden="true"
                    style={
                        {
                            "--agent-context-progress": `${view.ring * 100}%`,
                        } as CSSProperties
                    }
                ></span>
                <span className="agent-context-meter-copy">
                    <strong>{meterLabel}</strong>
                    <small>上下文</small>
                </span>
            </button>
        </Popover>
    );
}

function AgentHistory({
    conversations,
    activeConversationId,
    theme,
    onBack,
    onNew,
    onOpen,
    onDelete,
}: {
    conversations: CloudAgentConversation[];
    activeConversationId: string;
    theme: CanvasTheme;
    onBack: () => void;
    onNew: () => void;
    onOpen: (conversation: CloudAgentConversation) => void;
    onDelete: (id: string) => void;
}) {
    return (
        <div className="canvas-agent-history-root flex min-h-0 min-w-0 flex-1 flex-col">
            <header className="agent-panel-header flex shrink-0 items-center gap-2">
                <Button type="text" shape="circle" icon={<ArrowLeft className="size-4" />} onClick={onBack} aria-label="返回对话" />
                <div className="min-w-0 flex-1">
                    <div className="text-sm font-semibold">历史对话</div>
                    <div className="mt-0.5 text-[11px] opacity-40">保存在当前账号与画布下</div>
                </div>
                <Button type="text" shape="circle" icon={<MessageSquarePlus className="size-4" />} onClick={onNew} aria-label="新建对话" title="新建对话" />
            </header>
            <div className="canvas-agent-history-scroll thin-scrollbar min-h-0 min-w-0 flex-1 overflow-y-auto px-3 py-3">
                {conversations.length ? (
                    <div className="space-y-1">
                        {conversations.map((conversation) => {
                            const preview = truncateConversationPreview(conversation.messages.at(-1)?.text || "尚未发送消息");
                            const active = conversation.id === activeConversationId;
                            return (
                                <div
                                    key={conversation.id}
                                    className="canvas-agent-history-item group flex w-full items-center gap-3 rounded-lg px-3 py-3 text-left transition-colors"
                                    style={{ background: active ? theme.toolbar.itemHover : "transparent", color: theme.node.text }}
                                >
                                    <button type="button" className="canvas-agent-history-open flex min-w-0 flex-1 items-center gap-3 text-left" onClick={() => onOpen(conversation)} aria-current={active ? "page" : undefined}>
                                        <span className="grid size-8 shrink-0 place-items-center rounded-full" style={{ background: theme.node.fill, color: theme.node.muted }}>
                                            <Clock3 className="size-3.5" />
                                        </span>
                                        <span className="canvas-agent-history-text min-w-0 flex-1">
                                            <span className="canvas-agent-history-title block truncate text-[13px] font-medium">{conversation.title}</span>
                                            <span className="canvas-agent-history-preview mt-0.5 block text-[11px] opacity-40" title={preview}>
                                                {preview}
                                            </span>
                                        </span>
                                        <span className="canvas-agent-history-time shrink-0 text-[10px] opacity-35">{formatConversationTime(conversation.updatedAt)}</span>
                                    </button>
                                    <Button
                                        type="text"
                                        size="small"
                                        danger
                                        className="canvas-agent-history-delete !h-7 !px-2 !text-xs !opacity-80"
                                        icon={<Trash2 className="size-3.5" />}
                                        onClick={() => onDelete(conversation.id)}
                                        aria-label={`删除对话 ${conversation.title}`}
                                        title="删除对话"
                                    >
                                        删除
                                    </Button>
                                </div>
                            );
                        })}
                    </div>
                ) : (
                    <div className="flex h-full min-h-72 flex-col items-center justify-center text-center">
                        <History className="size-5 opacity-30" />
                        <div className="mt-3 text-sm font-medium">还没有历史对话</div>
                        <div className="mt-1 text-xs opacity-40">发送第一条消息后会自动保存</div>
                    </div>
                )}
            </div>
        </div>
    );
}

function AgentConversation({
    theme,
    messages,
    references,
    busy,
    approval,
    approvalSubmitting,
    nodeCount,
    onChooseSkill,
    onDraftPrompt,
    onFocusNode,
    onApprovalReasonChange,
    onApprove,
    onReject,
    footer,
}: {
    theme: CanvasTheme;
    messages: CloudAgentChatMessage[];
    references: CanvasResourceReference[];
    busy: boolean;
    approval: ApprovalState | null;
    approvalSubmitting: boolean;
    nodeCount: number;
    onChooseSkill: () => void;
    onDraftPrompt: (prompt: string) => void;
    onFocusNode?: (nodeId: string) => void;
    onApprovalReasonChange: (reason: string) => void;
    onApprove: (settings?: AgentMediaSettings) => void;
    onReject: () => void;
    footer?: ReactNode;
}) {
    const appearance = useAppearanceStore((state) => state.appearance.canvas) || DEFAULT_CANVAS_APPEARANCE;
    const scrollRef = useRef<HTMLDivElement>(null);
    const contentRef = useRef<HTMLDivElement>(null);
    const followRef = useRef(true);
    const lastUserId = messages.findLast((item) => item.role === "user")?.id;
    // 正文之间的思考、工具分别共用一行，计划/提问载体由输入区固定条渲染。
    const segments = useMemo(() => buildAgentFeedSegments(messages), [messages]);
    const lastMessage = messages.at(-1);

    // 自己发送时恢复跟随；阅读旧消息时不让流式输出抢走滚动位置。
    useLayoutEffect(() => {
        followRef.current = true;
    }, [lastUserId]);
    useLayoutEffect(() => {
        const element = scrollRef.current;
        if (element && followRef.current) element.scrollTop = element.scrollHeight;
    }, [messages, busy, approval]);
    useEffect(() => {
        const element = scrollRef.current;
        const content = contentRef.current;
        if (!element || !content) return;
        const observer = new ResizeObserver(() => {
            if (followRef.current) element.scrollTop = element.scrollHeight;
        });
        observer.observe(element);
        observer.observe(content);
        return () => observer.disconnect();
    }, []);

    return (
        <div
            ref={scrollRef}
            data-agent-conversation
            className="agent-conversation thin-scrollbar min-h-0 flex-1 overflow-y-auto"
            onScroll={(event) => {
                const element = event.currentTarget;
                followRef.current = element.scrollHeight - element.scrollTop - element.clientHeight < 48;
            }}
        >
            {!messages.length ? <AgentWelcome appearance={appearance} nodeCount={nodeCount} onChooseSkill={onChooseSkill} onDraftPrompt={onDraftPrompt} /> : null}
            <div ref={contentRef} className="agent-conversation-messages">
                {segments.map((segment) =>
                    segment.kind === "operations" ? (
                        // 聚合后按最新事件判断当前动作，不按行在列表里的位置判断。
                        <AgentOperationFeed key={segment.key} items={segment.items} theme={theme} references={references} onFocusNode={onFocusNode} live={busy && segment.items.at(-1) === lastMessage} />
                    ) : segment.kind === "reasoning" ? (
                        <AgentReasoningFeed key={segment.key} items={segment.items} theme={theme} />
                    ) : (
                        <AgentChatMessage key={segment.key} item={segment.item} theme={theme} references={references} onFocusNode={onFocusNode} isStreaming={busy && !approval && segment.item.streaming === true && segment.item === lastMessage} />
                    ),
                )}
                {approval ? <ApprovalCard key={approval.approvalId} approval={approval} theme={theme} submitting={approvalSubmitting} onFocusNode={onFocusNode} onReasonChange={onApprovalReasonChange} onApprove={onApprove} onReject={onReject} /> : null}
                {busy && !approval ? <AgentWorkingMessage theme={theme} label="正在处理当前画布" /> : null}
                {footer}
            </div>
        </div>
    );
}

function ComposerControls({
    config,
    selectedModel,
    permissionMode,
    theme,
    onModelChange,
    onPermissionChange,
    reasoningMode,
    onReasoningModeChange,
}: {
    config: ReturnType<typeof useEffectiveConfig>;
    selectedModel: string;
    permissionMode: AgentPermissionMode;
    theme: CanvasTheme;
    onModelChange: (model: string) => void;
    onPermissionChange: (mode: AgentPermissionMode) => void;
    // 推理模式由父组件持有状态；本组件只负责渲染选择器并回报变更。
    // reasoningSupported 刻意不通过 props 传入：本组件在下面自行推导一次，
    // 查不到能力时退化为"不支持推理"，避免父作用域变量缺失把面板打崩。
    reasoningMode: AgentReasoningMode;
    onReasoningModeChange: (mode: AgentReasoningMode) => void;
}) {
    const permissionVisual = agentPermissionVisual(permissionMode);
    const PermissionIcon = permissionVisual.icon;
    // 这个子组件拿不到父组件的 reasoningSupported（那是父作用域的局部变量），
    // 必须自己按当前配置与选中模型推导一次；查不到能力时退化成"不支持推理"，
    // 不能让整个面板因 ReferenceError 崩掉。
    const reasoningSupported = useMemo(() => {
        try {
            return Boolean(modelCapabilityConfigFor(config, selectedModel).text?.thinking);
        } catch {
            return false;
        }
    }, [config, selectedModel]);
    return (
        <div className="agent-composer-selection flex min-w-0 flex-1 flex-nowrap items-center gap-0.5">
            <ModelPicker
                config={config}
                value={selectedModel}
                capability="text"
                onChange={onModelChange}
                variant="creation"
                fullWidth
                className="agent-composer-model-trigger !h-8 !min-w-0 !w-full !max-w-full !border-0 !bg-transparent !px-1.5 !shadow-none"
                popoverClassName="agent-model-picker-popover"
                showSelectedPrice={false}
                showOptionPrices
                placeholder="选择文本模型"
            />
            {reasoningSupported ? (
                <Dropdown trigger={["click"]} placement="topLeft" menu={{ items: reasoningMenuItems(reasoningMode, onReasoningModeChange) }}>
                    <button
                        type="button"
                        aria-label="选择 Agent 推理模式"
                        title="推理模式：只用于规划和工具选择"
                        className="flex h-8 shrink-0 items-center gap-1 rounded-md px-2 text-[11px] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-current/25"
                        style={{ color: reasoningMode === "off" ? theme.node.muted : theme.accent.primary, background: reasoningMode === "off" ? "transparent" : theme.node.fill }}
                    >
                        <Sparkles className="size-3.5" />
                        {reasoningModeLabel(reasoningMode)}
                    </button>
                </Dropdown>
            ) : null}
            <Dropdown trigger={["click"]} placement="topLeft" menu={{ items: agentPermissionMenuItems(permissionMode, onPermissionChange) }}>
                <button
                    type="button"
                    className="grid size-8 shrink-0 place-items-center rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-current/25"
                    style={{ color: theme.node.muted, background: "transparent" }}
                    aria-label={`执行权限：${agentPermissionLabel(permissionMode)}，点击切换`}
                    title={`执行权限：${agentPermissionLabel(permissionMode)}，点击切换`}
                >
                    <PermissionIcon className="size-3.5" style={{ color: permissionVisual.color }} aria-hidden="true" />
                </button>
            </Dropdown>
        </div>
    );
}

const reasoningLabels: Record<AgentReasoningMode, string> = { off: "直达", auto: "自动推理", deep: "深入推理" };

function reasoningModeLabel(mode: AgentReasoningMode) {
    return reasoningLabels[mode];
}

function reasoningMenuItems(mode: AgentReasoningMode, onChange: (value: AgentReasoningMode) => void) {
    return (Object.keys(reasoningLabels) as AgentReasoningMode[]).map((value) => ({
        key: value,
        label: reasoningLabels[value],
        icon: value === mode ? <Check className="size-3.5" /> : undefined,
        onClick: () => onChange(value),
    }));
}

function ApprovalCard({
    approval,
    theme,
    submitting,
    onFocusNode,
    onReasonChange,
    onApprove,
    onReject,
}: {
    approval: ApprovalState;
    theme: CanvasTheme;
    submitting: boolean;
    onFocusNode?: (nodeId: string) => void;
    onReasonChange: (value: string) => void;
    onApprove: (settings?: AgentMediaSettings) => void;
    onReject: () => void;
}) {
    const [showReason, setShowReason] = useState(Boolean(approval.reason));
    const [mediaSettings, setMediaSettings] = useState<AgentMediaSettings>();
    const panelOpen = useContext(AgentPanelVisibilityContext);
    const imageApproval = agentImageApproval(approval.detail);
    const action = agentApprovalPresentation(approval.detail);
    return (
        <section className="canvas-agent-approval-card" aria-label={action.title}>
            <div className="canvas-agent-approval-header">
                <span className="canvas-agent-approval-icon" aria-hidden="true">
                    <ShieldCheck className="size-4" />
                </span>
                <h3>{action.title}</h3>
                <span className="canvas-agent-approval-badge">等待你的确认</span>
            </div>
            <p className="canvas-agent-approval-description" style={{ color: theme.node.muted }}>
                {action.description}
            </p>
            {action.items.length ? (
                <div className="canvas-agent-approval-items" aria-label="涉及节点">
                    {action.items.map((item, index) => (
                        <ApprovalPreviewItemView
                            key={`${item.operation}-${item.nodeId || item.nodeTitle || index}-${index}`}
                            item={imageApproval ? { ...item, details: item.details?.filter((detail) => !/^(模型|画幅|质量)[：:]/.test(detail)) } : item}
                            theme={theme}
                            onFocusNode={onFocusNode}
                        />
                    ))}
                </div>
            ) : (
                <div className="canvas-agent-approval-empty" style={{ color: theme.node.muted }}>
                    无法确认具体目标，继续前请重新读取画布。
                </div>
            )}
            {imageApproval ? <CanvasAgentImageApprovalSettings key={String(panelOpen)} initial={imageApproval} value={mediaSettings} onChange={setMediaSettings} theme={theme} disabled={submitting} /> : null}
            <button type="button" className="canvas-agent-approval-reason-toggle" aria-expanded={showReason} onClick={() => setShowReason((value) => !value)} disabled={submitting}>
                {showReason ? "收起拒绝理由" : "填写拒绝理由（可选）"}
            </button>
            {showReason ? (
                <Input.TextArea
                    className="canvas-agent-approval-reason"
                    value={approval.reason}
                    onChange={(event) => onReasonChange(event.target.value)}
                    placeholder="告诉 Agent 为什么暂不执行"
                    autoSize={{ minRows: 2, maxRows: 3 }}
                    maxLength={2000}
                    disabled={submitting}
                />
            ) : null}
            <div className="canvas-agent-approval-actions">
                <button type="button" className="canvas-agent-approval-reject" disabled={submitting} onClick={onReject}>
                    暂不执行
                </button>
                <button type="button" className="canvas-agent-approval-approve" disabled={submitting} onClick={() => onApprove(mediaSettings)}>
                    {submitting ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : <Check className="size-4" aria-hidden="true" />}
                    {submitting ? "正在提交" : "同意执行"}
                </button>
            </div>
        </section>
    );
}

function ApprovalPreviewItemView({ item, theme, onFocusNode }: { item: ReturnType<typeof agentApprovalPresentation>["items"][number]; theme: CanvasTheme; onFocusNode?: (nodeId: string) => void }) {
    const operationLabel = AGENT_APPROVAL_OPERATION_LABELS[item.operation];
    const renderNode = (title: string | undefined, id: string | undefined, typeLabel: string | undefined, role: "source" | "target" | "node") => {
        if (!title) return null;
        const content = (
            <>
                <span className="canvas-agent-approval-node-title">{title}</span>
                {typeLabel ? <span className="canvas-agent-approval-node-type">{typeLabel}</span> : null}
            </>
        );
        return id && onFocusNode ? (
            <button type="button" className="canvas-agent-approval-node canvas-agent-approval-node-button" onClick={() => onFocusNode(id)} title="定位到画布节点">
                {content}
            </button>
        ) : (
            <span className={`canvas-agent-approval-node canvas-agent-approval-node-${role}`}>{content}</span>
        );
    };
    return (
        <article className="canvas-agent-approval-item">
            <div className="canvas-agent-approval-item-main">
                <span className={`canvas-agent-approval-operation canvas-agent-approval-operation-${item.operation}`}>{operationLabel}</span>
                {item.operation === "connect_nodes" ? (
                    <div className="canvas-agent-approval-connection">
                        {renderNode(item.nodeTitle, item.nodeId, item.nodeTypeLabel, "source")}
                        <span className="canvas-agent-approval-arrow" aria-hidden="true">
                            →
                        </span>
                        {renderNode(item.targetNodeTitle, item.targetNodeId, undefined, "target")}
                    </div>
                ) : (
                    <div className="canvas-agent-approval-node-summary">
                        {renderNode(item.nodeTitle, item.nodeId, item.nodeTypeLabel, "node")}
                        {item.resultTitle ? (
                            <>
                                <span className="canvas-agent-approval-change-arrow" aria-hidden="true">
                                    改为
                                </span>
                                <span className="canvas-agent-approval-result-title">《{item.resultTitle}》</span>
                            </>
                        ) : null}
                    </div>
                )}
            </div>
            {item.fields?.length ? (
                <div className="canvas-agent-approval-field-list">
                    修改：
                    {item.fields.map((field) => (
                        <span key={field}>{field}</span>
                    ))}
                </div>
            ) : null}
            {item.details?.length ? (
                <div className="canvas-agent-approval-detail-list">
                    {item.details.map((detail) => (
                        <span key={detail}>{detail}</span>
                    ))}
                </div>
            ) : null}
            <div className="canvas-agent-approval-summary">{item.summary}</div>
        </article>
    );
}

function positiveNumber(value: string) {
    const number = Number(value);
    return Number.isFinite(number) && number > 0 ? number : undefined;
}
function truncateConversationPreview(value: string, max = 96) {
    const compact = markdownPlainText(value);
    return compact.length > max ? `${compact.slice(0, max)}…` : compact || "尚未发送消息";
}

function formatConversationTime(value: string) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "";
    const today = new Date();
    if (date.toDateString() === today.toDateString()) return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false });
    return date.toLocaleDateString("zh-CN", { month: "numeric", day: "numeric" });
}
function appendAgentError(current: CloudAgentChatMessage[], id: string, cause: unknown, fallback?: string) {
    const message = agentErrorPresentation(cause, fallback);
    const last = current.at(-1);
    if (last?.role === "error" && last.title === message.title && last.text === message.text) return current;
    return appendUniqueMessage(current, { id, role: "error", ...message });
}

function isNotFoundError(cause: unknown) {
    if (!cause || typeof cause !== "object") return false;
    const status = "status" in cause ? (cause as { status?: unknown }).status : undefined;
    return status === 404 || (cause instanceof Error && /\(404\)/u.test(cause.message));
}
