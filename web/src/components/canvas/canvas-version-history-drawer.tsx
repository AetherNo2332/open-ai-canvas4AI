import { useCallback, useEffect, useState } from "react";

import { App, Button, Popconfirm, Spin } from "antd";
import { AlertCircle, History, RefreshCw, RotateCcw } from "lucide-react";

import { AppDrawer } from "@/components/ui/product/app-drawer";
import { EmptyState } from "@/components/ui/product/empty-state";
import { getRemoteCanvasProject, listRemoteCanvasHistory, restoreRemoteCanvasHistory, type RemoteCanvasHistorySnapshot } from "@/services/api/user-data";
import { cn } from "@/lib/utils";

const REASON_LABELS: Record<string, string> = {
    automatic: "自动存档",
    before_restore: "恢复前备份",
    before_resource_repair: "资源修复前",
    initial: "初始版本",
    reference_migration: "引用迁移前",
};

type CanvasVersionHistoryDrawerProps = {
    projectId: string;
    open: boolean;
    currentRevision?: number;
    onClose: () => void;
    onBeforeRestore: () => Promise<void>;
    onRestored: () => Promise<void>;
};

export function CanvasVersionHistoryDrawer({ projectId, open, currentRevision, onClose, onBeforeRestore, onRestored }: CanvasVersionHistoryDrawerProps) {
    const { message } = App.useApp();
    const [snapshots, setSnapshots] = useState<RemoteCanvasHistorySnapshot[]>([]);
    const [serverRevision, setServerRevision] = useState(currentRevision || 0);
    const [loading, setLoading] = useState(false);
    const [refreshing, setRefreshing] = useState(false);
    const [restoringId, setRestoringId] = useState("");
    const [error, setError] = useState("");

    const load = useCallback(
        async (mode: "initial" | "refresh") => {
            if (mode === "initial") setLoading(true);
            else setRefreshing(true);
            setError("");
            try {
                const history = await listRemoteCanvasHistory(projectId);
                setSnapshots(history.snapshots || []);
                setServerRevision(history.currentRevision);
            } catch (cause) {
                setError(cause instanceof Error ? cause.message : "版本历史读取失败");
            } finally {
                setLoading(false);
                setRefreshing(false);
            }
        },
        [projectId],
    );

    useEffect(() => {
        if (!open) return;
        void load("initial");
    }, [load, open]);

    const restore = async (snapshot: RemoteCanvasHistorySnapshot) => {
        setRestoringId(snapshot.id);
        setError("");
        try {
            await onBeforeRestore();
            const latest = await getRemoteCanvasProject(projectId);
            const revision = latest.project.revision;
            if (!Number.isSafeInteger(revision)) throw new Error("无法确认当前画布版本，请刷新后重试");
            await restoreRemoteCanvasHistory(projectId, snapshot.id, revision as number);
            try {
                await onRestored();
            } catch (cause) {
                const text = cause instanceof Error ? cause.message : "当前窗口刷新失败";
                setError(`画布已恢复到 V${snapshot.revision}，但当前窗口刷新失败：${text}`);
                message.warning("画布已恢复，请刷新当前页面确认内容");
                void load("refresh");
                return;
            }
            message.success(`已恢复 V${snapshot.revision}，恢复前内容已自动留档`);
            onClose();
        } catch (cause) {
            const text = cause instanceof Error ? cause.message : "恢复历史版本失败";
            setError(text);
            message.error(text);
            void load("refresh");
        } finally {
            setRestoringId("");
        }
    };

    return (
        <AppDrawer
            title={
                <div className="flex items-center gap-2">
                    <History className="size-4 text-[var(--workspace-accent)]" />
                    <span className="text-sm font-semibold">版本历史</span>
                    <span className="rounded-md bg-stone-100 px-1.5 py-0.5 text-[var(--fs-label)] tabular-nums text-stone-600 dark:bg-stone-800 dark:text-stone-300">V{serverRevision}</span>
                </div>
            }
            placement="right"
            width={440}
            open={open}
            onClose={() => {
                if (!restoringId) onClose();
            }}
            closable={!restoringId}
            maskClosable={!restoringId}
            className="workspace-drawer canvas-version-history-drawer"
            styles={{
                header: { borderBottom: "1px solid var(--border-color, rgba(255,255,255,0.08))" },
                body: { padding: "16px 18px" },
            }}
        >
            <div className="flex h-full min-h-0 flex-col gap-3">
                <div className="flex items-center justify-between gap-2">
                    <p className="m-0 text-xs leading-5 text-stone-500 dark:text-stone-400">只显示最近 20 个内容版本，最多每 5 分钟自动留档；恢复前会先保存当前编辑并备份当前版本。</p>
                    <Button type="text" size="small" icon={<RefreshCw className={cn("size-3.5", refreshing && "animate-spin")} />} onClick={() => void load("refresh")} disabled={Boolean(restoringId)} aria-label="刷新版本历史" />
                </div>

                {error ? (
                    <div role="alert" className="flex items-start gap-2 rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-xs text-rose-700 dark:border-rose-900/50 dark:bg-rose-950/25 dark:text-rose-300">
                        <AlertCircle className="mt-0.5 size-3.5 shrink-0" />
                        <span className="min-w-0 flex-1 break-words">{error}</span>
                        <button type="button" className="shrink-0 font-medium underline underline-offset-2" onClick={() => void load("initial")}>
                            重试
                        </button>
                    </div>
                ) : null}

                <div className="thin-scrollbar min-h-0 flex-1 overflow-y-auto overscroll-contain pr-1">
                    {loading ? (
                        <div className="grid h-40 place-items-center">
                            <Spin />
                        </div>
                    ) : snapshots.length ? (
                        <div className="space-y-2">
                            {snapshots.map((snapshot) => (
                                <article key={snapshot.id} className="rounded-xl border border-stone-200 bg-white p-3 shadow-sm dark:border-stone-800 dark:bg-stone-900/70">
                                    <div className="flex items-start justify-between gap-3">
                                        <div className="min-w-0">
                                            <div className="flex flex-wrap items-center gap-2">
                                                <span className="text-sm font-semibold tabular-nums">V{snapshot.revision}</span>
                                                <span className="rounded-md bg-stone-100 px-1.5 py-0.5 text-[var(--fs-label)] text-stone-600 dark:bg-stone-800 dark:text-stone-300">{REASON_LABELS[snapshot.reason] || snapshot.reason || "历史版本"}</span>
                                            </div>
                                            <p className="mb-0 mt-1 truncate text-xs text-stone-600 dark:text-stone-300" title={snapshot.title}>
                                                {snapshot.title || "未命名画布"}
                                            </p>
                                        </div>
                                        <Popconfirm title={`恢复 V${snapshot.revision}？`} description="当前内容会先保存并自动留档，恢复后其他窗口会收到更新。" okText="恢复" cancelText="取消" onConfirm={() => restore(snapshot)}>
                                            <Button size="small" type="text" icon={<RotateCcw className="size-3.5" />} loading={restoringId === snapshot.id} disabled={Boolean(restoringId)} aria-label={`恢复版本 V${snapshot.revision}`}>
                                                恢复
                                            </Button>
                                        </Popconfirm>
                                    </div>
                                    <dl className="mb-0 mt-2 grid grid-cols-3 gap-2 text-[var(--fs-label)] text-stone-500 dark:text-stone-400">
                                        <div>
                                            <dt className="m-0">节点</dt>
                                            <dd className="m-0 font-medium tabular-nums text-stone-700 dark:text-stone-200">{snapshot.nodeCount.toLocaleString()}</dd>
                                        </div>
                                        <div>
                                            <dt className="m-0">连线</dt>
                                            <dd className="m-0 font-medium tabular-nums text-stone-700 dark:text-stone-200">{snapshot.connectionCount.toLocaleString()}</dd>
                                        </div>
                                        <div>
                                            <dt className="m-0">存档</dt>
                                            <dd className="m-0 font-medium tabular-nums text-stone-700 dark:text-stone-200">{formatHistoryTime(snapshot.createdAt)}</dd>
                                        </div>
                                    </dl>
                                </article>
                            ))}
                        </div>
                    ) : (
                        <EmptyState icon={History} title="还没有可恢复的版本" description="保存一次内容变化后，服务端会按保留策略自动留档。" />
                    )}
                </div>
            </div>
        </AppDrawer>
    );
}

function formatHistoryTime(value: string) {
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return "--";
    return `${String(date.getMonth() + 1).padStart(2, "0")}/${String(date.getDate()).padStart(2, "0")} ${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}
