import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Input } from "antd";
import { getAgentWorkspace, updateAgentWorkspace, type AgentWorkspaceView } from "@/services/api/agent-workspace";
import { ApiError } from "@/services/api/request";
import { useUserStore } from "@/stores/use-user-store";
import "./canvas-agent-workspace-settings.css";

type FormProps = {
    view: AgentWorkspaceView;
    saving: boolean;
    onSaveDocument: (revision: number, content: string) => Promise<AgentWorkspaceView>;
    onReload: () => Promise<void>;
};

export function workspaceDocumentBytes(content: string) {
    return new TextEncoder().encode(content).byteLength;
}

export function WorkspaceSettingsForm(props: FormProps) {
    const [view, setView] = useState(props.view);
    const [document, setDocument] = useState(props.view.agentsMd);
    const [error, setError] = useState("");
    const [conflict, setConflict] = useState(false);
    const [pending, setPending] = useState(false);
    const bytes = workspaceDocumentBytes(document);
    const disabled = props.saving || pending;
    const save = async () => {
        setError("");
        setConflict(false);
        setPending(true);
        try {
            const saved = await props.onSaveDocument(view.revision, document);
            setView(saved);
            setDocument(saved.agentsMd);
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : String(cause));
            setConflict(cause instanceof ApiError && cause.reason === "agent_workspace_revision_conflict");
        } finally {
            setPending(false);
        }
    };
    const reload = async () => {
        setPending(true);
        try {
            await props.onReload();
        } catch (cause) {
            setError(cause instanceof Error ? cause.message : String(cause));
        } finally {
            setPending(false);
        }
    };
    return (
        <div className="agent-workspace-settings thin-scrollbar">
            <section>
                <h3>当前画布规则 · Workspace</h3>
                <p>仅用于当前画布的新运行。与全局自定义系统提示词冲突时，以当前画布规则为准；运行中的任务保持原有设置。</p>
                <label htmlFor="agent-workspace-document">Agents.md</label>
                <Input.TextArea id="agent-workspace-document" aria-label="Workspace Agents.md" value={document} onChange={(event) => setDocument(event.target.value)} autoSize={{ minRows: 8, maxRows: 20 }} disabled={disabled} placeholder="例如：本画布统一使用横屏；保留已有角色设定；分镜先完成资产绑定再生成。" />
                <div className="agent-workspace-actions">
                    <span className="tabular-nums">{bytes} / 65536 字节</span>
                    <Button onClick={() => void save()} disabled={disabled || conflict || bytes > 65536 || document === view.agentsMd}>
                        保存文档
                    </Button>
                </div>
                <p>留空后保存可清除当前画布规则。平台权限、预算和审批规则始终有效。</p>
                {bytes > 65536 ? <p role="alert">超过 64KB，请缩短文档。</p> : null}
            </section>
            {error ? (
                <div role="alert">
                    <p>{error}</p>
                    {conflict ? (
                        <Button onClick={() => void reload()} disabled={disabled}>
                            重新读取配置（替换当前草稿）
                        </Button>
                    ) : null}
                </div>
            ) : null}
        </div>
    );
}

export function CanvasAgentWorkspaceSettings({ canvasId }: { canvasId: string }) {
    const userId = useUserStore((state) => state.user?.id);
    return <WorkspaceSettingsLoader key={[userId || "anonymous", canvasId].join(":")} userId={userId} canvasId={canvasId} />;
}

function WorkspaceSettingsLoader({ userId, canvasId }: { userId?: string; canvasId: string }) {
    const client = useQueryClient();
    const key = ["agent-workspace", userId, canvasId];
    const [reloadId, setReloadId] = useState(0);
    const workspace = useQuery({ queryKey: key, queryFn: ({ signal }) => getAgentWorkspace(canvasId, signal), enabled: Boolean(userId && canvasId) });
    if (!userId || !canvasId) return <p className="p-4">请登录并打开已保存的画布。</p>;
    if (workspace.isPending) return <p className="p-4">正在读取画布规则…</p>;
    if (workspace.error)
        return (
            <div role="alert" className="p-4">
                <p>{workspace.error.message}</p>
                <Button onClick={() => void workspace.refetch()}>重新读取</Button>
            </div>
        );
    if (!workspace.data) return null;
    return (
        <WorkspaceSettingsForm
            key={reloadId}
            view={workspace.data}
            saving={false}
            onSaveDocument={async (revision, content) => {
                const saved = await updateAgentWorkspace(canvasId, revision, content);
                client.setQueryData(key, saved);
                return saved;
            }}
            onReload={async () => {
                const result = await workspace.refetch();
                if (result.error) throw result.error;
                setReloadId((value) => value + 1);
            }}
        />
    );
}
