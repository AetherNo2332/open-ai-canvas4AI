import { Button, Input, Select, Switch } from "antd";
import { useState } from "react";
import type { CrewMemberInput, CrewView } from "@/services/api/agent-crew";

export function CrewSettings({ crew, onSave, onDelete }: { crew?: CrewView; onSave: (value: { name: string; description: string; status: string; members: CrewMemberInput[] }) => Promise<void>; onDelete?: () => Promise<void> }) {
    const [name, setName] = useState(crew?.name ?? "");
    const [description, setDescription] = useState(crew?.description ?? "");
    const [enabled, setEnabled] = useState(crew?.status === "enabled");
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState("");
    const members = crew?.members ?? [];
    const save = async () => {
        setBusy(true);
        setError("");
        try {
            await onSave({ name, description, status: enabled ? "enabled" : "disabled", members: members.map(({ id, skills, ...member }) => member) });
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : String(cause));
        } finally {
            setBusy(false);
        }
    };
    return (
        <section aria-label="Crew 设置" className="agent-crew-settings space-y-3">
            <header>
                <h3>Crew 子代理</h3>
                <p>配置只影响新 Run；成员使用独立会话和冻结技能快照。</p>
            </header>
            <label>
                名称
                <Input aria-label="Crew 名称" value={name} onChange={(e) => setName(e.target.value)} disabled={busy} />
            </label>
            <label>
                说明
                <Input.TextArea aria-label="Crew 说明" value={description} onChange={(e) => setDescription(e.target.value)} disabled={busy} />
            </label>
            <label className="flex items-center gap-2">
                启用
                <Switch aria-label="启用 Crew" checked={enabled} onChange={setEnabled} disabled={busy} />
            </label>
            <dl>
                {members.map((member) => (
                    <div key={member.name}>
                        <dt>{member.name}</dt>
                        <dd>
                            {member.role} · {member.permissionMode} · {member.budget.maxSteps} steps
                        </dd>
                    </div>
                ))}
            </dl>
            <div className="flex gap-2">
                <Button type="primary" onClick={() => void save()} disabled={busy || !name.trim()}>
                    保存 Crew
                </Button>
                {onDelete ? (
                    <Button danger onClick={() => void onDelete()} disabled={busy}>
                        删除 Crew
                    </Button>
                ) : null}
            </div>
            {error ? <p role="alert">{error}</p> : null}
        </section>
    );
}

export function CrewMemberEditor({ value, onChange }: { value: CrewMemberInput; onChange: (value: CrewMemberInput) => void }) {
    return (
        <fieldset aria-label={`成员 ${value.name}`}>
            <legend>{value.name}</legend>
            <Select
                aria-label="成员权限"
                value={value.permissionMode}
                options={[
                    { value: "read_only", label: "只读" },
                    { value: "propose", label: "提案" },
                ]}
                onChange={(permissionMode) => onChange({ ...value, permissionMode })}
            />
            <Input aria-label="成员模型" value={value.modelConfig.model} onChange={(e) => onChange({ ...value, modelConfig: { ...value.modelConfig, model: e.target.value } })} />
        </fieldset>
    );
}
