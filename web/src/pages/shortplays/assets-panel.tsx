import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input, InputNumber, Switch, Tag, Tooltip } from "antd";
import { Image as ImageIcon, Inbox, Plus, RefreshCw, Sparkles } from "lucide-react";

import { AppModal } from "@/components/ui/product/app-modal";
import { RelativeTime } from "@/components/ui/product/relative-time";
import { resourceFileUrl } from "@/services/api/resources";
import { listShortplayAssets, queueShortplayAssetImages, queueShortplayExtract, upsertShortplayAsset, type ShortplayAssetSummary, type ShortplayAssetWrite, type ShortplayImageQueueRequest, type ShortplayJob } from "@/services/api/shortplay";

// W04/W05 资产库、提取收件箱与参考图：列角色/道具/场景、手写或修订单条资产、
// 新建衍生造型、把提取或出图排进 job 队列，并在同一处查看状态与失败原因。

const KIND_LABELS: Record<string, string> = { character: "角色", prop: "道具", scene: "场景" };

const JOB_STATUS_LABELS: Record<string, string> = {
    queued: "排队中",
    running: "执行中",
    succeeded: "已完成",
    failed: "失败",
    canceled: "已取消",
};

const JOB_STATUS_COLORS: Record<string, string> = {
    queued: "default",
    running: "processing",
    succeeded: "success",
    failed: "error",
    canceled: "default",
};

type AssetDraft = {
    assetId?: number;
    kind: string;
    name: string;
    aliases: string;
    description: string;
    appearance: string;
    voice: string;
    locked: boolean;
    /** 非空 = 新建衍生造型：父资产的作品内序号。 */
    derivedFrom: string;
    variantName: string;
};

function draftFromAsset(asset?: ShortplayAssetSummary): AssetDraft {
    return {
        assetId: asset?.id,
        kind: asset?.kind || "character",
        name: asset?.name || "",
        aliases: asset?.aliases.join("，") || "",
        description: asset?.description || "",
        appearance: asset?.appearance || "",
        voice: asset?.voice || "",
        locked: asset?.locked || false,
        derivedFrom: "",
        variantName: "",
    };
}

function draftToWrite(draft: AssetDraft): ShortplayAssetWrite {
    const parentID = Number.parseInt(draft.derivedFrom.trim(), 10);
    const base: ShortplayAssetWrite = {
        assetId: draft.assetId,
        kind: draft.kind,
        name: draft.name.trim(),
        aliases: draft.aliases
            .split(/[,，\s]+/)
            .map((item) => item.trim())
            .filter(Boolean),
        description: draft.description.trim(),
        appearance: draft.appearance.trim(),
        voice: draft.kind === "character" ? draft.voice.trim() : "",
        locked: draft.locked,
    };
    if (!draft.assetId && Number.isFinite(parentID) && parentID > 0) {
        // 衍生造型：名称由父名 + 造型名合成，别名/音色/分类继承父资产，服务端会拒绝单独指定。
        return { ...base, name: "", aliases: [], voice: "", derivedFrom: parentID, variantName: draft.variantName.trim() };
    }
    return base;
}

export function ShortplayAssetsPanel({ projectId, jobs }: { projectId: string; jobs: ShortplayJob[] }) {
    const queryClient = useQueryClient();
    const { message } = App.useApp();
    const [draft, setDraft] = useState<AssetDraft | null>(null);

    const assetsQuery = useQuery({
        queryKey: ["shortplay-assets", projectId],
        queryFn: ({ signal }) => listShortplayAssets(projectId, signal),
        enabled: Boolean(projectId),
    });

    // 详情页在有待领 job 时轮询；job 状态一变就重读资产，提取完成后新资产立刻出现。
    const jobsSignature = jobs.map((job) => `${job.id}:${job.status}:${job.updatedAt}`).join("|");
    useEffect(() => {
        if (!jobsSignature) return;
        void queryClient.invalidateQueries({ queryKey: ["shortplay-assets", projectId] });
    }, [jobsSignature, projectId, queryClient]);

    const refresh = () => {
        void queryClient.invalidateQueries({ queryKey: ["shortplay-assets", projectId] });
        void queryClient.invalidateQueries({ queryKey: ["shortplay", projectId] });
    };

    const extract = useMutation({
        mutationFn: () => queueShortplayExtract(projectId),
        onSuccess: (result) => {
            message.success(result.queued ? "已排入资产提取" : "已有在途提取，复用该任务");
            refresh();
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "提取排队失败"),
    });

    const imageQueue = useMutation({
        mutationFn: (body: ShortplayImageQueueRequest) => queueShortplayAssetImages(projectId, body),
        onSuccess: (result) => {
            message.success(`参考图：新排队 ${result.queued}，复用 ${result.reused}，跳过 ${result.skipped}`);
            refresh();
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "参考图排队失败"),
    });

    const save = useMutation({
        mutationFn: (current: AssetDraft) => upsertShortplayAsset(projectId, draftToWrite(current)),
        onSuccess: () => {
            message.success("资产已保存");
            setDraft(null);
            refresh();
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "资产保存失败"),
    });

    const assets = assetsQuery.data?.assets ?? [];
    const grouped = ["character", "prop", "scene"].map((kind) => ({
        kind,
        items: assets.filter((asset) => asset.kind === kind),
    }));

    return (
        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
            <section className="space-y-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="text-xs text-foreground/55">资产按作品内序号标识，跨集复用同一行；提取会并入已有设定而不是覆盖。</div>
                    <div className="flex items-center gap-2">
                        <Button icon={<Plus />} onClick={() => setDraft(draftFromAsset())}>
                            手动添加
                        </Button>
                        <Tooltip title="为所有从未生成过参考图的资产排队；锁定的资产会被跳过">
                            <Button icon={<ImageIcon />} loading={imageQueue.isPending} disabled={assets.length === 0} onClick={() => imageQueue.mutate({ onlyMissing: true })}>
                                生成缺图
                            </Button>
                        </Tooltip>
                        <Button type="primary" icon={<Sparkles />} loading={extract.isPending} onClick={() => extract.mutate()}>
                            提取资产
                        </Button>
                    </div>
                </div>
                {assetsQuery.isLoading ? <div className="rounded-xl border border-dashed border-[var(--line-soft)] p-8 text-center text-xs text-foreground/50">正在读取资产…</div> : null}
                {!assetsQuery.isLoading && assets.length === 0 ? (
                    <div className="rounded-xl border border-dashed border-[var(--line-soft)] p-8 text-center text-xs leading-6 text-foreground/50">还没有资产。点「提取资产」从分集原文提取，或先手动添加一条。</div>
                ) : null}
                {grouped.map((group) =>
                    group.items.length ? (
                        <div key={group.kind}>
                            <div className="mb-2 text-xs font-medium text-foreground/70">
                                {KIND_LABELS[group.kind]}（{group.items.length}）
                            </div>
                            <div className="grid gap-2 md:grid-cols-2">
                                {group.items.map((asset) => (
                                    <AssetCard key={asset.id} asset={asset} onEdit={() => setDraft(draftFromAsset(asset))} onQueueImage={() => imageQueue.mutate({ assetIds: [asset.id] })} />
                                ))}
                            </div>
                        </div>
                    ) : null,
                )}
            </section>
            <aside className="space-y-3">
                <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-3">
                    <div className="flex items-center justify-between">
                        <div className="inline-flex items-center gap-1.5 text-xs font-medium text-foreground/70">
                            <Inbox className="h-3.5 w-3.5" /> 提取收件箱
                        </div>
                        <Tooltip title="刷新资产与任务状态">
                            <Button type="text" size="small" icon={<RefreshCw />} onClick={refresh} />
                        </Tooltip>
                    </div>
                    {jobs.length === 0 ? <div className="mt-3 text-[11px] text-foreground/45">还没有提取或生成任务。</div> : null}
                    <div className="mt-2 space-y-2">
                        {jobs.map((job) => (
                            <div key={job.id} className="rounded-lg border border-[var(--line-soft)] p-2">
                                <div className="flex items-center justify-between gap-2 text-[11px]">
                                    <span className="text-foreground/70">{job.kind}</span>
                                    <Tag color={JOB_STATUS_COLORS[job.status] || "default"} className="m-0">
                                        {JOB_STATUS_LABELS[job.status] || job.status}
                                    </Tag>
                                </div>
                                <div className="mt-1 text-[11px] text-foreground/45">
                                    {job.attempt > 0 ? `第 ${job.attempt} 次 · ` : ""}
                                    {job.updatedAt ? <RelativeTime value={job.updatedAt} /> : null}
                                </div>
                                {job.lastError ? <div className="mt-1 whitespace-pre-wrap break-words rounded bg-[var(--surface-2)] p-1.5 text-[11px] leading-5 text-[var(--danger,#d14343)]">{job.lastError}</div> : null}
                            </div>
                        ))}
                    </div>
                    <div className="mt-2 text-[11px] leading-5 text-foreground/45">失败的提取不会自动重试；修好渠道或设置后重新点「提取资产」，游标会从上次成功的批次继续。</div>
                </div>
            </aside>
            <AppModal
                title={draft?.assetId ? `编辑资产 #${draft.assetId}` : draft?.derivedFrom ? "新建衍生造型" : "新建资产"}
                open={Boolean(draft)}
                onCancel={() => setDraft(null)}
                okText="保存"
                confirmLoading={save.isPending}
                okButtonProps={{
                    disabled: draft ? (Boolean(draft.derivedFrom) ? !draft.variantName.trim() : !draft.name.trim()) : true,
                }}
                onOk={() => draft && save.mutate(draft)}
            >
                {draft ? (
                    <div className="space-y-3">
                        <div>
                            <div className="mb-1 text-xs text-foreground/60">类型</div>
                            <div className="flex gap-2">
                                {["character", "prop", "scene"].map((kind) => (
                                    <Button key={kind} size="small" type={draft.kind === kind ? "primary" : "default"} disabled={Boolean(draft.derivedFrom) && kind !== "character"} onClick={() => setDraft({ ...draft, kind })}>
                                        {KIND_LABELS[kind]}
                                    </Button>
                                ))}
                            </div>
                        </div>
                        {!draft.assetId && draft.kind === "character" ? (
                            <div className="rounded-lg border border-[var(--line-soft)] p-2">
                                <div className="text-xs text-foreground/60">衍生造型（留空父资产 = 新建基础角色）</div>
                                <div className="mt-2 flex items-center gap-2">
                                    <span className="text-[11px] text-foreground/50">父资产序号</span>
                                    <InputNumber className="w-24" min={1} precision={0} value={draft.derivedFrom ? Number(draft.derivedFrom) : null} onChange={(value) => setDraft({ ...draft, derivedFrom: value ? String(value) : "" })} />
                                </div>
                                {draft.derivedFrom ? <Input className="mt-2" placeholder="造型名，例如：战袍" value={draft.variantName} onChange={(event) => setDraft({ ...draft, variantName: event.target.value })} /> : null}
                                <div className="mt-1 text-[11px] leading-5 text-foreground/45">造型继承父角色的别名、音色与分类；显示名由父名 + 造型名合成，父角色改名会自动传导。</div>
                            </div>
                        ) : null}
                        {draft.derivedFrom ? null : (
                            <>
                                <div>
                                    <div className="mb-1 text-xs text-foreground/60">名称</div>
                                    <Input value={draft.name} maxLength={120} onChange={(event) => setDraft({ ...draft, name: event.target.value })} />
                                </div>
                                <div>
                                    <div className="mb-1 text-xs text-foreground/60">别名（逗号或空格分隔）</div>
                                    <Input value={draft.aliases} onChange={(event) => setDraft({ ...draft, aliases: event.target.value })} />
                                </div>
                            </>
                        )}
                        <div>
                            <div className="mb-1 text-xs text-foreground/60">设定</div>
                            <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} value={draft.description} onChange={(event) => setDraft({ ...draft, description: event.target.value })} />
                        </div>
                        <div>
                            <div className="mb-1 text-xs text-foreground/60">外观</div>
                            <Input.TextArea autoSize={{ minRows: 2, maxRows: 6 }} value={draft.appearance} onChange={(event) => setDraft({ ...draft, appearance: event.target.value })} />
                        </div>
                        {draft.kind === "character" ? (
                            <div>
                                <div className="mb-1 text-xs text-foreground/60">音色</div>
                                <Input value={draft.voice} onChange={(event) => setDraft({ ...draft, voice: event.target.value })} />
                            </div>
                        ) : null}
                        <div className="flex items-center gap-2">
                            <Switch checked={draft.locked} onChange={(checked) => setDraft({ ...draft, locked: checked })} />
                            <span className="text-xs text-foreground/60">锁定设定：提取只并入别名，不改设定</span>
                        </div>
                    </div>
                ) : null}
            </AppModal>
        </div>
    );
}

function AssetCard({ asset, onEdit, onQueueImage }: { asset: ShortplayAssetSummary; onEdit: () => void; onQueueImage: () => void }) {
    return (
        <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-3">
            <div className="flex items-start gap-3">
                {asset.imageResourceId ? (
                    <img src={resourceFileUrl(asset.imageResourceId)} alt={`${asset.name} 参考图`} className="h-16 w-16 shrink-0 rounded-lg border border-[var(--line-soft)] object-cover" />
                ) : (
                    <div className="flex h-16 w-16 shrink-0 items-center justify-center rounded-lg border border-dashed border-[var(--line-soft)] text-foreground/30">
                        <ImageIcon className="h-5 w-5" />
                    </div>
                )}
                <div className="min-w-0 flex-1">
                    <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                            <div className="truncate text-sm font-medium">
                                #{asset.id} {asset.name}
                                {asset.variantName ? ` · ${asset.variantName}` : ""}
                            </div>
                            <div className="mt-0.5 text-[11px] text-foreground/50">
                                {KIND_LABELS[asset.kind] || asset.kind}
                                {asset.aliases.length ? ` · 别名 ${asset.aliases.join("、")}` : ""}
                                {asset.episodeNumbers.length ? ` · 第 ${asset.episodeNumbers.join("/")} 集` : ""}
                            </div>
                        </div>
                        <div className="flex shrink-0 items-center gap-1">
                            {asset.locked ? <Tag className="m-0">已锁定</Tag> : null}
                            <Tooltip title="编辑这一条资产">
                                <Button size="small" onClick={onEdit}>
                                    编辑
                                </Button>
                            </Tooltip>
                        </div>
                    </div>
                    {asset.description ? <div className="mt-2 line-clamp-3 text-xs leading-5 text-foreground/60">{asset.description}</div> : null}
                    <div className="mt-2 flex items-center justify-between gap-2">
                        <div className="text-[11px] text-foreground/40">
                            图 {asset.imageStatus || "未生成"}
                            {asset.audioStatus ? ` · 音 ${asset.audioStatus}` : ""} · rev {asset.revision}
                        </div>
                        <Tooltip title={asset.locked ? "参考图已锁定，出图会跳过；先解除锁定" : "为这一条资产排队出参考图"}>
                            <Button size="small" icon={<ImageIcon />} disabled={asset.locked} onClick={onQueueImage}>
                                出图
                            </Button>
                        </Tooltip>
                    </div>
                </div>
            </div>
        </div>
    );
}
