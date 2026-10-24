import { App, Button, Dropdown, Popconfirm, Popover } from "antd";
import { CloudCheck, CloudOff, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";
import { exportCanvasProjects } from "@/lib/canvas/canvas-export";
import { readAllCanvasSyncDrafts, readCanvasSyncDrafts, type CanvasSyncDraft } from "@/services/canvas-sync-drafts";
import { getActiveUserScope } from "@/lib/user-scope";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";
import { useSyncProgressStore } from "@/stores/use-sync-progress-store";
import { useCanvasSyncActivity } from "@/components/canvas/use-canvas-edit-lease";

export function CanvasSyncStatus({ projectId, onLoadLatest, onRetrySync, onOpenVersions }: { projectId: string; onLoadLatest: () => Promise<void>; onRetrySync?: () => Promise<void>; onOpenVersions?: () => void }) {
    const { message } = App.useApp();
    const { activity, error: activityError } = useCanvasSyncActivity(projectId);
    const progress = useSyncProgressStore((state) => state.syncingProjects[projectId]);
    const [busy, setBusy] = useState(false);
    const [statusOpen, setStatusOpen] = useState(false);
    const [actionError, setActionError] = useState("");
    const phase = progress?.phase;
    const conflict = phase === "conflict";
    const failed = phase === "error" || conflict || !phase;
    const saving = phase === "pending" || phase === "saving" || phase === "uploading";
    const rejected = phase === "error" && !!progress?.reason;
    const label = conflict ? "版本冲突 · 点击处理" : rejected ? progress.reason === "node_locked" ? "其他窗口正在编辑" : "修改未保存" : !phase ? "尚未同步" : failed ? "云端未保存" : saving ? "正在保存" : "已保存";
    useEffect(() => {
        setActionError("");
        setStatusOpen(conflict || rejected);
    }, [projectId, conflict, rejected]);
    const run = async (operation: () => Promise<unknown>) => {
        setBusy(true);
        setActionError("");
        try {
            await operation();
        } catch (error) {
            const text = error instanceof Error ? error.message : "操作失败，请重试";
            setActionError(text);
            message.error(text);
        } finally {
            setBusy(false);
        }
    };

    return (
        <>
            <Popover
                trigger="click"
                placement="bottom"
                open={statusOpen}
                onOpenChange={setStatusOpen}
                content={
                    <div className="w-80 max-w-[calc(100vw-2rem)] space-y-3" data-canvas-no-zoom>
                        <p role="status" className="text-sm">
                            {progress?.message || (phase === "done" ? "画布已同步到云端" : "尚未确认云端保存，请保留本地内容")}
                        </p>
                        {conflict ? (
                            <div className="space-y-2 text-sm leading-6">
                                <p>云端与本地修改不一致，部分编辑尚未保存。请按以下步骤处理：</p>
                                <ol className="list-decimal space-y-1 pl-5 text-muted-foreground">
                                    <li>先点击「重新同步」，核对是否有已经保存的修改。</li>
                                    <li>仍有冲突时，点击「备份草稿并加载云端」。备份成功后，当前画布将切换为云端版本。</li>
                                    <li>从「本地草稿」下载原修改，在画布列表导入为副本，再把需要的内容补回。</li>
                                </ol>
                                <p className="text-xs text-muted-foreground">草稿保存在当前浏览器和账号下。处理完成前，请保留本站浏览器数据。</p>
                            </div>
                        ) : null}
                        {actionError ? <p role="alert" className="text-sm leading-6">{actionError}</p> : null}
                        {rejected ? <p className="text-sm leading-6">本地修改已保留。请修正上述问题后点击「重试保存」，无需先切换到云端版本。</p> : null}
                        {activity ? (
                            <div className="space-y-1 text-xs text-muted-foreground">
                                <p>{activity.leases.length ? `${activity.leases.length} 个节点正在编辑` : "暂无节点编辑占用"}</p>
                                {activity.leases.map((lease) => (
                                    <p key={`${lease.nodeId}:${lease.sessionId}`} className="break-all">
                                        节点 {lease.nodeId} · 占用至 {new Date(lease.expiresAt).toLocaleTimeString()}
                                    </p>
                                ))}
                                {activity.activeAgents.map((agent) => (
                                    <p key={agent.id}>
                                        {agent.title || "Agent"} · {agent.status}
                                    </p>
                                ))}
                            </div>
                        ) : activityError ? (
                            <p className="text-xs text-muted-foreground">编辑占用状态暂不可用，保存仍以回执为准</p>
                        ) : null}
                        <div className="flex flex-wrap gap-2">
                            {onRetrySync ? <Button size="small" disabled={busy} onClick={() => void run(onRetrySync)}>{rejected ? "重试保存" : "重新同步"}</Button> : null}
                            <Popconfirm
                                title="采用云端最新版本？"
                                description="未同步修改会先备份为本地草稿。备份成功后，当前画布将显示云端内容。"
                                okText="备份并加载"
                                cancelText="暂不处理"
                                disabled={busy}
                                onConfirm={() => run(onLoadLatest)}
                            >
                                <Button size="small" type={conflict ? "primary" : "default"} loading={busy}>
                                    {conflict ? "备份草稿并加载云端" : "加载云端最新版本"}
                                </Button>
                            </Popconfirm>
                            <CanvasSyncDraftMenu projectId={projectId} disabled={busy} />
                            <Button
                                size="small"
                                disabled={busy}
                                onClick={() =>
                                    void run(async () => {
                                        const project = useCanvasStore.getState().openProject(projectId);
                                        if (project) await exportCanvasProjects([project], `${project.title}-本地草稿`);
                                    })
                                }
                            >
                                下载当前内容
                            </Button>
                            {onOpenVersions ? (
                                <Button
                                    size="small"
                                    onClick={() => {
                                        setStatusOpen(false);
                                        onOpenVersions();
                                    }}
                                >
                                    版本记录{progress?.draftCount ? ` · ${progress.draftCount} 份草稿` : ""}
                                </Button>
                            ) : null}
                        </div>
                    </div>
                }
            >
                <Button
                    type="text"
                    size="small"
                    danger={failed}
                    aria-label={`画布保存状态：${label}`}
                    icon={saving ? <LoaderCircle className="size-3.5 animate-spin motion-reduce:animate-none" /> : failed ? <CloudOff className="size-3.5" /> : <CloudCheck className="size-3.5" />}
                >
                    <span className="canvas-sync-status-label text-xs">{label}</span>
                </Button>
            </Popover>
        </>
    );
}

export function CanvasSyncDraftMenu({ projectId, disabled }: { projectId?: string; disabled?: boolean }) {
    const { message } = App.useApp();
    const [drafts, setDrafts] = useState<CanvasSyncDraft[]>([]);
    const [draftScope, setDraftScope] = useState("");
    const [loading, setLoading] = useState(false);
    const [exporting, setExporting] = useState(false);
    return (
        <Dropdown
            trigger={["click"]}
            onOpenChange={(open) => {
                if (!open) return;
                const scope = getActiveUserScope();
                setDraftScope(scope);
                setDrafts([]);
                setLoading(true);
                void (projectId ? readCanvasSyncDrafts(projectId, scope) : readAllCanvasSyncDrafts(scope))
                    .then((items) => {
                        if (getActiveUserScope() === scope) setDrafts(items.reverse());
                    })
                    .catch(() => message.error("读取本地草稿失败"))
                    .finally(() => setLoading(false));
            }}
            menu={{
                items: drafts.length
                    ? drafts.map((draft) => ({
                          key: draft.id,
                          label: `${draft.project.title} · ${new Date(draft.savedAt).toLocaleString()}`,
                          onClick: () => {
                              if (getActiveUserScope() !== draftScope) {
                                  setDrafts([]);
                                  message.error("账号已切换，请重新打开本地草稿");
                                  return;
                              }
                              setExporting(true);
                              void exportCanvasProjects([draft.project], `${draft.project.title}-本地草稿`, { includeLocalDrawings: false })
                                  .then(() => message.success("草稿已下载，可从画布列表导入为新画布"))
                                  .catch(() => message.error("草稿下载失败，请重试"))
                                  .finally(() => setExporting(false));
                          },
                      }))
                    : [{ key: "empty", label: loading ? "正在读取草稿…" : "暂无本地草稿", disabled: true }],
            }}
        >
            <Button size={projectId ? "small" : "middle"} loading={exporting} disabled={disabled}>
                本地草稿
            </Button>
        </Dropdown>
    );
}
