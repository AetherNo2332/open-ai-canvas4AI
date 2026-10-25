import { App, Button, Dropdown, Popconfirm, Popover } from "antd";
import { ChevronRight, CloudCheck, CloudDownload, CloudOff, Download, FolderClock, History, LoaderCircle, RefreshCw, Users } from "lucide-react";
import { useEffect, useState } from "react";
import { exportCanvasProjects } from "@/lib/canvas/canvas-export";
import { readAllCanvasSyncDrafts, readCanvasSyncDrafts, type CanvasSyncDraft } from "@/services/canvas-sync-drafts";
import { getActiveUserScope } from "@/lib/user-scope";
import { useCanvasStore } from "@/stores/canvas/use-canvas-store";
import { useSyncProgressStore } from "@/stores/use-sync-progress-store";
import { useCanvasSyncActivity } from "@/components/canvas/use-canvas-edit-lease";

const syncActionClass =
    "flex min-h-10 w-full items-center gap-3 rounded-lg px-3 py-2 text-left text-sm font-normal text-foreground/85 transition-colors hover:bg-foreground/[.06] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-40 [&>svg]:size-4 [&>svg]:shrink-0 [&>svg]:text-muted-foreground";

export function CanvasSyncStatus({
    projectId,
    onLoadLatest,
    onRetrySync,
    onOpenVersions,
    onLocateNode,
}: {
    projectId: string;
    onLoadLatest: () => Promise<void>;
    onRetrySync?: () => Promise<void>;
    onOpenVersions?: () => void;
    onLocateNode?: (nodeId: string) => void;
}) {
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
    const label = conflict ? "版本冲突 · 点击处理" : rejected ? (progress.reason === "node_locked" ? "其他窗口正在编辑" : "修改未保存") : !phase ? "尚未同步" : failed ? "云端未保存" : saving ? "正在保存" : "已保存";
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
                placement="bottomLeft"
                arrow={false}
                styles={{ container: { padding: 0, overflow: "hidden" } }}
                open={statusOpen}
                onOpenChange={setStatusOpen}
                content={
                    <div className="w-80 max-w-[calc(100vw-2rem)] max-h-[min(40rem,80vh)] overflow-y-auto text-foreground" data-canvas-no-zoom>
                        <div className="flex items-center gap-3 border-b border-border/60 px-4 py-4">
                            <span
                                className="grid size-10 shrink-0 place-items-center rounded-xl bg-foreground/[.04]"
                                style={{ color: saving ? "var(--palette-status-loading)" : failed ? "var(--palette-status-warning)" : "var(--palette-status-success)" }}
                                aria-hidden="true"
                            >
                                {saving ? <LoaderCircle className="size-5 animate-spin motion-reduce:animate-none" /> : failed ? <CloudOff className="size-5" /> : <CloudCheck className="size-5" />}
                            </span>
                            <div className="min-w-0 space-y-1">
                                <p role="status" className="text-sm font-medium leading-5">
                                    {saving ? "正在保存修改" : conflict ? "有修改需要处理" : failed ? "尚未同步到云端" : "已同步到云端"}
                                </p>
                                <p className="text-xs leading-5 text-muted-foreground">{saving ? "请稍候" : failed ? "查看下方详情以继续" : "最新修改已保存"}</p>
                            </div>
                        </div>
                        {progress?.message || failed ? <p className="border-b border-border/60 px-4 py-3 text-xs leading-5 break-words text-muted-foreground">{progress?.message || "尚未确认云端保存，请保留本地内容"}</p> : null}
                        {conflict ? (
                            <div className="space-y-2 border-b border-border/60 px-4 py-3 text-sm leading-6">
                                <p>云端与本地修改不一致，部分编辑尚未保存。请按以下步骤处理：</p>
                                <ol className="list-decimal space-y-1 pl-5 text-muted-foreground">
                                    <li>先点击「重新同步」，核对是否有已经保存的修改。</li>
                                    <li>仍有冲突时，点击「备份草稿并加载云端」。备份成功后，当前画布将切换为云端版本。</li>
                                    <li>从「本地草稿」下载原修改，在画布列表导入为副本，再把需要的内容补回。</li>
                                </ol>
                                <p className="text-xs text-muted-foreground">草稿保存在当前浏览器和账号下。处理完成前，请保留本站浏览器数据。</p>
                            </div>
                        ) : null}
                        {actionError ? (
                            <p role="alert" className="px-4 py-3 text-sm leading-6 text-destructive">
                                {actionError}
                            </p>
                        ) : null}
                        {rejected ? <p className="px-4 py-3 text-xs leading-5 text-muted-foreground">本地修改已保留。请修正上述问题后点击「重试保存」，无需先切换到云端版本。</p> : null}
                        {activity ? (
                            <div className="space-y-1 border-b border-border/60 px-4 py-3 text-xs text-muted-foreground">
                                <p className="flex items-center gap-2 leading-5">
                                    <Users className="size-3.5 shrink-0" aria-hidden="true" />
                                    {activity.leases.length ? `${activity.leases.length} 个节点正在编辑` : "当前没有节点正在编辑"}
                                </p>
                                {activity.leases.map((lease) => (
                                    <div key={`${lease.nodeId}:${lease.sessionId}`} className="space-y-1 border-b border-border/60 py-2 last:border-b-0">
                                        <div className="flex items-start justify-between gap-2">
                                            <p className="min-w-0 break-words text-foreground" title={`节点 ID：${lease.nodeId}`}>
                                                {lease.nodeTypeLabel} · {lease.nodeTitle}
                                            </p>
                                            {lease.nodeExists && onLocateNode ? (
                                                <Button
                                                    type="link"
                                                    size="small"
                                                    className="shrink-0"
                                                    disabled={busy}
                                                    onClick={() => {
                                                        setStatusOpen(false);
                                                        onLocateNode(lease.nodeId);
                                                    }}
                                                >
                                                    定位节点
                                                </Button>
                                            ) : null}
                                        </div>
                                        <p>{lease.isCurrentWindow ? "当前窗口正在编辑" : "其他窗口正在编辑"}</p>
                                        <p>占用有效至 {new Date(lease.expiresAt).toLocaleTimeString([], { hour12: false })}，编辑时自动续期</p>
                                    </div>
                                ))}
                                {activity.activeAgents.map((agent) => (
                                    <p key={agent.id}>
                                        {agent.title || "Agent"} · {agent.status}
                                    </p>
                                ))}
                            </div>
                        ) : activityError ? (
                            <p className="px-4 py-3 text-xs leading-5 text-muted-foreground">编辑状态暂不可用</p>
                        ) : null}
                        <div className="space-y-0.5 p-1.5">
                            {onRetrySync ? (
                                <button type="button" className={syncActionClass} disabled={busy} onClick={() => void run(onRetrySync)}>
                                    <RefreshCw aria-hidden="true" />
                                    <span>{rejected ? "重试保存" : "重新同步"}</span>
                                </button>
                            ) : null}
                            <Popconfirm title="采用云端最新版本？" description="未同步修改会先备份为本地草稿。备份成功后，当前画布将显示云端内容。" okText="备份并加载" cancelText="暂不处理" disabled={busy} onConfirm={() => run(onLoadLatest)}>
                                <button type="button" className={syncActionClass} disabled={busy}>
                                    {busy ? <LoaderCircle className="animate-spin motion-reduce:animate-none" aria-hidden="true" /> : <CloudDownload aria-hidden="true" />}
                                    <span>{conflict ? "备份草稿并加载云端" : "加载云端最新版本"}</span>
                                </button>
                            </Popconfirm>
                        </div>
                        <div className="space-y-0.5 border-t border-border/60 p-1.5">
                            <CanvasSyncDraftMenu projectId={projectId} disabled={busy} menuRow />
                            <button
                                type="button"
                                className={syncActionClass}
                                disabled={busy}
                                onClick={() =>
                                    void run(async () => {
                                        const project = useCanvasStore.getState().openProject(projectId);
                                        if (project) await exportCanvasProjects([project], `${project.title}-本地草稿`);
                                    })
                                }
                            >
                                <Download aria-hidden="true" />
                                <span>下载当前内容</span>
                            </button>
                            {onOpenVersions ? (
                                <button
                                    type="button"
                                    className={syncActionClass}
                                    onClick={() => {
                                        setStatusOpen(false);
                                        onOpenVersions();
                                    }}
                                >
                                    <History aria-hidden="true" />
                                    <span className="flex-1">版本记录</span>
                                    <ChevronRight className="opacity-50" aria-hidden="true" />
                                </button>
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

export function CanvasSyncDraftMenu({ projectId, disabled, menuRow = false }: { projectId?: string; disabled?: boolean; menuRow?: boolean }) {
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
            {menuRow ? (
                <button type="button" className={syncActionClass} disabled={disabled || exporting}>
                    {exporting ? <LoaderCircle className="animate-spin motion-reduce:animate-none" aria-hidden="true" /> : <FolderClock aria-hidden="true" />}
                    <span className="flex-1">本地草稿</span>
                    <ChevronRight className="opacity-50" aria-hidden="true" />
                </button>
            ) : (
                <Button size={projectId ? "small" : "middle"} loading={exporting} disabled={disabled}>
                    本地草稿
                </Button>
            )}
        </Dropdown>
    );
}
