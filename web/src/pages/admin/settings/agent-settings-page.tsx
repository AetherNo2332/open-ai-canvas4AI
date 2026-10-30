import { Alert, App, Button, InputNumber, Skeleton } from "antd";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { AdminPageFrame } from "../components/admin-shell";
import { RefreshCw, Save } from "lucide-react";
import AgentSkillDefaultsSection from "./components/agent-skill-defaults-section";
import AgentSubagentsAvailabilitySection from "./components/agent-subagents-availability-section";
import AgentToolCount from "./components/agent-tool-count";
import AgentWebSearchSection from "./components/agent-web-search-section";
import "./agent-settings-page.css";
import { getAgentSchedulerSetting, getAgentSchedulerStatus, updateAgentSchedulerSetting, type AgentRuntimeStatus, type AgentSchedulerDraft, type AgentSchedulerSetting } from "@/services/api/admin-agent-settings";

const fields: Array<{ key: keyof AgentSchedulerDraft; label: string; detail: string; max: number }> = [
    { key: "dispatchConcurrency", label: "Agent 调度槽", detail: "每个执行器同时推进的短步骤数；等待模型、工具或审批不占槽。", max: 16 },
    { key: "maxResidentSessions", label: "执行器驻留会话上限", detail: "每个执行器持有的会话上限；缩容后已有会话自然排空。", max: 64 },
    { key: "maxResidentPerUser", label: "用户驻留会话上限", detail: "同一用户跨画布、跨执行器共享的上限；缩容后已有会话自然排空。", max: 64 },
    { key: "maxResidentPerCanvas", label: "画布驻留会话上限", detail: "同一用户、同一画布跨执行器共享的上限，保留其他画布准入机会。", max: 64 },
];

export default function AgentSettingsPage() {
    const { message } = App.useApp();
    const mounted = useRef(true);
    const [toolCount, setToolCount] = useState<number>();
    const [setting, setSetting] = useState<AgentSchedulerSetting>();
    const [draft, setDraft] = useState<AgentSchedulerDraft>();
    const [configError, setConfigError] = useState("");
    const [workers, setWorkers] = useState<AgentRuntimeStatus[]>([]);
    const [saving, setSaving] = useState(false);
    const loadConfig = useCallback(async () => {
        try {
            const result = await getAgentSchedulerSetting();
            if (!mounted.current) return;
            setSetting(result.setting);
            setToolCount(result.toolCount);
            setDraft(result.setting);
            setConfigError("");
        } catch (error) {
            if (mounted.current) setConfigError(error instanceof Error ? error.message : "配置读取失败");
        }
    }, []);
    useEffect(() => {
        mounted.current = true;
        void loadConfig();
        const loadWorkers = () => {
            void getAgentSchedulerStatus()
                .then(({ instances }) => {
                    if (mounted.current) setWorkers(instances);
                })
                .catch(() => {});
        };
        loadWorkers();
        const timer = window.setInterval(loadWorkers, 15000);
        return () => {
            mounted.current = false;
            window.clearInterval(timer);
        };
    }, [loadConfig]);
    const valid =
        draft &&
        Number.isInteger(draft.dispatchConcurrency) &&
        draft.dispatchConcurrency >= 1 &&
        draft.dispatchConcurrency <= 16 &&
        Number.isInteger(draft.maxResidentSessions) &&
        draft.maxResidentSessions >= draft.dispatchConcurrency &&
        draft.maxResidentSessions <= 64 &&
        Number.isInteger(draft.maxResidentPerUser) &&
        draft.maxResidentPerUser >= 1 &&
        draft.maxResidentPerUser <= 64 &&
        Number.isInteger(draft.maxResidentPerCanvas) &&
        draft.maxResidentPerCanvas >= 1 &&
        draft.maxResidentPerCanvas <= draft.maxResidentSessions;
    const dirty = draft && setting && fields.some(({ key }) => draft[key] !== setting[key]);
    async function save() {
        if (!valid || !draft || !setting || saving) return;
        setSaving(true);
        try {
            const { setting: saved } = await updateAgentSchedulerSetting({
                dispatchConcurrency: draft.dispatchConcurrency,
                maxResidentSessions: draft.maxResidentSessions,
                maxResidentPerUser: draft.maxResidentPerUser,
                maxResidentPerCanvas: draft.maxResidentPerCanvas,
                expectedRevision: setting.revision,
            });
            setSetting(saved);
            setDraft(saved);
            setConfigError("");
            void message.success("配置已保存，等待执行器应用");
        } catch (error) {
            setConfigError(error instanceof Error ? error.message : "保存失败，调整已保留");
        } finally {
            setSaving(false);
        }
    }
    return (
        <AdminPageFrame title="Agent（beta）" description="统一管理 Agent 调度、动态子代理、联网搜索与默认技能" scroll>
            <div className="agent-settings">
                <nav className="agent-settings-nav" aria-label="Agent 配置区域">
                    <a href="#tools">工具总数</a>
                    <a href="#config">调度配置</a>
                    <a href="#subagents">动态子代理</a>
                    <a href="#web-search">联网搜索</a>
                    <a href="#skill-defaults">默认技能</a>
                </nav>
                <AgentToolCount count={configError ? undefined : toolCount} loading={!draft && !configError} />
                <section className="agent-settings-section space-y-2" aria-label="Agent Worker 状态">
                    <h2 className="text-base font-semibold">Worker 状态</h2>
                    {workers.length === 0 ? (
                        <p className="text-sm text-foreground/60">暂无 Worker 心跳；请检查 Agent 容器、令牌和后端连接。</p>
                    ) : (
                        workers.map((worker) => (
                            <p key={worker.instanceId} className="text-sm text-foreground/70">
                                {worker.instanceId} · {worker.online ? (worker.readiness === "ready" ? "就绪" : `在线但未就绪（${worker.readiness || "starting"}）`) : "离线"}
                                {worker.readinessReason ? ` · ${worker.readinessReason}` : ""} · {worker.resident}/{worker.capacity} 会话
                            </p>
                        ))
                    )}
                </section>
                <section id="config" className="agent-settings-section space-y-4" aria-labelledby="agent-config-heading">
                    <div className="agent-settings-heading">
                        <h2 id="agent-config-heading" className="text-base font-semibold">
                            调度配置
                        </h2>
                        <Button
                            icon={<RefreshCw size={14} />}
                            onClick={() => {
                                void loadConfig();
                            }}
                            disabled={saving}
                        >
                            重新读取配置
                        </Button>
                    </div>
                    <p className="text-sm text-foreground/60">
                        配置保存后自动生效；数据库设置优先于部署初始值。模型请求额度由
                        <Link to="/admin/settings/runtime-policy" className="underline">
                            资源与策略
                        </Link>
                        中的 Go Worker 和渠道并发控制。
                    </p>
                    {configError ? <Alert type="error" showIcon title={configError} /> : null}
                    {draft ? (
                        <div className="grid gap-4 md:grid-cols-3">
                            {fields.map((field) => (
                                <label key={field.key} className="agent-settings-field space-y-2 rounded-[var(--r-md)] border border-foreground/10 p-4">
                                    <span className="block text-sm font-medium">{field.label}</span>
                                    <InputNumber
                                        aria-label={field.label}
                                        min={1}
                                        max={field.max}
                                        precision={0}
                                        value={draft[field.key]}
                                        disabled={saving}
                                        onChange={(value) => setDraft((current) => (current ? { ...current, [field.key]: value ?? 0 } : current))}
                                    />
                                    <span className="block text-xs text-foreground/60">{field.detail}</span>
                                </label>
                            ))}
                        </div>
                    ) : !configError ? (
                        <Skeleton active />
                    ) : null}
                    {draft && !valid ? <Alert type="warning" title="驻留上限须不小于调度槽；画布上限须不大于驻留上限。" /> : null}
                    <div className="flex flex-wrap items-center gap-3">
                        <Button
                            type="primary"
                            icon={<Save size={14} />}
                            loading={saving}
                            disabled={!valid || !dirty}
                            onClick={() => {
                                void save();
                            }}
                        >
                            保存配置
                        </Button>
                        <span className="text-sm text-foreground/60">{setting ? `已保存配置修订号 ${setting.revision}${dirty ? " · 有未保存调整" : ""}` : "配置尚未读取"}</span>
                    </div>
                </section>
                <section id="subagents" className="agent-settings-section space-y-4" aria-labelledby="agent-subagents-heading">
                    <AgentSubagentsAvailabilitySection />
                </section>
                <section id="web-search" className="agent-settings-section space-y-4" aria-labelledby="agent-web-search-heading">
                    <AgentWebSearchSection />
                </section>
                <section id="skill-defaults" className="agent-settings-section" aria-labelledby="agent-skill-defaults-heading">
                    <AgentSkillDefaultsSection />
                </section>
                <section id="memory" className="agent-settings-section space-y-2" aria-labelledby="agent-memory-heading">
                    <h2 id="agent-memory-heading" className="text-base font-semibold">
                        用户记忆
                    </h2>
                    <p className="text-sm text-foreground/60">
                        每个用户一份长期记忆文件，由 Agent 用 memory_update 维护、用户本人确认或撤销。管理员只读查看见 <Link to="/admin/agent-lessons">Agent 记忆</Link>。
                    </p>
                </section>
            </div>
        </AdminPageFrame>
    );
}
