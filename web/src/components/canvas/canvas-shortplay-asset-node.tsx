import { AudioLines, Image as ImageIcon } from "lucide-react";
import { useEffect, useState } from "react";

import { getResourceAccess, resolveResourceAccessURL, resourceStorageKey } from "@/services/api/resources";
import { SHORTPLAY_ASSET_CATEGORY_LABELS, shortplayAssetMetadata, shortplayAssetPlacement, shortplayAssetInitial } from "@/lib/canvas/canvas-shortplay-asset";
import type { CanvasNodeData } from "@/types/canvas";

const RESOURCE_FILE_PATTERN = /\/resources\/([^/?#]+)\/file/;

/** 资产卡封面存的是 `/resources/:id/file`，展示前换成授权访问地址；换取失败再回落原地址。 */
export function useShortplayAssetCoverSrc(url?: string) {
    const [resolved, setResolved] = useState<{ url: string; src: string } | null>(null);
    const resourceId = url ? RESOURCE_FILE_PATTERN.exec(url)?.[1] : undefined;
    useEffect(() => {
        if (!url || !resourceId) return;
        let cancelled = false;
        getResourceAccess(resourceStorageKey(decodeURIComponent(resourceId)), "display")
            .then((access) => {
                if (!cancelled) setResolved({ url, src: resolveResourceAccessURL(access.url) || url });
            })
            .catch(() => {
                if (!cancelled) setResolved({ url, src: url });
            });
        return () => {
            cancelled = true;
        };
    }, [resourceId, url]);
    if (!url) return "";
    if (!resourceId) return url;
    return resolved?.url === url ? resolved.src : "";
}

/**
 * 集画布上的短剧资产卡：竖版海报式主视觉 + 标题/别名 + 正文预览。
 *
 * `linked` 表示当前画布确实是某部短剧的分集画布：归属徽标据此显示「项目资产」或「局部卡」。
 * 卡片不写表、不发请求；表改动由详情页资产库发起，画布只按快照渲染。
 */
export function ShortplayAssetNodeContent({ node, linked = false }: { node: CanvasNodeData; linked?: boolean }) {
    const metadata = shortplayAssetMetadata(node);
    const name = node.title || metadata?.content?.split("\n")[0]?.trim() || "资产";
    const cover = useShortplayAssetCoverSrc(metadata?.shortplayAssetCoverUrl);
    const category = metadata?.shortplayAssetCategory;
    const aliases = (metadata?.shortplayAssetAliases || []).filter(Boolean);
    const voice = metadata?.shortplayAssetVoice || "";
    const body = (metadata?.content || "").trim();
    const placement = shortplayAssetPlacement(node, linked);
    const visualReady = metadata?.shortplayAssetImageStatus === "ready";
    const imagePending = metadata?.shortplayAssetImageStatus === "queued" || metadata?.shortplayAssetImageStatus === "running";
    const variantName = metadata?.shortplayAssetVariantName || "";
    const derived = Boolean(metadata?.shortplayAssetDerivedFrom);
    const baseImageStale = metadata?.shortplayAssetBaseImageStale === true;
    const baseAudioStale = metadata?.shortplayAssetBaseAudioStale === true;
    const audioStatus = metadata?.shortplayAssetAudioStatus;
    const hasAudio = Boolean(metadata?.shortplayAssetAudioResourceId);
    const audioPending = audioStatus === "queued" || audioStatus === "running";

    return (
        <div className="relative isolate flex h-full w-full flex-col overflow-hidden bg-[#111114] text-white">
            <div className="relative h-[196px] w-full shrink-0 overflow-hidden">
                {cover ? (
                    <>
                        <img src={cover} alt="" aria-hidden className="absolute inset-0 -z-10 h-full w-full scale-125 object-cover opacity-50 blur-2xl saturate-125" draggable={false} />
                        <img src={cover} alt={name} className="absolute inset-0 h-full w-full object-contain px-3 pt-3 drop-shadow-[0_18px_30px_rgba(0,0,0,.45)]" draggable={false} />
                    </>
                ) : (
                    <div className="absolute inset-0 grid place-items-center bg-[radial-gradient(circle_at_50%_40%,rgba(255,255,255,.1),transparent_62%)]">
                        <span className="grid size-16 place-items-center rounded-full border border-white/15 bg-white/[.03] text-2xl font-normal text-white/75">{shortplayAssetInitial(name)}</span>
                    </div>
                )}
                <div className="pointer-events-none absolute inset-x-0 bottom-0 h-1/2 bg-gradient-to-t from-[#111114] via-[#111114]/55 to-transparent" />
                <span className="absolute left-3 top-2.5 rounded-full border border-white/15 bg-black/45 px-2 py-0.5 text-[var(--fs-tiny)] font-medium text-white/75 backdrop-blur">{category ? SHORTPLAY_ASSET_CATEGORY_LABELS[category] : "资产"}</span>
                {derived ? <span className="absolute left-3 top-9 rounded-full border border-white/20 bg-black/55 px-2 py-0.5 text-[var(--fs-tiny)] font-medium text-white/85 backdrop-blur">{variantName || "造型"}</span> : null}
                {baseImageStale ? (
                    <span
                        className="absolute bottom-2.5 left-3 inline-flex items-center gap-1 rounded-full border border-amber-300/40 bg-amber-400/20 px-2 py-0.5 text-[var(--fs-tiny)] font-medium text-amber-100 backdrop-blur"
                        title="父资产换过参考图；这张卡仍显示上一次生成的图"
                    >
                        父图已变
                    </span>
                ) : null}
                <span
                    className={`absolute right-3 top-2.5 inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[var(--fs-tiny)] font-medium backdrop-blur ${
                        placement === "domain" ? "border-emerald-300/35 bg-emerald-400/15 text-emerald-100" : "border-white/15 bg-black/45 text-white/65"
                    }`}
                >
                    {placement === "domain" ? <ImageIcon className="size-2.5" /> : null}
                    {placement === "domain" ? "项目资产" : "局部卡"}
                </span>
            </div>
            <div className="flex min-h-0 flex-1 flex-col px-3.5 pb-3 pt-1">
                <div className="truncate text-[var(--fs-heading)] font-semibold leading-6 tracking-tight" title={name}>
                    {name}
                </div>
                <div className="truncate text-[var(--fs-label)] leading-4 text-white/55">
                    {[aliases.length ? `别名 ${aliases.join("、")}` : "", voice ? `音色 ${voice}` : ""].filter(Boolean).join("　") || (imagePending ? "参考图生成中" : visualReady ? "参考图就绪" : "还没有参考图")}
                </div>
                {hasAudio || audioPending || baseAudioStale ? (
                    <div className="mt-1 flex items-center gap-1 text-[var(--fs-tiny)] text-white/55">
                        <AudioLines className="size-3" />
                        <span className="truncate">{baseAudioStale ? "父音色已变" : audioPending ? "音色生成中" : "音色就绪"}</span>
                    </div>
                ) : null}
                <div className="thin-scrollbar mt-2 min-h-0 flex-1 overflow-hidden whitespace-pre-wrap break-words text-[var(--fs-label)] leading-5 text-white/60" onWheel={(event) => event.stopPropagation()}>
                    {body || <span className="text-white/30">还没有设定正文</span>}
                </div>
            </div>
        </div>
    );
}
