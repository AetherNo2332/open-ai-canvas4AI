import { Alert, App, Button, InputNumber, Skeleton } from "antd";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { AdminPageFrame } from "../components/admin-shell";
import AgentLessonsPanel from "../components/agent-lessons-panel";
import { RefreshCw, Save } from "lucide-react";
import AgentSkillDefaultsSection from "./components/agent-skill-defaults-section";
import AgentCrewAvailabilitySection from "./components/agent-crew-availability-section";
import "./agent-settings-page.css";
import { getAgentSchedulerSetting, updateAgentSchedulerSetting, type AgentSchedulerDraft, type AgentSchedulerSetting } from "@/services/api/admin-agent-settings";

const fields: Array<{ key: keyof AgentSchedulerDraft; label: string; detail: string; max: number }> = [
    { key: "dispatchConcurrency", label: "Agent 调度槽", detail: "每个执行器同时推进的短步骤数；等待模型、工具或审批不占槽。", max: 16 },
    { key: "maxResidentSessions", label: "执行器驻留会话上限", detail: "每个执行器持有的会话上限；缩容后已有会话自然排空。", max: 64 },
    { key: "maxResidentPerCanvas", label: "画布驻留会话上限", detail: "同一用户、同一画布跨执行器共享的上限，保留其他画布准入机会。", max: 64 },
];

export default function AgentSettingsPage() {
    const { message } = App.useApp();
    const location = useLocation();
    const memory = useRef<HTMLElement>(null);
    const mounted = useRef(true);
    const [setting, setSetting] = useState<AgentSchedulerSetting>();
    const [draft, setDraft] = useState<AgentSchedulerDraft>();
    const [configError, setConfigError] = useState("");
    const [saving, setSaving] = useState(false);
    const loadConfig = useCallback(async () => {
        try {
            const result = await getAgentSchedulerSetting();
            if (!mounted.current) return;
            setSetting(result.setting);
            setDraft(result.setting);
            setConfigError("");
        } catch (error) {
            if (mounted.current) setConfigError(error instanceof Error ? error.message : "配置读取失败");
        }
    }, []);
    useEffect(() => {
        mounted.current = true;
        void loadConfig();
        return () => {
            mounted.current = false;
        };
    }, [loadConfig]);
    useEffect(() => {
        if (location.hash === "#memory") memory.current?.scrollIntoView({ block: "start" });
    }, [location.hash]);
    const valid =
        draft &&
        Number.isInteger(draft.dispatchConcurrency) &&
        draft.dispatchConcurrency >= 1 &&
        draft.dispatchConcurrency <= 16 &&
        Number.isInteger(draft.maxResidentSessions) &&
        draft.maxResidentSessions >= draft.dispatchConcurrency &&
        draft.maxResidentSessions <= 64 &&
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
        <AdminPageFrame title="Agent（beta）" description="统一管理 Agent 调度、Crew 子代理、默认技能与用户记忆" scroll>
            <div className="agent-settings">
                <nav className="agent-settings-nav" aria-label="Agent 配置区域">
                    <a href="#config">调度配置</a>
                    <a href="#crew">Crew 子代理</a>
                    <a href="#skill-defaults">默认技能</a>
                    <a href="#memory">记忆管理</a>
                </nav>
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
                <section id="crew" className="agent-settings-section space-y-4" aria-labelledby="agent-crew-heading">
                    <AgentCrewAvailabilitySection />
                </section>
                <section id="skill-defaults" className="agent-settings-section" aria-labelledby="agent-skill-defaults-heading">
                    <AgentSkillDefaultsSection />
                </section>
                <section id="memory" ref={memory} className="agent-settings-section space-y-3">
                    <h2 className="text-base font-semibold">记忆管理</h2>
                    <AgentLessonsPanel />
                </section>
            </div>
        </AdminPageFrame>
    );
}
