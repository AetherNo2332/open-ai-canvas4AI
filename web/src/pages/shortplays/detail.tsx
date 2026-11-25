import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { App, Button, Input, Segmented, Tabs, Tooltip } from "antd";
import { ArrowLeft, Film, Save } from "lucide-react";
import { useNavigate, useParams } from "react-router";

import { PageHeader, WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceErrorState, WorkspaceLoadingState } from "@/components/layout/workspace-state";
import { RelativeTime } from "@/components/ui/product/relative-time";
import { getShortplayDetail, updateShortplaySettings, type ShortplaySettings } from "@/services/api/shortplay";
import { ASSET_MODES, parseShortplaySettings, settingsDirty } from "./shortplay-shared";

// 详情：分集（原文、字数、画布地址）与设置（整份保存，回执为权威）。
// 资产、提取收件箱、集画布与收录按 W04–W06 接入同一页。
export default function ShortplayDetailPage() {
    const { projectId = "" } = useParams();
    const navigate = useNavigate();
    const queryClient = useQueryClient();
    const { message } = App.useApp();

    const query = useQuery({
        queryKey: ["shortplay", projectId],
        queryFn: ({ signal }) => getShortplayDetail(projectId, signal),
        enabled: Boolean(projectId),
    });

    const savedSettings = useMemo(() => parseShortplaySettings(query.data?.settingsJson, query.data?.aspectRatio), [query.data?.settingsJson, query.data?.aspectRatio]);
    const [settings, setSettings] = useState<ShortplaySettings>(savedSettings);
    useEffect(() => setSettings(savedSettings), [savedSettings]);
    const dirty = settingsDirty(settings, savedSettings);

    const save = useMutation({
        mutationFn: () => updateShortplaySettings(projectId, settings),
        onSuccess: () => {
            message.success("设置已保存");
            void queryClient.invalidateQueries({ queryKey: ["shortplay", projectId] });
            void queryClient.invalidateQueries({ queryKey: ["shortplays"] });
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "保存失败"),
    });

    if (query.isLoading) {
        return (
            <WorkspacePage>
                <WorkspaceLoadingState label="正在读取短剧" />
            </WorkspacePage>
        );
    }
    if (query.isError || !query.data) {
        return (
            <WorkspacePage>
                <WorkspaceErrorState description="短剧详情暂时无法读取。" onRetry={() => void query.refetch()} />
            </WorkspacePage>
        );
    }

    const detail = query.data;
    const episodes = detail.episodes;
    const totalRunes = episodes.reduce((sum, episode) => sum + episode.wordCount, 0);

    return (
        <WorkspacePage>
            <Button type="text" icon={<ArrowLeft />} onClick={() => navigate("/shortplays")}>
                返回列表
            </Button>
            <PageHeader
                title={detail.name}
                description={`${detail.aspectRatio} · ${episodes.length} 集 · 共 ${totalRunes} 字 · 更新于 `}
                meta={
                    <span className="inline-flex items-center gap-1 text-xs text-foreground/60">
                        <Film className="h-3.5 w-3.5" /> 短剧
                    </span>
                }
            />
            <div className="mt-2 text-[11px] text-foreground/45">
                更新于 <RelativeTime value={detail.updatedAt} />
            </div>

            <Tabs
                className="mt-3"
                items={[
                    {
                        key: "episodes",
                        label: `分集（${episodes.length}）`,
                        children: (
                            <div className="space-y-2">
                                {episodes.map((episode) => (
                                    <div key={episode.id} className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4">
                                        <div className="flex flex-wrap items-start justify-between gap-3">
                                            <div className="min-w-0">
                                                <div className="text-sm font-semibold">
                                                    第 {episode.number} 集 · {episode.title}
                                                </div>
                                                <div className="mt-1 text-xs text-foreground/55">
                                                    {episode.wordCount} 字 · 画布地址 {episode.canvasId || "未分配"}
                                                    {episode.assetIds.length ? ` · ${episode.assetIds.length} 个资产` : ""}
                                                </div>
                                            </div>
                                            <Tooltip title="集画布在 W06 接入后创建">
                                                <Button disabled>进精修画布</Button>
                                            </Tooltip>
                                        </div>
                                        {episode.scriptText ? <div className="mt-3 max-h-40 overflow-y-auto whitespace-pre-wrap rounded-lg bg-[var(--surface-2)] p-3 text-xs leading-6 text-foreground/70">{episode.scriptText}</div> : null}
                                    </div>
                                ))}
                                {episodes.length === 0 ? <div className="rounded-xl border border-dashed border-[var(--line-soft)] p-8 text-center text-xs text-foreground/50">还没有分集。</div> : null}
                            </div>
                        ),
                    },
                    {
                        key: "settings",
                        label: "设置",
                        children: (
                            <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
                                <section className="space-y-4">
                                    <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4">
                                        <div className="text-xs font-medium text-foreground/70">画幅</div>
                                        <div className="mt-1 text-xs text-foreground/50">画幅以作品容器行为准，创建后不在此修改。</div>
                                        <div className="mt-2 text-sm font-medium">{detail.aspectRatio}</div>
                                    </div>
                                    <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4">
                                        <div className="text-xs font-medium text-foreground/70">视觉风格</div>
                                        <Input className="mt-2" value={settings.styleName} placeholder="风格名" onChange={(event) => setSettings((current) => ({ ...current, styleName: event.target.value }))} />
                                        <Input.TextArea
                                            className="mt-2"
                                            autoSize={{ minRows: 2, maxRows: 8 }}
                                            value={settings.stylePrompt}
                                            placeholder="风格提示词"
                                            onChange={(event) => setSettings((current) => ({ ...current, stylePrompt: event.target.value }))}
                                        />
                                        <div className="mt-3 text-xs font-medium text-foreground/70">对白语言</div>
                                        <Input className="mt-2" value={settings.language} onChange={(event) => setSettings((current) => ({ ...current, language: event.target.value }))} />
                                    </div>
                                    <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4">
                                        <div className="text-xs font-medium text-foreground/70">参考图方案</div>
                                        <Segmented
                                            className="mt-2"
                                            value={settings.assets.mode}
                                            options={ASSET_MODES.map((item) => ({ label: item.label, value: item.value }))}
                                            onChange={(value) => setSettings((current) => ({ ...current, assets: { ...current.assets, mode: value as ShortplaySettings["assets"]["mode"] } }))}
                                        />
                                        <div className="mt-3 text-xs font-medium text-foreground/70">角色参考图画幅</div>
                                        <Input className="mt-2" value={settings.assets.characterRatio} onChange={(event) => setSettings((current) => ({ ...current, assets: { ...current.assets, characterRatio: event.target.value } }))} />
                                        <div className="mt-3 text-xs font-medium text-foreground/70">角色提示词</div>
                                        <Input.TextArea
                                            className="mt-2"
                                            autoSize={{ minRows: 2, maxRows: 6 }}
                                            value={settings.assets.characterPrompt}
                                            onChange={(event) => setSettings((current) => ({ ...current, assets: { ...current.assets, characterPrompt: event.target.value } }))}
                                        />
                                        <div className="mt-3 text-xs font-medium text-foreground/70">场景提示词</div>
                                        <Input.TextArea
                                            className="mt-2"
                                            autoSize={{ minRows: 2, maxRows: 6 }}
                                            value={settings.assets.scenePrompt}
                                            onChange={(event) => setSettings((current) => ({ ...current, assets: { ...current.assets, scenePrompt: event.target.value } }))}
                                        />
                                    </div>
                                </section>
                                <aside className="space-y-3">
                                    <Button type="primary" block size="large" icon={<Save />} loading={save.isPending} disabled={!dirty} onClick={() => save.mutate()}>
                                        保存设置
                                    </Button>
                                    <div className="text-[11px] leading-5 text-foreground/45">{dirty ? "有未保存的改动。" : "设置与服务端一致。"}整份提交，服务端归一后的回执才是权威值。</div>
                                    {detail.jobs.length ? (
                                        <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-3">
                                            <div className="text-xs font-medium text-foreground/70">最近任务</div>
                                            <div className="mt-2 space-y-1 text-[11px] text-foreground/55">
                                                {detail.jobs.slice(0, 6).map((job) => (
                                                    <div key={job.id}>
                                                        {job.kind} · {job.status}
                                                    </div>
                                                ))}
                                            </div>
                                        </div>
                                    ) : null}
                                </aside>
                            </div>
                        ),
                    },
                ]}
            />
        </WorkspacePage>
    );
}
