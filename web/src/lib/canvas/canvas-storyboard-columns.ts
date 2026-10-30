import type { StoryboardColumn } from "@/types/canvas";

/**
 * 分镜脚本节点的默认展示列。
 *
 * 新建节点、历史默认迁移、生成结果回写和 Agent 读取的缺省值都以这里为准；
 * 默认值只在这一处维护，其他调用方从这里 import。
 */
export const DEFAULT_STORYBOARD_COLUMNS: readonly StoryboardColumn[] = ["shotNumber", "durationSeconds", "plotDescription", "shotSize", "motion", "assets"];

/**
 * 旧版本写入节点 metadata 的默认集合：读取时归一到当前默认，避免旧默认锁死新默认。
 *
 * 最后一套是自动生成的“电影感”十五列：它由生成流程补出来，不是用户手选，
 * 因此同样视为历史默认，迁移到当前默认。
 */
const LEGACY_DEFAULT_STORYBOARD_COLUMN_SETS: ReadonlyArray<readonly StoryboardColumn[]> = [
    ["shotNumber", "durationSeconds", "videoMotionPrompt", "dialogue", "assets"],
    ["shotNumber", "durationSeconds", "plotDescription", "dialogue"],
    ["shotNumber", "plotDescription", "videoMotionPrompt", "dialogue"],
    ["shotNumber", "durationSeconds", "videoMotionPrompt", "dialogue", "assets", "shotSize", "narrativeIntent", "viewerPOV", "performanceBlocking", "camera", "motion", "timeBeats", "lightingAndAtmosphere", "continuityOut", "negativePrompt"],
];

/** 全屏编辑器的列顺序与中文标签；画布节点卡片按同一套顺序渲染。 */
export const STORYBOARD_COLUMN_OPTIONS: ReadonlyArray<{ label: string; value: StoryboardColumn }> = [
    { label: "序号", value: "shotNumber" },
    { label: "时长", value: "durationSeconds" },
    { label: "画面描述", value: "plotDescription" },
    { label: "台词/旁白", value: "dialogue" },
    { label: "镜头意图", value: "narrativeIntent" },
    { label: "观众视点", value: "viewerPOV" },
    { label: "表演调度", value: "performanceBlocking" },
    { label: "景别", value: "shotSize" },
    { label: "情绪", value: "emotion" },
    { label: "光影氛围", value: "lightingAndAtmosphere" },
    { label: "音效", value: "audioEffects" },
    { label: "镜头设计", value: "camera" },
    { label: "运镜", value: "motion" },
    { label: "时间节拍", value: "timeBeats" },
    { label: "图片提示词", value: "imageGenerationPrompt" },
    { label: "视频提示词", value: "videoMotionPrompt" },
    { label: "关联资产", value: "assets" },
    { label: "连续性出口", value: "continuityOut" },
    { label: "负面要求", value: "negativePrompt" },
];

export function storyboardColumnLabel(column: StoryboardColumn) {
    return STORYBOARD_COLUMN_OPTIONS.find((option) => option.value === column)?.label ?? column;
}

/**
 * 解析节点里持久化的可见列：
 * - 缺失或为空时用当前默认；
 * - 与任一历史默认集合完全一致时迁移到当前默认；
 * - 其他情况视为用户手选，原样保留（返回新数组，避免调用方改到节点数据）。
 */
export function resolveStoryboardVisibleColumns(columns?: readonly StoryboardColumn[] | null): StoryboardColumn[] {
    if (!columns?.length) return [...DEFAULT_STORYBOARD_COLUMNS];
    const isLegacyDefault = LEGACY_DEFAULT_STORYBOARD_COLUMN_SETS.some((legacy) => legacy.length === columns.length && legacy.every((column) => columns.includes(column)));
    return isLegacyDefault ? [...DEFAULT_STORYBOARD_COLUMNS] : [...columns];
}
