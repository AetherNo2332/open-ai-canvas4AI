import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button } from "antd";
import { Film, Plus, Trash2 } from "lucide-react";
import { Link, useNavigate } from "react-router";

import { PageHeader, WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceLoadingState } from "@/components/layout/workspace-state";
import { RelativeTime } from "@/components/ui/product/relative-time";
import { deleteShortplay, listShortplays, type ShortplayListItem } from "@/services/api/shortplay";

// 短剧工作台列表：作品是 projects 容器行，集数与资产都在域表里。
// 建档入口与入口页一致；删除复用 /projects 的级联删除。
export default function ShortplayListPage() {
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { message, modal } = App.useApp();
    const [deletingId, setDeletingId] = useState<string | null>(null);

    const query = useQuery({
        queryKey: ["shortplays"],
        queryFn: ({ signal }) => listShortplays(signal),
    });
    const remove = useMutation({
        mutationFn: deleteShortplay,
        onSuccess: () => {
            message.success("短剧已删除");
            void queryClient.invalidateQueries({ queryKey: ["shortplays"] });
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "删除失败"),
    });

    const confirmDelete = (item: ShortplayListItem) => {
        modal.confirm({
            title: `删除《${item.name}》？`,
            content: "全部分集、资产与集画布记录都会删除，此操作无法撤销。",
            okText: "删除",
            okButtonProps: { danger: true },
            cancelText: "取消",
            centered: true,
            onOk: async () => {
                setDeletingId(item.projectId);
                try {
                    await remove.mutateAsync(item.projectId);
                } finally {
                    setDeletingId(null);
                }
            },
        });
    };

    if (query.isLoading) {
        return (
            <WorkspacePage>
                <WorkspaceLoadingState label="正在读取短剧作品" />
            </WorkspacePage>
        );
    }
    if (query.isError) {
        return (
            <WorkspacePage>
                <WorkspaceErrorState description="短剧列表暂时无法读取。" onRetry={() => void query.refetch()} />
            </WorkspacePage>
        );
    }

    const items = query.data?.shortplays ?? [];
    return (
        <WorkspacePage>
            <PageHeader
                title="短剧工作台"
                description="导入分集原文，维护角色、道具与场景，进入每一集的精修画布。"
                meta={<span className="text-xs text-foreground/60">{items.length} 部</span>}
                actions={
                    <Button type="primary" icon={<Plus />} onClick={() => navigate("/shortplays/new")}>
                        新建短剧
                    </Button>
                }
            />
            {items.length === 0 ? (
                <div className="mt-10 flex flex-col items-center gap-3 rounded-xl border border-dashed border-[var(--line-soft)] px-6 py-14 text-center">
                    <Film className="h-8 w-8 text-foreground/35" />
                    <div className="text-sm font-medium">还没有短剧作品</div>
                    <div className="max-w-md text-xs leading-5 text-foreground/55">把已经切好的分集原文粘进来，建档后再维护资产、出参考图并进入每一集的画布。</div>
                    <Button type="primary" icon={<Plus />} onClick={() => navigate("/shortplays/new")}>
                        新建短剧
                    </Button>
                </div>
            ) : (
                <div className="mt-4 grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
                    {items.map((item) => (
                        <div key={item.projectId} className="group relative rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4 transition hover:border-[var(--line-strong)]">
                            <Link to={`/shortplays/${item.projectId}`} className="block">
                                <div className="flex items-start justify-between gap-3">
                                    <div className="min-w-0">
                                        <div className="truncate text-sm font-semibold">{item.name}</div>
                                        <div className="mt-1 text-xs text-foreground/55">
                                            {item.aspectRatio} · {item.episodeCount} 集
                                        </div>
                                    </div>
                                    <span className="rounded-full border border-[var(--line-soft)] px-2 py-0.5 text-[11px] text-foreground/60">短剧</span>
                                </div>
                                <div className="mt-3 text-[11px] text-foreground/45">
                                    更新于 <RelativeTime value={item.updatedAt} />
                                </div>
                            </Link>
                            <Button
                                size="small"
                                type="text"
                                danger
                                className="absolute bottom-2 right-2 opacity-0 transition group-hover:opacity-100"
                                loading={deletingId === item.projectId}
                                icon={<Trash2 />}
                                aria-label={`删除 ${item.name}`}
                                onClick={() => confirmDelete(item)}
                            />
                        </div>
                    ))}
                </div>
            )}
        </WorkspacePage>
    );
}
