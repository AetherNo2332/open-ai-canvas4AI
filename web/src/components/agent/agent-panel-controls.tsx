import { useState, type CSSProperties, type ReactNode } from "react";
import { Button, Dropdown, Input, Popover } from "antd";
import { Check, ChevronDown, LoaderCircle, LockKeyhole, ShieldCheck, Sparkles } from "lucide-react";
import { ModelPicker } from "@/components/model-picker";
import { ThinkingLevelSlider } from "@/components/thinking-level-slider";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { resolveThinkingLevel, THINKING_LEVEL_LABELS, thinkingLevelsFor, type ThinkingRequest } from "@/lib/text-thinking";
import { canvasThemes, type CanvasTheme } from "@/lib/agent/theme";
import { presentAgentContextUsage, type AgentContextUsage, type AgentContextUsageView } from "@/lib/agent/context-usage";
import { useAgentTheme } from "@/lib/agent/use-agent-theme";
import { usePopoverAnchorAlign } from "@/hooks/use-popover-anchor-align";
import type { AgentApprovalSnapshot, AgentPermissionMode, AgentRunRequest } from "@/sdk/agent";
import type { useEffectiveConfig } from "@/stores/use-config-store";

const permissionOptions: Array<{ value: AgentPermissionMode; label: string; description: string; icon: typeof ShieldCheck; color: string }> = [
    { value: "read_only", label: "只读", description: "只分析和建议", icon: LockKeyhole, color: "var(--primary)" },
    { value: "request_approval", label: "请求审批", description: "写入和生成前确认", icon: ShieldCheck, color: "var(--palette-status-warning)" },
    { value: "auto", label: "自动执行", description: "预算内执行已授权工具", icon: Sparkles, color: "var(--palette-status-success)" },
];

export function agentPermissionLabel(mode: AgentPermissionMode) {
    return permissionOptions.find((option) => option.value === mode)?.label || "请求审批";
}

export function agentPermissionVisual(mode: AgentPermissionMode) {
    const option = permissionOptions.find((item) => item.value === mode) || permissionOptions[0];
    return { color: option.color, soft: `${option.color}1f`, icon: option.icon };
}

export function agentPermissionMenuItems(mode: AgentPermissionMode, onChange: (mode: AgentPermissionMode) => void, modes?: AgentPermissionMode[]) {
    return permissionOptions
        .filter((option) => !modes || modes.includes(option.value))
        .map((option) => ({
            key: option.value,
            label: <span>{option.label}</span>,
            icon: mode === option.value ? <Check className="size-3.5" style={{ color: option.color }} /> : <option.icon className="size-3.5" style={{ color: option.color }} />,
            onClick: () => onChange(option.value),
        }));
}

export function AgentComposerControls({
    config,
    selectedModel,
    permissionMode,
    theme,
    onModelChange,
    onPermissionChange,
    reasoningMode,
    onReasoningModeChange,
    modelEnabled = true,
    reasoningEnabled = true,
    permissionModes,
    modelFilter,
}: {
    config: ReturnType<typeof useEffectiveConfig>;
    selectedModel: string;
    permissionMode: AgentPermissionMode;
    theme: CanvasTheme;
    onModelChange: (model: string) => void;
    onPermissionChange: (mode: AgentPermissionMode) => void;
    reasoningMode: ThinkingRequest;
    onReasoningModeChange: (mode: ThinkingRequest) => void;
    modelEnabled?: boolean;
    reasoningEnabled?: boolean;
    permissionModes?: AgentPermissionMode[];
    modelFilter?: (model: string) => boolean;
}) {
    const permissionVisual = agentPermissionVisual(permissionMode);
    const PermissionIcon = permissionVisual.icon;
    const [thinkingOpen, setThinkingOpen] = useState(false);
    const thinkingAnchor = usePopoverAnchorAlign(thinkingOpen);
    const thinking = modelCapabilityConfigFor(config, selectedModel).text?.thinking;
    const levels = reasoningEnabled ? thinkingLevelsFor(thinking) : [];
    const thinkingLabel = reasoningMode === "auto" ? "默认" : reasoningMode === "deep" ? "深入" : THINKING_LEVEL_LABELS[reasoningMode];
    return (
        <div className="agent-composer-selection flex min-w-0 flex-1 flex-nowrap items-center gap-0.5">
            {modelEnabled ? (
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
                    modelFilter={modelFilter}
                />
            ) : null}
            {levels.length ? (
                <Popover
                    ref={thinkingAnchor}
                    open={thinkingOpen}
                    onOpenChange={setThinkingOpen}
                    trigger="click"
                    placement="top"
                    arrow={false}
                    content={
                        <div className="agent-thinking-popover">
                            <ThinkingLevelSlider levels={levels} value={reasoningMode} resolvedLevel={resolveThinkingLevel(thinking, reasoningMode)} onChange={onReasoningModeChange} />
                        </div>
                    }
                >
                    <button type="button" aria-label={`选择推理强度：${thinkingLabel}`} aria-expanded={thinkingOpen} className="agent-thinking-trigger" onPointerDown={(event) => event.stopPropagation()}>
                        <span>{thinkingLabel}</span>
                        <ChevronDown className="size-3" aria-hidden="true" />
                    </button>
                </Popover>
            ) : null}
            <Dropdown trigger={["click"]} placement="topLeft" menu={{ items: agentPermissionMenuItems(permissionMode, onPermissionChange, permissionModes) }}>
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

const CONTEXT_PHASE_LABEL: Record<AgentContextUsageView["phase"], string> = {
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

export function AgentContextRing({ usage }: { usage: AgentContextUsage }) {
    const view = presentAgentContextUsage(usage);
    const [open, setOpen] = useState(false);
    const theme = useAgentTheme();
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
                        <span>缓存命中率 {view.cacheHitRate === undefined ? "暂无数据" : `${Math.round(view.cacheHitRate * 1000) / 10}%`}</span>
                    </div>
                    {view.lastCompaction ? <p className="agent-context-note">本轮已完成一次上下文压缩，下一次读数会刷新。</p> : null}
                </div>
            }
        >
            <button
                type="button"
                className={`agent-context-ring is-${view.phase}`}
                aria-label={`${usageHeading}，${phaseLabel}。点击查看明细`}
                aria-expanded={open}
                title="查看上下文用量"
                onPointerDown={(event) => event.stopPropagation()}
                data-agent-context-ring
            >
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

export function AgentApprovalCard({
    approval,
    theme,
    submitting,
    reason,
    onReasonChange,
    onApprove,
    onReject,
    accessory,
    onFocusNode,
}: {
    approval: AgentApprovalSnapshot;
    theme: CanvasTheme;
    submitting: boolean;
    reason: string;
    onReasonChange: (value: string) => void;
    onApprove: () => void;
    onReject: () => void;
    accessory?: ReactNode;
    onFocusNode?: (nodeId: string) => void;
}) {
    const [showReason, setShowReason] = useState(Boolean(reason));
    const preview = approval.preview as
        | {
              title?: string;
              description?: string;
              items?: Array<{
                  operation?: string;
                  nodeId?: string;
                  nodeTitle?: string;
                  resultTitle?: string;
                  nodeTypeLabel?: string;
                  targetNodeId?: string;
                  targetNodeTitle?: string;
                  fields?: string[];
                  details?: string[];
                  summary?: string;
              }>;
          }
        | undefined;
    const items = Array.isArray(preview?.items) ? preview.items : [];
    return (
        <section className="canvas-agent-approval-card" aria-label={preview?.title || "Agent 请求审批"}>
            <div className="canvas-agent-approval-header">
                <span className="canvas-agent-approval-icon" aria-hidden="true">
                    <ShieldCheck className="size-4" />
                </span>
                <h3>{preview?.title || "Agent 请求写入"}</h3>
                <span className="canvas-agent-approval-badge">等待你的确认</span>
            </div>
            <p className="canvas-agent-approval-description" style={{ color: theme.node.muted }}>
                {preview?.description || "批准后才会执行本次业务写入。"}
            </p>
            {items.length ? (
                <div className="canvas-agent-approval-items" aria-label="涉及对象">
                    {items.map((item, index) => {
                        const operationLabel = approvalOperationLabel(item.operation);
                        const targetTitle = item.targetNodeTitle || item.nodeTitle;
                        const targetId = item.targetNodeId || item.nodeId;
                        return (
                            <article key={`${item.operation || "item"}-${item.nodeId || item.nodeTitle || index}-${index}`} className="canvas-agent-approval-item">
                                <div className="canvas-agent-approval-item-main">
                                    <span className="canvas-agent-approval-operation">{operationLabel}</span>
                                    <div className="canvas-agent-approval-node-summary">
                                        {targetTitle ? (
                                            targetId && onFocusNode ? (
                                                <button type="button" className="canvas-agent-approval-node canvas-agent-approval-node-button" onClick={() => onFocusNode(targetId)} title="定位到业务对象">
                                                    <span className="canvas-agent-approval-node-title">{targetTitle}</span>
                                                    {item.nodeTypeLabel ? <span className="canvas-agent-approval-node-type">{item.nodeTypeLabel}</span> : null}
                                                </button>
                                            ) : (
                                                <span className="canvas-agent-approval-node">
                                                    <span className="canvas-agent-approval-node-title">{targetTitle}</span>
                                                    {item.nodeTypeLabel ? <span className="canvas-agent-approval-node-type">{item.nodeTypeLabel}</span> : null}
                                                </span>
                                            )
                                        ) : null}
                                        {item.resultTitle ? <span className="canvas-agent-approval-result-title">《{item.resultTitle}》</span> : null}
                                    </div>
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
                                {item.summary ? <div className="canvas-agent-approval-summary">{item.summary}</div> : null}
                            </article>
                        );
                    })}
                </div>
            ) : approval.call ? (
                <pre className="max-h-40 overflow-auto rounded-lg bg-black/[0.04] p-2 text-[11px]">
                    {approval.call.function.name}
                    {"\n"}
                    {approval.call.function.arguments}
                </pre>
            ) : null}
            {accessory}
            <button type="button" className="canvas-agent-approval-reason-toggle" aria-expanded={showReason} onClick={() => setShowReason((value) => !value)} disabled={submitting}>
                {showReason ? "收起拒绝理由" : "填写拒绝理由（可选）"}
            </button>
            {showReason ? (
                <Input.TextArea
                    className="canvas-agent-approval-reason"
                    value={reason}
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
                <button type="button" className="canvas-agent-approval-approve" disabled={submitting} onClick={onApprove}>
                    {submitting ? <LoaderCircle className="size-4 animate-spin motion-reduce:animate-none" aria-hidden="true" /> : <Check className="size-4" aria-hidden="true" />}
                    {submitting ? "正在提交" : "同意执行"}
                </button>
            </div>
        </section>
    );
}

const APPROVAL_OPERATION_LABELS: Record<string, string> = {
    add_node: "新增节点",
    update_node: "修改节点",
    delete_node: "删除节点",
    duplicate_node: "复制节点",
    connect_nodes: "连接节点",
    delete_connection: "删除连接",
    update_connection: "修改连接",
    set_parent: "调整归属",
    replace_text: "替换文本",
    reorder_nodes: "调整顺序",
    reorder_rows: "调整表格",
    arrange_nodes: "整理画布",
    generate_media: "生成媒体",
    create_storyboard: "创建分镜",
    edit_storyboard: "修改分镜",
    edit_drawing: "修改绘图",
    bind_asset: "替换素材",
    canvas_undo: "撤销画布",
    canvas_redo: "重做画布",
    create_character: "创建角色",
    plan_step: "执行步骤",
    director_read: "读取导演台",
    director_preview: "录制预演",
};

function approvalOperationLabel(operation?: string) {
    return (operation && APPROVAL_OPERATION_LABELS[operation]) || operation || "业务操作";
}

export function AgentComposerError({ children }: { children: ReactNode }) {
    return <div className="agent-message-queue agent-message-queue-error">{children}</div>;
}

export { permissionOptions };
