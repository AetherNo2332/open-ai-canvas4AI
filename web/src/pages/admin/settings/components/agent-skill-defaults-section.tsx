import { Alert, App, Button, Input, Skeleton, Switch } from "antd";
import { ArrowDown, ArrowUp, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { getSkill, listSkills, type Skill } from "@/services/api/skills";
import { ApiError } from "@/services/api/request";
import { listAdminAgentSkillDefaults, updateAdminAgentSkillDefaults, type AgentSkillDefaultsViewItem } from "@/services/api/admin-skill-defaults";

const bytes = (value: number) => `${(value / 1024).toFixed(1)} KiB`;

export default function AgentSkillDefaultsSection() {
    const { message } = App.useApp();
    const [items, setItems] = useState<AgentSkillDefaultsViewItem[]>([]);
    const [revision, setRevision] = useState<number>();
    const [keyword, setKeyword] = useState("");
    const [results, setResults] = useState<Skill[]>([]);
    const [loading, setLoading] = useState(true);
    const [searching, setSearching] = useState(false);
    const [saving, setSaving] = useState(false);
    const [adding, setAdding] = useState(false);
    const [dirty, setDirty] = useState(false);
    const [error, setError] = useState("");
    const [conflict, setConflict] = useState(false);
    const query = useDebouncedValue(keyword.trim(), 250);
    const loadSeq = useRef(0);
    const searchSeq = useRef(0);
    const load = useCallback(async () => {
        const seq = ++loadSeq.current;
        setLoading(true);
        try {
            const view = await listAdminAgentSkillDefaults();
            if (seq !== loadSeq.current) return;
            setItems(view.items);
            setRevision(view.revision);
            setDirty(false);
            setError("");
            setConflict(false);
        } catch (cause) {
            if (seq === loadSeq.current) setError(cause instanceof Error ? cause.message : "默认技能读取失败");
        } finally {
            if (seq === loadSeq.current) setLoading(false);
        }
    }, []);
    useEffect(() => {
        void load();
        return () => {
            loadSeq.current++;
        };
    }, [load]);
    useEffect(() => {
        const seq = ++searchSeq.current;
        setResults([]);
        if (!query) {
            setSearching(false);
            return;
        }
        setSearching(true);
        void listSkills({ search: query, scope: "public", pageSize: 20 })
            .then((view) => {
                if (seq === searchSeq.current) setResults(view.skills.filter((skill) => !skill.isPrivate && skill.status === 1));
            })
            .catch((cause) => {
                if (seq === searchSeq.current) setError(cause instanceof Error ? cause.message : "搜索技能失败");
            })
            .finally(() => {
                if (seq === searchSeq.current) setSearching(false);
            });
        return () => {
            searchSeq.current++;
        };
    }, [query]);
    function change(next: AgentSkillDefaultsViewItem[]) {
        setItems(next);
        setDirty(true);
    }
    async function add(skill: Skill) {
        if (saving || loading || adding || items.some((item) => item.skillId === skill.skillId)) return;
        setAdding(true);
        try {
            const { skill: detail } = await getSkill(skill.skillId);
            setItems((current) =>
                current.some((item) => item.skillId === detail.skillId)
                    ? current
                    : [
                          ...current,
                          {
                              skillId: detail.skillId,
                              skillVersionId: detail.versionId,
                              skillName: detail.skillName,
                              versionLabel: detail.version,
                              position: current.length,
                              enabled: 1,
                              status: detail.status,
                              fileCount: detail.fileCount,
                              totalBytes: detail.totalBytes,
                          },
                      ],
            );
            setDirty(true);
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : "添加技能失败");
        } finally {
            setAdding(false);
        }
    }
    function move(index: number, direction: number) {
        const next = [...items];
        [next[index], next[index + direction]] = [next[index + direction], next[index]];
        change(next);
    }
    async function save() {
        if (revision === undefined || saving || loading || adding || conflict) return;
        setSaving(true);
        try {
            const result = await updateAdminAgentSkillDefaults({ revision, items: items.map((item, position) => ({ skillId: item.skillId, skillVersionId: item.skillVersionId, position, enabled: item.enabled })) });
            setRevision(result.revision);
            setDirty(false);
            setError("");
            void message.success("默认技能已保存，新会话将使用此配置");
        } catch (cause) {
            const isConflict = cause instanceof ApiError && cause.reason === "agent_skill_defaults_revision_conflict";
            setConflict(isConflict);
            const details = cause instanceof ApiError && cause.details ? `（${JSON.stringify(cause.details)}）` : "";
            setError(`${cause instanceof Error ? cause.message : "保存失败"}${details}${isConflict ? "；请重新加载配置后再编辑。" : ""}`);
        } finally {
            setSaving(false);
        }
    }
    const enabled = items.filter((item) => item.enabled === 1);
    const totalFiles = enabled.reduce((sum, item) => sum + item.fileCount, 0);
    const totalBytes = enabled.reduce((sum, item) => sum + item.totalBytes, 0);
    const busy = saving || loading || adding;
    return (
        <div className="agent-skill-defaults space-y-4">
            <div className="agent-settings-heading">
                <h2 id="agent-skill-defaults-heading" className="text-base font-normal">
                    默认技能
                </h2>
                <Button
                    icon={<RefreshCw size={14} />}
                    disabled={saving || adding}
                    onClick={() => {
                        void load();
                    }}
                >
                    重新加载配置
                </Button>
            </div>
            <p className="text-sm text-foreground/60 max-w-[60ch]">全局默认技能自动加入新会话，无需用户安装。保存后已有会话的技能版本保持冻结；容量由后端统一校验。</p>
            {error ? <Alert type="error" showIcon title={error} /> : null}
            <Input aria-label="搜索默认技能" placeholder="搜索公开技能…" value={keyword} disabled={busy} onChange={(event) => setKeyword(event.target.value)} />
            {searching ? <p className="text-sm opacity-60">搜索中…</p> : null}
            {query && !searching && results.length === 0 ? <p className="text-sm opacity-60">未找到可用的公开技能</p> : null}
            <ul className="agent-skill-defaults-results">
                {results.map((skill) => (
                    <li key={skill.skillId}>
                        <span>{skill.skillName}</span>
                        <Button
                            icon={<Plus size={14} />}
                            disabled={busy || items.some((item) => item.skillId === skill.skillId)}
                            onClick={() => {
                                void add(skill);
                            }}
                        >
                            加入默认
                        </Button>
                    </li>
                ))}
            </ul>
            {loading ? (
                <Skeleton active />
            ) : (
                <ol className="agent-skill-defaults-items">
                    {items.map((item, index) => (
                        <li key={item.skillId}>
                            <div className="agent-skill-defaults-description">
                                <span>{item.skillName || item.skillId}</span>
                                <span className="text-xs opacity-60">
                                    {item.versionLabel || item.skillVersionId} · {item.fileCount} 文件 · {bytes(item.totalBytes)}
                                </span>
                                {item.status !== 1 ? <span className="text-xs text-[var(--palette-status-error)]">技能不可用，请移除或替换</span> : null}
                            </div>
                            <div className="agent-skill-defaults-actions">
                                <Switch aria-label={`启用 ${item.skillName}`} checked={item.enabled === 1} disabled={busy} onChange={(checked) => change(items.map((row, i) => (i === index ? { ...row, enabled: checked ? 1 : 0 } : row)))} />
                                <Button aria-label={`上移 ${item.skillName}`} icon={<ArrowUp size={14} />} disabled={busy || index === 0} onClick={() => move(index, -1)} />
                                <Button aria-label={`下移 ${item.skillName}`} icon={<ArrowDown size={14} />} disabled={busy || index === items.length - 1} onClick={() => move(index, 1)} />
                                <Button aria-label={`移除 ${item.skillName}`} icon={<Trash2 size={14} />} disabled={busy} onClick={() => change(items.filter((_, i) => i !== index))} />
                            </div>
                        </li>
                    ))}
                </ol>
            )}
            {!loading && items.length === 0 ? <p className="text-sm opacity-60">尚未配置默认技能</p> : null}
            <p className="agent-skill-defaults-totals text-sm">
                合计：启用 {enabled.length} 个 · {totalFiles} 文件 · {bytes(totalBytes)}；上下文预估 {bytes(totalBytes)}（技能包整体读入的保守上限）
            </p>
            <div className="flex flex-wrap items-center gap-3">
                <Button
                    type="primary"
                    icon={<Save size={14} />}
                    loading={saving}
                    disabled={busy || revision === undefined || !dirty || conflict}
                    onClick={() => {
                        void save();
                    }}
                >
                    保存默认技能
                </Button>
                <span className="text-sm opacity-60">{revision === undefined ? "配置尚未读取" : `配置修订号 ${revision}${dirty ? " · 有未保存调整" : ""}`}</span>
            </div>
        </div>
    );
}
