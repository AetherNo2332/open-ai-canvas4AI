import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { App, Button, Input, Segmented } from "antd";
import { ArrowLeft, Plus, Trash2 } from "lucide-react";
import { useNavigate } from "react-router";

import { PageHeader, WorkspacePage } from "@/components/layout/workspace-page";
import { createShortplay, type ShortplaySettings } from "@/services/api/shortplay";
import { ASPECT_RATIOS, defaultShortplaySettings } from "./shortplay-shared";

type ChapterDraft = { title: string; text: string };

const EMPTY_CHAPTER: ChapterDraft = { title: "", text: "" };

// 建档：集边界由用户在前端切好（一集=一次提交），服务端只校验不切分、不调模型。
// 建档不建画布、不入队提取；那两步在详情页显式触发。
export default function ShortplayCreatePage() {
    const navigate = useNavigate();
    const { message } = App.useApp();
    const [name, setName] = useState("");
    const [aspectRatio, setAspectRatio] = useState<string>("9:16");
    const [chapters, setChapters] = useState<ChapterDraft[]>([{ ...EMPTY_CHAPTER }, { ...EMPTY_CHAPTER }]);
    const [style, setStyle] = useState(() => defaultShortplaySettings("9:16"));

    const create = useMutation({
        mutationFn: () => {
            const settings: ShortplaySettings = {
                ...style,
                aspectRatio,
            };
            return createShortplay({
                name: name.trim(),
                aspectRatio,
                chapters: chapters.map((chapter) => ({ title: chapter.title.trim(), text: chapter.text })),
                settingsJson: JSON.stringify(settings),
            });
        },
        onSuccess: (result) => {
            message.success("短剧已创建");
            navigate(`/shortplays/${result.projectId}`, { replace: true });
        },
        onError: (error) => message.error(error instanceof Error ? error.message : "创建失败"),
    });

    const updateChapter = (index: number, patch: Partial<ChapterDraft>) => {
        setChapters((current) => current.map((chapter, position) => (position === index ? { ...chapter, ...patch } : chapter)));
    };
    const canSubmit = name.trim().length > 0 && chapters.some((chapter) => chapter.text.trim().length > 0) && !create.isPending;

    return (
        <WorkspacePage>
            <div className="flex items-center gap-2">
                <Button type="text" icon={<ArrowLeft />} onClick={() => navigate("/shortplays")}>
                    返回列表
                </Button>
            </div>
            <PageHeader title="新建短剧" description="粘进已经切好的分集原文；保存后可以在分集里继续维护资产与画布。" />
            <div className="mt-4 grid gap-4 xl:grid-cols-[minmax(0,1fr)_320px]">
                <section className="space-y-4">
                    <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4">
                        <label className="text-xs font-medium text-foreground/70" htmlFor="shortplay-name">
                            作品名
                        </label>
                        <Input id="shortplay-name" className="mt-2" value={name} maxLength={80} placeholder="例如：雨夜回眸" onChange={(event) => setName(event.target.value)} />
                        <div className="mt-4 text-xs font-medium text-foreground/70">画幅</div>
                        <Segmented className="mt-2" value={aspectRatio} options={ASPECT_RATIOS.map((value) => ({ label: value, value }))} onChange={(value) => setAspectRatio(String(value))} />
                    </div>

                    <div className="space-y-3">
                        {chapters.map((chapter, index) => (
                            <div key={index} className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4">
                                <div className="flex items-center justify-between gap-2">
                                    <div className="text-xs font-medium text-foreground/70">第 {index + 1} 集</div>
                                    <Button
                                        type="text"
                                        size="small"
                                        danger
                                        icon={<Trash2 />}
                                        disabled={chapters.length <= 1}
                                        aria-label={`删除第 ${index + 1} 集`}
                                        onClick={() => setChapters((current) => current.filter((_, position) => position !== index))}
                                    />
                                </div>
                                <Input className="mt-2" value={chapter.title} placeholder={`第 ${index + 1} 集标题（可留空）`} onChange={(event) => updateChapter(index, { title: event.target.value })} />
                                <Input.TextArea className="mt-2" autoSize={{ minRows: 4, maxRows: 12 }} value={chapter.text} placeholder="本集原文；单集不超过 12000 字" onChange={(event) => updateChapter(index, { text: event.target.value })} />
                                <div className="mt-2 text-right text-[11px] text-foreground/45">{chapter.text.trim().length} 字</div>
                            </div>
                        ))}
                        <Button block icon={<Plus />} onClick={() => setChapters((current) => [...current, { ...EMPTY_CHAPTER }])}>
                            增加一集
                        </Button>
                    </div>
                </section>

                <aside className="space-y-4">
                    <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4">
                        <div className="text-xs font-medium text-foreground/70">视觉风格</div>
                        <Input className="mt-2" value={style.styleName} placeholder="风格名，例如：写实电影感" onChange={(event) => setStyle((current) => ({ ...current, styleName: event.target.value }))} />
                        <Input.TextArea
                            className="mt-2"
                            autoSize={{ minRows: 2, maxRows: 6 }}
                            value={style.stylePrompt}
                            placeholder="风格提示词，例如：霓虹雨夜，冷调高反差"
                            onChange={(event) => setStyle((current) => ({ ...current, stylePrompt: event.target.value }))}
                        />
                        <div className="mt-3 text-xs font-medium text-foreground/70">对白语言</div>
                        <Input className="mt-2" value={style.language} onChange={(event) => setStyle((current) => ({ ...current, language: event.target.value }))} />
                    </div>
                    <Button type="primary" block size="large" loading={create.isPending} disabled={!canSubmit} onClick={() => create.mutate()}>
                        创建短剧
                    </Button>
                    <div className="text-[11px] leading-5 text-foreground/45">创建时会一次写入作品、域配置与全部分集；画布与资产提取在详情页按需触发。</div>
                </aside>
            </div>
        </WorkspacePage>
    );
}
