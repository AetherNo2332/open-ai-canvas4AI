import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input, Skeleton } from "antd";
import { getAgentSystemPrompt, updateAgentSystemPrompt, type AgentSystemPromptSettings } from "@/services/api/agent";
import { ApiError } from "@/services/api/request";
import { useUserStore } from "@/stores/use-user-store";

type FormProps = {
    settings: AgentSystemPromptSettings;
    onSave: (input: AgentSystemPromptSettings) => Promise<AgentSystemPromptSettings>;
    onReload: () => void;
};

export function AgentSystemPromptForm({ settings, onSave, onReload }: FormProps) {
    const [saved, setSaved] = useState(settings);
    const [content, setContent] = useState(settings.systemPrompt);
    const [saving, setSaving] = useState(false);
    const [error, setError] = useState("");
    const [conflict, setConflict] = useState(false);
    const [success, setSuccess] = useState(false);
    const count = Array.from(content).length;
    const save = async () => {
        setSaving(true);
        setError("");
        setSuccess(false);
        try {
            const result = await onSave({ systemPrompt: content, revision: saved.revision });
            setSaved(result);
            setContent(result.systemPrompt);
            setConflict(false);
            setSuccess(true);
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : "保存提示词失败");
            setConflict(cause instanceof ApiError && cause.status === 409);
        } finally {
            setSaving(false);
        }
    };
    return (
        <section className="space-y-4" aria-labelledby="agent-system-prompt-heading">
            <div className="settings-preference-heading">
                <h3 id="agent-system-prompt-heading">全局自定义系统提示词</h3>
                <p className="max-w-[60ch]">设置画布 Agent 的回复口吻、创作偏好和工作方式。保存后用于本账户所有画布后续新建的运行，运行中的任务保持原有设置。留空使用默认行为。</p>
            </div>
            <label htmlFor="agent-system-prompt" className="sr-only">
                自定义系统提示词
            </label>
            <Input.TextArea
                id="agent-system-prompt"
                aria-describedby="agent-system-prompt-help"
                value={content}
                onChange={(event) => {
                    setContent(event.target.value);
                    setSuccess(false);
                }}
                autoSize={{ minRows: 8, maxRows: 20 }}
                disabled={saving}
                placeholder="例如：始终用中文回答；创作前先给出简短方案；保留我提供的角色名称。"
                status={count > 12000 ? "error" : undefined}
            />
            <div className="flex flex-wrap items-center gap-3">
                <Button type="primary" onClick={() => void save()} loading={saving} disabled={conflict || count > 12000 || content === saved.systemPrompt}>
                    保存提示词
                </Button>
                <Button
                    onClick={() => {
                        setContent("");
                        setSuccess(false);
                    }}
                    disabled={saving || content === ""}
                >
                    清空
                </Button>
                <span className="ml-auto text-xs tabular-nums text-foreground/60">{count} / 12000 字符</span>
            </div>
            <p id="agent-system-prompt-help" className="max-w-[60ch] text-xs text-foreground/60">
                清空后需保存才能恢复默认行为。自定义要求仍遵循平台权限、预算和审批规则。
            </p>
            {count > 12000 ? (
                <p role="alert" className="text-sm">
                    提示词不能超过 12000 个字符，请缩短后保存。
                </p>
            ) : null}
            {error ? (
                <div role="alert" className="space-y-2 text-sm">
                    <p>{error}</p>
                    {conflict ? <Button onClick={onReload}>重新加载已保存内容</Button> : null}
                </div>
            ) : null}
            {success ? (
                <p role="status" className="text-sm text-foreground/70">
                    已保存，将用于下一次运行。
                </p>
            ) : null}
        </section>
    );
}

export function AgentSystemPromptPane() {
    const userID = useUserStore((state) => state.user?.id);
    const queryClient = useQueryClient();
    const { message } = App.useApp();
    const queryKey = ["agent-system-prompt", userID];
    const query = useQuery({ queryKey, queryFn: getAgentSystemPrompt, enabled: Boolean(userID), refetchOnWindowFocus: false, refetchOnReconnect: false });
    if (!userID) return <p role="alert">请先登录后设置 Agent。</p>;
    if (query.isPending) return <Skeleton active paragraph={{ rows: 5 }} />;
    if (query.isError && !query.data)
        return (
            <div role="alert" className="space-y-3">
                <p>{query.error.message || "加载提示词失败"}</p>
                <Button onClick={() => void query.refetch()}>重试</Button>
            </div>
        );
    return (
        <AgentSystemPromptForm
            key={`${userID}:${query.data.revision}`}
            settings={query.data}
            onSave={async (input) => {
                const result = await updateAgentSystemPrompt(input);
                queryClient.setQueryData(queryKey, result);
                message.success("系统提示词已保存，将用于下一次运行");
                return result;
            }}
            onReload={() => void query.refetch()}
        />
    );
}
