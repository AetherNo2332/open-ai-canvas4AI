import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Checkbox, Input } from "antd";
import { getAgentWorkspace, replaceWorkspaceSkills, updateAgentWorkspace, type AgentWorkspaceView, type WorkspaceSkillSelection } from "@/services/api/agent-workspace";
import { listAgentSkillDefaults } from "@/services/api/agent";
import { ApiError } from "@/services/api/request";
import type { Skill } from "@/services/api/skills";
import { useUserStore } from "@/stores/use-user-store";
import "./canvas-agent-workspace-settings.css";

type DefaultSkill = { skillId: string; skillName: string };
type FormProps = {
    view: AgentWorkspaceView;
    defaultSkills: DefaultSkill[];
    availableSkills: Skill[];
    saving: boolean;
    onSaveDocument: (revision: number, content: string) => Promise<AgentWorkspaceView>;
    onSaveSkills: (revision: number, skills: WorkspaceSkillSelection[]) => Promise<AgentWorkspaceView>;
    onReload: () => Promise<void>;
};

export function workspaceDocumentBytes(content: string) { return new TextEncoder().encode(content).byteLength; }

export function WorkspaceSettingsForm(props: FormProps) {
    const [view, setView] = useState(props.view);
    const [document, setDocument] = useState(props.view.agentsMd);
    const [skills, setSkills] = useState<WorkspaceSkillSelection[]>(props.view.skills);
    const [error, setError] = useState("");
    const [conflict, setConflict] = useState(false);
    const [pending, setPending] = useState(false);
    const bytes = workspaceDocumentBytes(document);
    const disabled = props.saving || pending;
    const save = async (kind: "document" | "skills") => {
        setError(""); setConflict(false); setPending(true);
        try {
            const saved = kind === "document" ? await props.onSaveDocument(view.revision, document) : await props.onSaveSkills(view.revision, skills.map((skill, position) => ({ skillId: skill.skillId, skillVersionId: skill.skillVersionId, position, enabled: skill.enabled })));
            setView(saved);
            if (kind === "document") setDocument(saved.agentsMd);
            else setSkills(saved.skills);
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : String(cause));
            setConflict(cause instanceof ApiError && cause.reason === "agent_workspace_revision_conflict");
        } finally { setPending(false); }
    };
    const reload = async () => { setPending(true); try { await props.onReload(); } catch (cause) { setError(cause instanceof Error ? cause.message : String(cause)); } finally { setPending(false); } };
    const toggle = (skill: Skill) => setSkills((current) => current.some((item) => item.skillId === skill.skillId) ? current.filter((item) => item.skillId !== skill.skillId) : [...current, { skillId: skill.skillId, skillVersionId: skill.versionId, position: current.length, enabled: true }]);
    const publicSkills = props.availableSkills.filter((skill) => !skill.isPrivate && skill.status === 1);
    return <div className="agent-workspace-settings thin-scrollbar">
        <section>
            <h3>项目规则 · Workspace</h3>
            <p>仅影响新 Run。运行中的文档和技能版本保持创建时的快照。</p>
            <dl className="agent-workspace-facts"><div><dt>Revision</dt><dd>{view.revision}</dd></div><div><dt>Agents.md SHA-256</dt><dd>{view.agentsMdHash}</dd></div></dl>
            <label htmlFor="agent-workspace-document">Agents.md</label>
            <Input.TextArea id="agent-workspace-document" aria-label="Workspace Agents.md" value={document} onChange={(event) => setDocument(event.target.value)} autoSize={{ minRows: 8, maxRows: 20 }} disabled={disabled} />
            <div className="agent-workspace-actions"><span className="tabular-nums">{bytes} / 65536 字节</span><Button onClick={() => void save("document")} disabled={disabled || bytes > 65536 || document === view.agentsMd}>保存文档</Button></div>
            {bytes > 65536 ? <p role="alert">超过 64KB，请缩短文档。</p> : null}
        </section>
        <section>
            <h3>技能来源与版本</h3>
            <p>全局默认 {props.defaultSkills.length} 个 · Workspace {skills.filter((skill) => skill.enabled).length} 个。会话追加的版本优先。</p>
            <ul className="agent-workspace-skills">
                {props.defaultSkills.filter((skill) => !skills.some((item) => item.skillId === skill.skillId && item.enabled)).map((skill) => <li key={`global-${skill.skillId}`}><span>{skill.skillName}</span><small>全局默认</small></li>)}
                {skills.map((item) => <li key={item.skillId}><span>{view.skills.find((skill) => skill.skillId === item.skillId)?.skillName || publicSkills.find((skill) => skill.skillId === item.skillId)?.skillName || item.skillId}</span><small>Workspace · {item.skillVersionId}{!item.enabled ? " · 已禁用" : ""}</small><Button type="text" disabled={disabled} aria-label={`移除 Workspace 技能 ${item.skillId}`} onClick={() => setSkills((current) => current.filter((skill) => skill.skillId !== item.skillId))}>移除</Button></li>)}
            </ul>
            <fieldset disabled={disabled}><legend>追加已安装的公开技能</legend>{publicSkills.map((skill) => <Checkbox key={skill.skillId} checked={skills.some((item) => item.skillId === skill.skillId)} onChange={() => toggle(skill)}>{skill.skillName} <small>{skill.version}</small></Checkbox>)}{!publicSkills.length ? <p>请先从技能库安装公开技能。</p> : null}</fieldset>
            <Button onClick={() => void save("skills")} disabled={disabled}>保存技能</Button>
        </section>
        {error ? <div role="alert"><p>{error}</p>{conflict ? <Button onClick={() => void reload()} disabled={disabled}>重新读取配置（替换当前草稿）</Button> : null}</div> : null}
    </div>;
}

export function CanvasAgentWorkspaceSettings({ canvasId, availableSkills }: { canvasId: string; availableSkills: Skill[] }) {
    const userId = useUserStore((state) => state.user?.id);
    return <WorkspaceSettingsLoader key={`${userId || "anonymous"}:${canvasId}`} userId={userId} canvasId={canvasId} availableSkills={availableSkills} />;
}

function WorkspaceSettingsLoader({ userId, canvasId, availableSkills }: { userId?: string; canvasId: string; availableSkills: Skill[] }) {
    const client = useQueryClient();
    const key = ["agent-workspace", userId, canvasId];
    const [reloadId, setReloadId] = useState(0);
    const workspace = useQuery({ queryKey: key, queryFn: ({ signal }) => getAgentWorkspace(canvasId, signal), enabled: Boolean(userId && canvasId) });
    const defaults = useQuery({ queryKey: ["agent-skill-defaults", userId], queryFn: listAgentSkillDefaults, enabled: Boolean(userId) });
    if (!userId || !canvasId) return <p className="p-4">请登录并打开已保存的画布。</p>;
    if (workspace.isPending || defaults.isPending) return <p className="p-4">正在读取 Workspace…</p>;
    if (workspace.error || defaults.error) return <div role="alert" className="p-4"><p>{(workspace.error || defaults.error)?.message}</p><Button onClick={() => { void workspace.refetch(); void defaults.refetch(); }}>重新读取</Button></div>;
    if (!workspace.data || !defaults.data) return null;
    const remember = (view: AgentWorkspaceView) => { client.setQueryData(key, view); return view; };
    return <WorkspaceSettingsForm key={reloadId} view={workspace.data} defaultSkills={defaults.data.skills} availableSkills={availableSkills} saving={false}
        onSaveDocument={async (revision, content) => remember(await updateAgentWorkspace(canvasId, revision, content))}
        onSaveSkills={async (revision, skills) => remember(await replaceWorkspaceSkills(canvasId, revision, skills))}
        onReload={async () => { const result = await workspace.refetch(); if (result.error) throw result.error; setReloadId((value) => value + 1); }} />;
}
