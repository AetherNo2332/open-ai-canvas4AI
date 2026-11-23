import { useEffect, useState, type CSSProperties } from "react";
import { Button, Dropdown } from "antd";
import { motion, useReducedMotion } from "motion/react";
import { ArrowLeft, Clock3, Download, Ellipsis, History, LoaderCircle, MessageSquarePlus, PanelRight, PictureInPicture2, RotateCcw, Settings2, Sparkles, Trash2, X } from "lucide-react";
import { FluidOrb } from "@/components/ui/fluid-orb";
import { AgentAvatarVideo } from "./agent-avatar-video";
import { Live2DAvatar } from "./agent-live2d-avatar";
import { agentAvatarBundleFileURL, live2DModelURL } from "@/services/api/appearance";
import { DEFAULT_CANVAS_APPEARANCE, agentAvatarClipForState, agentAvatarStateForRunStatus, agentCopy, isAgentAvatarBundleManifest, type AgentAvatarBundleManifest, type CanvasAppearance } from "@/lib/agent/agent-appearance";
import { markdownPlainText } from "@/lib/markdown-plain-text";
import { cn } from "@/lib/utils";
import type { CanvasTheme } from "@/lib/agent/theme";
import type { AgentConversationRecord } from "@/services/agent/conversation-types";
import { useAppearanceStore } from "@/stores/use-appearance-store";

export function AgentLauncher({ theme, statusColor, approvalPending, status, onOpen }: { theme: CanvasTheme; statusColor: string; approvalPending: boolean; status: string; onOpen: () => void }) {
    const appearance = useAppearanceStore((state) => state.appearance.canvas) || DEFAULT_CANVAS_APPEARANCE;
    const appearanceResolved = useAppearanceStore((state) => state.resolved);
    const appearanceRevision = useAppearanceStore((state) => state.appearance.revision);
    const reducedMotion = useReducedMotion();
    const live = appearance.avatarType === "live2d" && Boolean(appearance.live2dResourceId && appearance.live2dEntry);
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
            className={cn("canvas-agent-launcher fixed z-[var(--z-agent-panel)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-current/35", live && "canvas-agent-launcher-live2d", media && "canvas-agent-launcher-media")}
            style={{ right: "var(--canvas-inset-x)", bottom: "var(--canvas-inset-y)", width, height, color: theme.node.text, "--canvas-agent-launcher-shadow": theme.spatial.shadow } as CSSProperties}
            data-canvas-no-zoom
            data-agent-launcher
            onClick={onOpen}
            whileHover={reducedMotion ? undefined : { scale: 1.035 }}
            whileTap={reducedMotion ? undefined : { scale: 0.96 }}
            transition={{ duration: reducedMotion ? 0 : 0.18 }}
        >
            {live ? (
                <Live2DAvatar url={live2DModelURL(appearance.live2dResourceId, appearance.live2dEntry)} width={width} height={height} reducedMotion={Boolean(reducedMotion)} fallback={<FluidOrb size={60} color="#7164f6" />} />
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

export function AgentPanelHeader({
    theme,
    hasMessages,
    statusLabel,
    statusColor,
    onNew,
    onHistory,
    onSettings,
    onResetLayout,
    docked = false,
    onToggleDock,
    onCollapse,
    onExport,
    exporting,
    skillsOpen,
    selectedSkillCount,
    onSkills,
    title,
    skillsAvailable = true,
}: {
    theme: CanvasTheme;
    hasMessages: boolean;
    statusLabel: string;
    statusColor: string;
    onNew: () => void;
    onHistory: () => void;
    onSettings: () => void;
    onResetLayout?: () => void;
    docked?: boolean;
    onToggleDock?: () => void;
    onCollapse: () => void;
    onExport: () => void;
    exporting: boolean;
    skillsOpen: boolean;
    selectedSkillCount: number;
    onSkills: () => void;
    title?: string;
    skillsAvailable?: boolean;
}) {
    const appearance = useAppearanceStore((state) => state.appearance.canvas) || DEFAULT_CANVAS_APPEARANCE;
    return (
        <header data-agent-drag-handle={docked ? undefined : ""} className="agent-panel-header agent-panel-chat-header flex shrink-0 items-center gap-2">
            <div className="min-w-0 flex-1">
                <div className="agent-panel-title truncate">{title || agentCopy(appearance.panelTitle, appearance.agentName)}</div>
                <div className="agent-panel-status truncate" style={{ color: statusColor }}>
                    {statusLabel === "待命" && !hasMessages ? "新对话" : statusLabel}
                </div>
            </div>
            <div className="agent-header-actions ml-auto flex shrink-0 items-center gap-0.5" style={{ color: theme.node.muted }}>
                {onToggleDock ? (
                    <Button
                        type="text"
                        shape="circle"
                        icon={docked ? <PictureInPicture2 className="size-4" /> : <PanelRight className="size-4" />}
                        onClick={onToggleDock}
                        aria-pressed={docked}
                        aria-label="停靠到画布右侧"
                        title={docked ? "已停靠到画布右侧，点击改为浮动窗口" : "停靠到画布右侧，画布让出位置不被遮挡"}
                    />
                ) : null}
                <Button
                    type="text"
                    className="agent-header-skills"
                    shape="circle"
                    icon={<Sparkles className="size-4" />}
                    disabled={!skillsAvailable}
                    style={{ color: selectedSkillCount || skillsOpen ? theme.accent.primary : theme.node.muted }}
                    aria-label={skillsAvailable ? `打开 Skills 技能库${selectedSkillCount ? `，已启用 ${selectedSkillCount} 个` : ""}` : "当前业务未开放 Skills"}
                    aria-expanded={skillsOpen}
                    aria-haspopup="dialog"
                    title={skillsAvailable ? "打开 Skills 技能库" : "当前业务未开放 Skills"}
                    onClick={onSkills}
                />
                <Button type="text" shape="circle" icon={<MessageSquarePlus className="size-4" />} onClick={onNew} aria-label="新建对话" title="新建对话" />
                <Button type="text" shape="circle" icon={<History className="size-4" />} onClick={onHistory} aria-label="历史对话" title="历史对话" />
                <Dropdown
                    trigger={["click"]}
                    menu={{
                        items: [
                            { key: "settings", label: "Agent 设置", icon: <Settings2 className="size-4" />, onClick: onSettings },
                            ...(onResetLayout ? [{ key: "reset", label: docked ? "恢复默认宽度" : "恢复默认窗口", icon: <RotateCcw className="size-4" />, onClick: onResetLayout }] : []),
                            { key: "export", label: "导出 Agent 调试记录", title: "含对话、工具参数和审批，分享前请检查隐私", icon: <Download className="size-4" />, disabled: exporting, onClick: onExport },
                        ],
                    }}
                >
                    <Button type="text" shape="circle" icon={<Ellipsis className="size-4" />} aria-label="更多 Agent 操作" title="更多操作" />
                </Dropdown>
                <Button type="text" shape="circle" icon={<X className="size-4" />} onClick={onCollapse} aria-label="收起 Agent" title="收起" />
            </div>
        </header>
    );
}

export function AgentHistoryView({
    conversations,
    activeConversationId,
    theme,
    onBack,
    onNew,
    onOpen,
    onDelete,
}: {
    conversations: AgentConversationRecord[];
    activeConversationId: string;
    theme: CanvasTheme;
    onBack: () => void;
    onNew: () => void;
    onOpen: (conversation: AgentConversationRecord) => void;
    onDelete: (id: string) => void;
}) {
    return (
        <div className="canvas-agent-history-root flex min-h-0 min-w-0 flex-1 flex-col">
            <header className="agent-panel-header flex shrink-0 items-center gap-2">
                <Button type="text" shape="circle" icon={<ArrowLeft className="size-4" />} onClick={onBack} aria-label="返回对话" />
                <div className="min-w-0 flex-1">
                    <div className="text-sm font-semibold">历史对话</div>
                    <div className="mt-0.5 text-[11px] opacity-40">保存在当前账号与业务范围下</div>
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

export function launcherAppearance(): CanvasAppearance {
    return useAppearanceStore.getState().appearance.canvas || DEFAULT_CANVAS_APPEARANCE;
}
