import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { App } from "antd";

import { Select } from "@/components/ui/base/select";
import { AppModal } from "@/components/ui/product/app-modal";
import { SHORTPLAY_ASSET_CATEGORY_LABELS, shortplayAssetMetadata, type ShortplayAssetCategory } from "@/lib/canvas/canvas-shortplay-asset";
import { adoptShortplayAsset, getShortplayDetail, listShortplays } from "@/services/api/shortplay";
import type { CanvasNodeData } from "@/types/canvas";

type ShortplayAdoptModalProps = {
    open: boolean;
    /** 源画布：收录要把这张局部卡升格成该画布所属作品之外的资产，所以这里只带画布与节点。 */
    canvasId: string;
    node: CanvasNodeData | null;
    onClose: () => void;
    /** 收录成功回执，页面用来提示与刷新。 */
    onAdopted?: (result: { assetId: number; name: string; episodeNumbers: number[]; created: boolean }) => void;
};

/**
 * 自由画布上的局部卡 → 作品资产 + 目标集画布新卡。
 *
 * 作品与集都要由用户当场选定：画布本身没有短剧归属，归属只能来自这次收录。
 * 分类、名称、设定、图都取自节点快照，后端不接受请求体覆盖，所以这里只负责挑目标。
 */
export function CanvasShortplayAdoptModal({ open, canvasId, node, onClose, onAdopted }: ShortplayAdoptModalProps) {
    const { message } = App.useApp();
    const [shortplayId, setShortplayId] = useState<string>("");
    const [episodeId, setEpisodeId] = useState<string>("");
    const [submitting, setSubmitting] = useState(false);

    const listQuery = useQuery({ queryKey: ["shortplays", "adopt-picker"], queryFn: ({ signal }) => listShortplays(signal), enabled: open });
    const detailQuery = useQuery({
        queryKey: ["shortplays", "adopt-picker", shortplayId],
        queryFn: ({ signal }) => getShortplayDetail(shortplayId, signal),
        enabled: open && Boolean(shortplayId),
    });

    const metadata = node ? shortplayAssetMetadata(node) : null;
    const category: ShortplayAssetCategory | undefined = metadata?.shortplayAssetCategory;
    const name = (node?.title || "").trim();
    const episodes = detailQuery.data?.episodes || [];
    const shortplayOptions = useMemo(() => (listQuery.data?.shortplays || []).map((item) => ({ value: item.projectId, label: `${item.name}（${item.episodeCount} 集）` })), [listQuery.data]);
    const episodeOptions = episodes.map((item) => ({ value: item.id, label: `第 ${item.number} 集 · ${item.title || "未命名"}` }));

    // 可收录的前提都能在提交前说清：没有画布、没有分类、没有名字都收录不了。
    const blocker = !canvasId
        ? "这张画布还没有保存地址，先等画布同步完成"
        : !category
          ? "这张卡还没有分类：先在卡上选「角色 / 道具 / 场景」，再收录"
          : !name
            ? "这张卡没有名称：先给卡起个名字，再收录"
            : !shortplayId
              ? "选择要收录进哪部短剧"
              : !episodeId
                ? "选择目标集"
                : "";

    const submit = async () => {
        if (blocker || submitting || !node) return;
        setSubmitting(true);
        try {
            const result = await adoptShortplayAsset(shortplayId, { canvasId, nodeId: node.id, episodeId });
            const label = result.episodeNumbers.length ? `第 ${result.episodeNumbers.join("/")} 集` : "目标集";
            message.success(result.created ? `已收录为资产 #${result.assetId}「${result.name}」，并在${label}画布放了一张卡` : result.note || `已复用资产 #${result.assetId}「${result.name}」并绑定${label}`);
            onAdopted?.({ assetId: result.assetId, name: result.name, episodeNumbers: result.episodeNumbers, created: result.created });
            onClose();
        } catch (error) {
            message.error(error instanceof Error && error.message ? error.message : "收录失败");
        } finally {
            setSubmitting(false);
        }
    };

    return (
        <AppModal open={open} centered title="收录到短剧" width={520} onCancel={onClose} okText="收录" confirmLoading={submitting} okButtonProps={{ disabled: Boolean(blocker) }} onOk={() => void submit()}>
            <p className="mb-4 text-sm text-muted-foreground">这张局部卡会成为作品资产：新建（或复用同分类同名）资产行，绑定到目标集，并在该集画布上放一张新卡；源卡保持原样不动。</p>
            <div className="grid gap-3">
                <label className="grid gap-1 text-xs text-muted-foreground">
                    要收录的卡
                    <div className="rounded-md border border-border/60 bg-muted/40 px-3 py-2 text-sm text-foreground">
                        {name || "未命名"} · {category ? SHORTPLAY_ASSET_CATEGORY_LABELS[category] : "还没有分类"}
                    </div>
                </label>
                <label className="grid gap-1 text-xs text-muted-foreground">
                    目标短剧
                    <Select
                        ariaLabel="选择目标短剧"
                        appearance="field"
                        showSearch
                        optionFilterProp="label"
                        value={shortplayId || undefined}
                        loading={listQuery.isLoading}
                        options={shortplayOptions}
                        onChange={(value: string) => {
                            setShortplayId(value);
                            setEpisodeId("");
                        }}
                        placeholder={listQuery.isLoading ? "正在读取作品" : shortplayOptions.length ? "选择作品" : "还没有短剧作品"}
                    />
                </label>
                <label className="grid gap-1 text-xs text-muted-foreground">
                    目标集
                    <Select
                        ariaLabel="选择目标集"
                        appearance="field"
                        value={episodeId || undefined}
                        loading={detailQuery.isLoading}
                        disabled={!episodes.length}
                        options={episodeOptions}
                        onChange={(value: string) => setEpisodeId(value)}
                        placeholder={!shortplayId ? "先选作品" : detailQuery.isLoading ? "正在读取分集" : episodes.length ? "选择目标集" : "这部作品还没有分集"}
                    />
                </label>
                {blocker ? <p className="text-xs leading-5 text-amber-600">{blocker}</p> : null}
            </div>
        </AppModal>
    );
}
