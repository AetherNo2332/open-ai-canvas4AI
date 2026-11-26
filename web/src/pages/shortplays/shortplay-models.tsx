import { Select } from "antd";
import { useQuery } from "@tanstack/react-query";

import { getDramaModels, type DramaCapability, type DramaModelChoice } from "@/services/api/drama";
import type { ShortplayModels } from "@/services/api/shortplay";

// 短剧设置里的模型槽：提取文本、参考图（默认 + 三类覆盖）、音色。
// 目录来自平台同一份 `/dramas/models`（短剧保存时用同一道目录校验），不另开一套目录接口。
// 留空 = 跟随平台默认，由服务端在派发时解析；页面不把默认值写死进设置。

type Slot = {
    key: keyof ShortplayModels;
    label: string;
    capability: DramaCapability | "audio";
    hint?: string;
};

const SLOTS: Slot[] = [
    { key: "text", label: "资产提取文本模型", capability: "text", hint: "提取任务的提示词与结构解析走文本模型" },
    { key: "bibleText", label: "资产提取覆盖", capability: "text", hint: "填写后优先于上面的文本模型" },
    { key: "image", label: "参考图默认图片模型", capability: "image" },
    { key: "characterImage", label: "角色参考图覆盖", capability: "image" },
    { key: "propImage", label: "道具参考图覆盖", capability: "image" },
    { key: "sceneImage", label: "场景参考图覆盖", capability: "image" },
    { key: "audio", label: "音色样本音频模型", capability: "audio", hint: "只有角色有音色槽" },
];

/** 目录里的一个选择压成字符串键：平台目录模型用 logicalModelId，渠道模型用 channelId|modelKey。 */
function choiceValue(choice?: DramaModelChoice): string {
    if (!choice) return "";
    if (choice.logicalModelId) return `logical:${choice.logicalModelId}`;
    if (choice.channelId && choice.modelKey) return `channel:${choice.channelId}|${choice.modelKey}`;
    return "";
}

export function ShortplayModelPickers({ models, onChange }: { models: ShortplayModels; onChange: (next: ShortplayModels) => void }) {
    const catalog = useQuery({ queryKey: ["drama-models"], queryFn: getDramaModels, staleTime: 60_000, retry: false });

    return (
        <div className="rounded-xl border border-[var(--line-soft)] bg-[var(--surface-1)] p-4">
            <div className="text-xs font-medium text-foreground/70">模型</div>
            <div className="mt-1 text-[11px] leading-5 text-foreground/45">目录与 drama 共用；留空表示跟随平台默认。选了目录里没有的模型时保存会被服务端拒绝，不会带着非法选择开工。</div>
            {catalog.isError ? <div className="mt-2 text-[11px] text-[var(--danger,#d14343)]">模型目录读取失败：{catalog.error instanceof Error ? catalog.error.message : "未知错误"}；已保存的设置不受影响。</div> : null}
            <div className="mt-3 space-y-3">
                {SLOTS.map((slot) => {
                    const options = (catalog.data?.options?.[slot.capability] ?? []).map((item) => ({
                        value: choiceValue(item.choice),
                        label: `${item.label}${item.priceLabel ? ` · ${item.priceLabel}` : ""}`,
                    }));
                    const current = models[slot.key];
                    return (
                        <div key={slot.key}>
                            <div className="mb-1 text-xs text-foreground/60">{slot.label}</div>
                            <Select
                                allowClear
                                className="w-full"
                                size="small"
                                placeholder={catalog.isLoading ? "正在读取目录…" : "跟随平台默认"}
                                value={choiceValue(current) || undefined}
                                loading={catalog.isLoading}
                                options={options}
                                onChange={(value?: string) => {
                                    const next = { ...models };
                                    if (!value) {
                                        delete next[slot.key];
                                    } else {
                                        const item = (catalog.data?.options?.[slot.capability] ?? []).find((option) => choiceValue(option.choice) === value);
                                        if (item) next[slot.key] = item.choice;
                                    }
                                    onChange(next);
                                }}
                            />
                            {slot.hint ? <div className="mt-0.5 text-[11px] text-foreground/40">{slot.hint}</div> : null}
                        </div>
                    );
                })}
            </div>
        </div>
    );
}
