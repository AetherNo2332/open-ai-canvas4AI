import type { ShortplayAssetSettings, ShortplaySettings } from "@/services/api/shortplay";

// 独立 shortplay 工作台的页面常量与设置解析。纯函数，不依赖 React。

export const ASPECT_RATIOS = ["9:16", "16:9", "1:1", "3:4", "4:3"] as const;

export type AspectRatio = (typeof ASPECT_RATIOS)[number];

export const ASSET_MODES: Array<{ value: ShortplayAssetSettings["mode"]; label: string }> = [
    { value: "portrait", label: "单人设定图" },
    { value: "turnaround", label: "三视图" },
    { value: "fourview", label: "四视图" },
    { value: "custom", label: "自定义" },
];

export const EMPTY_ASSET_SETTINGS: ShortplayAssetSettings = {
    mode: "portrait",
    characterPrompt: "",
    scenePrompt: "",
    characterRatio: "16:9",
};

/** 设置页唯一的权威默认值来源；后端归一不一致时以保存回执为准。 */
export function defaultShortplaySettings(aspectRatio?: string): ShortplaySettings {
    return {
        aspectRatio: aspectRatio || "9:16",
        styleName: "",
        stylePrompt: "",
        language: "中文",
        assets: { ...EMPTY_ASSET_SETTINGS },
        models: {},
    };
}

/**
 * 解析 settingsJson。坏 JSON 按「从没保存过设置」处理，不让整页打不开；
 * 画幅优先用容器行的值，因为保存时后端会以容器行为准覆盖。
 */
export function parseShortplaySettings(raw: string | undefined, aspectRatio?: string): ShortplaySettings {
    const fallback = defaultShortplaySettings(aspectRatio);
    if (!raw || !raw.trim()) return fallback;
    let parsed: Partial<ShortplaySettings>;
    try {
        parsed = JSON.parse(raw) as Partial<ShortplaySettings>;
    } catch {
        return fallback;
    }
    if (!parsed || typeof parsed !== "object") return fallback;
    const assets = parsed.assets && typeof parsed.assets === "object" ? parsed.assets : EMPTY_ASSET_SETTINGS;
    return {
        aspectRatio: aspectRatio || parsed.aspectRatio || fallback.aspectRatio,
        styleName: parsed.styleName || "",
        stylePrompt: parsed.stylePrompt || "",
        language: parsed.language || fallback.language,
        assets: {
            ...EMPTY_ASSET_SETTINGS,
            ...assets,
            mode: assets.mode || EMPTY_ASSET_SETTINGS.mode,
            characterRatio: assets.characterRatio || EMPTY_ASSET_SETTINGS.characterRatio,
        },
        models: parsed.models && typeof parsed.models === "object" ? parsed.models : {},
        extractSkill: parsed.extractSkill?.skillId ? parsed.extractSkill : undefined,
    };
}

/** 有没有未保存的改动。逐字段比而不是引用比：每次编辑都是新对象。 */
export function settingsDirty(current: ShortplaySettings, saved: ShortplaySettings) {
    return JSON.stringify(current) !== JSON.stringify(saved);
}
