import { Button } from "antd";

import { AppModal } from "@/components/ui/product/app-modal";
import type { CanvasShotMigrationPreview } from "@/lib/canvas/canvas-shot-migration";

export function CanvasShotMigrationPanel({
    open,
    preview,
    applying,
    error,
    onCancel,
    onApply,
    onRollback,
}: {
    open: boolean;
    preview: CanvasShotMigrationPreview | null;
    applying: boolean;
    error: string;
    onCancel: () => void;
    onApply: () => void;
    onRollback: (strategy: "restore" | "new_canvas") => void;
}) {
    const pending = Boolean(preview?.required);
    const rollback = preview && !preview.required ? preview.rollback : undefined;
    const blocked = Boolean(preview && (preview.conflicts.length > 0 || preview.activeTasks.length > 0 || preview.blocking.length > 0));
    return (
        <AppModal open={open} centered footer={null} width="min(680px, calc(100vw - 24px))" title={pending ? "把旧分镜行切换为镜头子画布" : "分镜升级回退"} maskClosable={!applying} closable={!applying} onCancel={applying ? undefined : onCancel}>
            <div className="flex flex-col gap-4 px-5 pb-5 pt-1">
                {pending ? (
                    <p className="max-w-[60ch] text-sm leading-6 text-foreground/65 text-pretty">每条分镜行会变成一个镜头容器，并生成图片提示词、视频提示词、台词和备注四个子节点。已有图片或视频结果保持原节点、原素材和原任务绑定。</p>
                ) : rollback ? (
                    <p className="max-w-[60ch] text-sm leading-6 text-foreground/65 text-pretty">
                        {rollback.newerEdits ? "升级后又产生了新编辑，不能直接用旧快照覆盖。可保留当前画布，另外生成一份升级前的分镜副本。" : "这次升级还没有产生新编辑，可以恢复升级前的旧分镜文档。"}
                    </p>
                ) : null}
                {pending && preview ? (
                    <dl className="grid grid-cols-2 gap-3 rounded-[var(--card-radius)] border border-border/60 p-4 text-sm">
                        <div>
                            <dt className="text-foreground/50">待转换分镜行</dt>
                            <dd className="mt-1 font-medium text-foreground">{preview.rows}</dd>
                        </div>
                        <div>
                            <dt className="text-foreground/50">将生成镜头容器</dt>
                            <dd className="mt-1 font-medium text-foreground">{preview.shotSubmaps}</dd>
                        </div>
                    </dl>
                ) : null}
                {rollback ? (
                    <dl className="grid grid-cols-2 gap-3 rounded-[var(--card-radius)] border border-border/60 p-4 text-sm">
                        <div>
                            <dt className="text-foreground/50">升级前版本</dt>
                            <dd className="mt-1 font-medium text-foreground">r{rollback.snapshotRevision}</dd>
                        </div>
                        <div>
                            <dt className="text-foreground/50">当前版本</dt>
                            <dd className="mt-1 font-medium text-foreground">r{rollback.revision}</dd>
                        </div>
                    </dl>
                ) : null}
                {pending && preview?.conflicts.length ? (
                    <div role="alert" className="rounded-[var(--card-radius)] bg-foreground/[0.04] p-4 text-sm text-foreground">
                        <p className="font-medium">请先完成引用迁移</p>
                        <p className="mt-1 leading-6 text-foreground/65">画布里的素材列表和旧连线还有 {preview.conflicts.length} 处不一致。先按顶部「引用迁移」确认来源，再回来升级分镜。</p>
                    </div>
                ) : null}
                {pending && preview?.activeTasks.length ? (
                    <div role="alert" className="rounded-[var(--card-radius)] bg-foreground/[0.04] p-4 text-sm text-foreground">
                        <p className="font-medium">画布仍有在途任务</p>
                        <p className="mt-1 break-words leading-6 text-foreground/65">请等待任务结束或取消后再升级：{preview.activeTasks.join("、")}</p>
                    </div>
                ) : null}
                {pending && preview?.blocking.length ? (
                    <div role="alert" className="rounded-[var(--card-radius)] bg-foreground/[0.04] p-4 text-sm text-foreground">
                        <p className="font-medium">还有阻断项</p>
                        <ul className="mt-1 list-disc space-y-1 pl-5 text-foreground/65">
                            {preview.blocking.map((item) => (
                                <li key={`${item.code}:${item.path || ""}`}>{item.message}</li>
                            ))}
                        </ul>
                    </div>
                ) : null}
                {error ? (
                    <div role="alert" className="rounded-[var(--card-radius)] bg-foreground/[0.06] px-3 py-2 text-sm text-foreground">
                        <p className="font-medium">暂时无法完成分镜格式操作</p>
                        <p className="mt-1 break-words leading-6 text-foreground/70">{error}</p>
                    </div>
                ) : null}
                <div className="flex flex-wrap items-center justify-end gap-2">
                    <Button className="min-h-11" onClick={onCancel} disabled={applying}>
                        稍后再说
                    </Button>
                    {pending ? (
                        <Button className="min-h-11" type="primary" loading={applying} disabled={!preview || blocked} onClick={onApply}>
                            确认并升级
                        </Button>
                    ) : null}
                    {rollback?.canRestore ? (
                        <Button className="min-h-11" danger loading={applying} onClick={() => onRollback("restore")}>
                            撤销升级并恢复旧分镜
                        </Button>
                    ) : null}
                    {rollback?.canCopy ? (
                        <Button className="min-h-11" danger loading={applying} onClick={() => onRollback("new_canvas")}>
                            保留当前并生成迁移前副本
                        </Button>
                    ) : null}
                </div>
            </div>
        </AppModal>
    );
}
