import type { ReactNode } from "react";
import { Button, Checkbox, Input } from "antd";
import { ArrowLeft, BookMarked, Check, Cpu, Gauge, ListOrdered, LockKeyhole, ShieldCheck, Sparkles } from "lucide-react";
import { ModelPicker } from "@/components/model-picker";
import { ThinkingLevelSlider } from "@/components/thinking-level-slider";
import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { resolveThinkingLevel, thinkingLevelsFor, type ThinkingRequest } from "@/lib/text-thinking";
import type { CanvasTheme } from "@/lib/agent/theme";
import type { AgentCapabilities, AgentPermissionMode } from "@/sdk/agent";
import type { Skill } from "@/services/api/skills";
import type { AiConfig } from "@/stores/use-config-store";
import { agentPermissionLabel, agentPermissionVisual } from "./agent-panel-controls";

export type AgentSettingsContextKey = "canvas" | "resources" | "generation_history" | "skills" | "project";

export function AgentPanelSettings({
    theme,
    config,
    selectedModel,
    onModelChange,
    reasoningMode,
    onReasoningModeChange,
    permissionMode,
    onPermissionChange,
    capabilities,
    sendMode,
    sendModeSaving,
    sendModeError,
    onSendModeChange,
    onSendModeRetry,
    skills,
    defaultSkills,
    selectedSkillIds,
    allowUserSkills,
    onSkillToggle,
    onOpenSkillLibrary,
    onBack,
    extension,
    modelFilter,
}: {
    theme: CanvasTheme;
    config: AiConfig;
    selectedModel: string;
    onModelChange: (model: string) => void;
    reasoningMode: ThinkingRequest;
    onReasoningModeChange: (value: ThinkingRequest) => void;
    permissionMode: AgentPermissionMode;
    onPermissionChange: (mode: AgentPermissionMode) => void;
    capabilities: AgentCapabilities;
    sendMode: "queue" | "interject";
    sendModeSaving: boolean;
    sendModeError?: string;
    onSendModeChange: (mode: "queue" | "interject") => void;
    onSendModeRetry: () => void;
    skills: Skill[];
    defaultSkills: Array<{ skillId: string; skillName: string }>;
    selectedSkillIds: string[];
    allowUserSkills: boolean;
    onSkillToggle: (skillId: string) => void;
    onOpenSkillLibrary: () => void;
    onBack: () => void;
    extension?: ReactNode;
    modelFilter?: (model: string) => boolean;
}) {
    const thinking = (() => {
        try {
            return modelCapabilityConfigFor(config, selectedModel).text?.thinking;
        } catch {
            return undefined;
        }
    })();
    const permissionVisual = agentPermissionVisual(permissionMode);
    const PermissionIcon = permissionVisual.icon;
    return (
        <div className="canvas-agent-settings flex min-h-0 min-w-0 flex-1 flex-col" style={{ background: theme.node.panel }}>
            <header className="agent-panel-header flex shrink-0 items-center gap-2">
                <Button type="text" shape="circle" icon={<ArrowLeft className="size-4" />} onClick={onBack} aria-label="返回对话" />
                <div className="min-w-0 flex-1">
                    <div className="text-sm font-semibold">Agent 设置</div>
                    <div className="mt-0.5 text-[11px] opacity-40">发送偏好与任务配置</div>
                </div>
                <span className="rounded-full px-2 py-1 text-[10px] opacity-50" style={{ background: theme.node.fill }}>
                    {selectedSkillIds.length} 已启用
                </span>
            </header>
            <div className="canvas-agent-settings-scroll thin-scrollbar min-h-0 flex-1 space-y-6 overflow-y-auto p-4">
                <section aria-label="默认发送方式">
                    <SettingLabel label="默认发送方式" hint={sendModeSaving ? "保存中…" : "账号同步"} />
                    <div className="grid grid-cols-2 gap-2">
                        {(
                            [
                                { value: "interject", label: "插队", description: "优先把新要求交给当前任务。", icon: ListOrdered },
                                { value: "queue", label: "排队", description: "等当前任务结束，再按顺序发送。", icon: ListOrdered },
                            ] as const
                        ).map(({ value, label, description, icon: Icon }) => (
                            <button
                                type="button"
                                key={value}
                                aria-pressed={sendMode === value}
                                disabled={sendModeSaving}
                                onClick={() => onSendModeChange(value)}
                                className="rounded-xl p-3 text-left disabled:opacity-50"
                                style={{ background: theme.node.fill, border: `1px solid ${sendMode === value ? theme.accent.primary : "transparent"}` }}
                            >
                                <span className="flex items-center gap-2 text-xs font-semibold">
                                    <Icon aria-hidden="true" className="size-4" />
                                    {label}
                                </span>
                                <span className="mt-2 block text-xs leading-5" style={{ color: theme.node.muted }}>
                                    {description}
                                </span>
                            </button>
                        ))}
                    </div>
                    <p className="mt-2 text-xs leading-5" style={{ color: theme.node.muted }}>
                        Enter 或点击发送使用默认方式；Ctrl/⌘+Enter 仅本次使用相反方式，Shift+Enter 换行。
                    </p>
                    {sendModeError ? (
                        <div role="alert" className="mt-2 text-xs">
                            <span>{sendModeError}</span>
                            <Button type="link" size="small" onClick={onSendModeRetry}>
                                重试
                            </Button>
                        </div>
                    ) : null}
                </section>
                <section>
                    <SettingLabel label="推理模式" hint="新运行生效" />
                    <ThinkingLevelSlider levels={thinkingLevelsFor(thinking)} value={reasoningMode} resolvedLevel={resolveThinkingLevel(thinking, reasoningMode)} onChange={onReasoningModeChange} />
                </section>
                <section>
                    <SettingLabel label="对话模型" hint="新运行生效" />
                    <div className="min-w-0 rounded-xl px-2 py-1.5" style={{ background: theme.node.fill }}>
                        <ModelPicker
                            config={config}
                            value={selectedModel}
                            capability="text"
                            onChange={onModelChange}
                            variant="creation"
                            fullWidth
                            showSelectedPrice
                            showOptionPrices
                            placeholder="选择文本模型"
                            className="!border-0 !bg-transparent !shadow-none"
                            popoverClassName="agent-model-picker-popover"
                            modelFilter={modelFilter}
                        />
                    </div>
                </section>
                <section>
                    <SettingLabel label="执行权限" hint="由服务端能力限制" />
                    <div className="flex items-center gap-3 rounded-xl p-3" style={{ background: theme.node.fill }}>
                        <PermissionIcon className="size-4" style={{ color: permissionVisual.color }} />
                        <div className="min-w-0 flex-1">
                            <div className="text-xs font-medium">{agentPermissionLabel(permissionMode)}</div>
                            <div className="mt-0.5 text-[11px]" style={{ color: theme.node.muted }}>
                                {capabilities.permissionModes.map(agentPermissionLabel).join(" / ") || "只读"}
                            </div>
                        </div>
                        <div className="flex gap-1">
                            {capabilities.permissionModes.map((mode) =>
                                mode === permissionMode ? null : (
                                    <Button key={mode} size="small" onClick={() => onPermissionChange(mode)}>
                                        {agentPermissionLabel(mode)}
                                    </Button>
                                ),
                            )}
                        </div>
                    </div>
                </section>
                <section>
                    <SettingLabel label="技能" hint={allowUserSkills ? "可选择已安装技能" : "仅默认技能可用"} />
                    <div className="space-y-1">
                        {defaultSkills.map((skill) => (
                            <div key={`default-${skill.skillId}`} className="canvas-agent-skill-row flex items-start gap-3 rounded-xl p-3" style={{ background: theme.node.fill }}>
                                <LockKeyhole className="mt-0.5 size-4 shrink-0" style={{ color: theme.node.muted }} />
                                <div className="min-w-0 flex-1">
                                    <div className="text-[13px] font-medium">{skill.skillName}</div>
                                    <div className="mt-1 text-xs" style={{ color: theme.node.muted }}>
                                        默认技能 · 自动生效，不可移除
                                    </div>
                                </div>
                            </div>
                        ))}
                        {skills.map((skill) => {
                            const selected = selectedSkillIds.includes(skill.skillId);
                            return (
                                <label key={skill.skillId} className={`flex items-start gap-3 rounded-xl p-3 ${allowUserSkills ? "cursor-pointer" : "opacity-55"}`} style={{ background: theme.node.fill }}>
                                    <Checkbox checked={selected} disabled={!allowUserSkills} onChange={() => onSkillToggle(skill.skillId)} aria-label={`启用 ${skill.skillName}`} />
                                    <span className="min-w-0 flex-1">
                                        <span className="block text-[13px] font-medium">{skill.skillName}</span>
                                        <span className="mt-1 line-clamp-2 block text-xs" style={{ color: theme.node.muted }}>
                                            {skill.description || "暂无技能说明"}
                                        </span>
                                    </span>
                                </label>
                            );
                        })}
                        <Button block disabled={!capabilities.skills} onClick={onOpenSkillLibrary}>
                            <Sparkles className="size-3.5" />
                            {capabilities.skills ? "打开 Skills 技能库" : "当前业务未开放 Skills"}
                        </Button>
                    </div>
                </section>
                <section>
                    <SettingLabel label="能力与范围" />
                    <div className="space-y-1 rounded-xl border border-black/10 p-3 text-xs" style={{ color: theme.node.muted }}>
                        <p>
                            模型要求：
                            {capabilities.modelRequirements
                                ? [
                                      capabilities.modelRequirements.text ? "文本" : "",
                                      capabilities.modelRequirements.toolCalling ? "工具调用" : "",
                                      capabilities.modelRequirements.streamOutput ? "流式输出" : "",
                                      capabilities.modelRequirements.vision ? "图片输入" : "",
                                  ]
                                      .filter(Boolean)
                                      .join(" / ")
                                : "服务端未声明"}
                        </p>
                        <p>用户自选技能：{allowUserSkills ? "允许" : "不可用"}</p>
                        <p>写入能力：{capabilities.writeTools ? "已授权" : "只读"}</p>
                    </div>
                </section>
                {extension}
            </div>
        </div>
    );
}

function SettingLabel({ label, hint }: { label: string; hint?: string }) {
    return (
        <div className="mb-2 flex items-center justify-between text-xs font-semibold">
            <span>{label}</span>
            {hint ? <span className="text-[10px] font-normal opacity-40">{hint}</span> : null}
        </div>
    );
}
