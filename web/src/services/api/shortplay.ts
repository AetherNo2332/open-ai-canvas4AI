import { http } from "@/services/api/request";

// 独立 shortplay 工作台：建档、分集、设置，以及后续分片的资产、提取、收录与集画布。
// 与 drama 制作台是两条产品线，共用 `/projects` 容器的删除入口，不复用对方的接口形状。

/** 一次模型选择：平台目录模型只有 logicalModelId，渠道模型是 channelId + modelKey 成对。 */
export type ShortplayModelChoice = { logicalModelId?: string; channelId?: string; modelKey?: string; label?: string; priceLabel?: string };

/** 各步骤与各类参考图的模型：资产提取用 bibleText（跟随 text），参考图用 image 及其三类覆盖。 */
export type ShortplayModels = {
    text?: ShortplayModelChoice;
    bibleText?: ShortplayModelChoice;
    image?: ShortplayModelChoice;
    characterImage?: ShortplayModelChoice;
    propImage?: ShortplayModelChoice;
    sceneImage?: ShortplayModelChoice;
};

/** 参考图方案，与 drama 的资产方案同一份契约：方案版式 + 角色/场景提示词 + 角色画幅 + 分辨率档位。 */
export type ShortplayAssetSettings = {
    mode: "portrait" | "turnaround" | "fourview" | "custom";
    characterPrompt: string;
    scenePrompt: string;
    characterRatio: string;
    resolution?: string;
    propResolution?: string;
    sceneResolution?: string;
    sourceSkillId?: string;
    sourceSkillName?: string;
    sourcePath?: string;
};

/** 用户技能库里的一个技能文件，作为资产提取的写作方法。 */
export type ShortplaySkillRef = { skillId: string; skillName: string; path: string };

/** 短剧设置：只包含短剧真正会用的项，不含分镜规则、视频规格与审阅方式。 */
export type ShortplaySettings = {
    aspectRatio: string;
    styleName: string;
    stylePrompt: string;
    language: string;
    assets: ShortplayAssetSettings;
    models: ShortplayModels;
    extractSkill?: ShortplaySkillRef;
};

/** 一集的摘要。正文随详情返回（scriptText），列表与建档响应不带。 */
export type ShortplayEpisodeSummary = {
    id: string;
    number: number;
    title: string;
    /** 画布地址：建档时就分配，画布行由「进精修画布」按需建出来。 */
    canvasId?: string;
    wordCount: number;
    scriptText?: string;
    /** 本集用到的资产 id（同一作品内从 1 开始的整数序号）。 */
    assetIds: number[];
};

/** job 状态与后端状态机同词。 */
export type ShortplayJob = {
    id: string;
    shortplayId: string;
    canvasId?: string;
    kind: string;
    status: "queued" | "running" | "succeeded" | "failed" | "canceled";
    priority: number;
    attempt: number;
    taskId?: string;
    lastError?: string;
    costMicrocredits: number;
    startedAt?: string;
    finishedAt?: string;
    createdAt: string;
    updatedAt: string;
};

/** 作品详情：容器行 + 域配置 + 全部集 + 最近 job（jobs 恒为数组）。 */
export type ShortplayDetail = {
    projectId: string;
    shortplayId: string;
    name: string;
    aspectRatio: string;
    defaultImageModel?: string;
    defaultVideoModel?: string;
    settingsJson: string;
    createdAt: string;
    updatedAt: string;
    episodes: ShortplayEpisodeSummary[];
    jobs: ShortplayJob[];
};

export type ShortplayListItem = {
    projectId: string;
    shortplayId: string;
    name: string;
    aspectRatio: string;
    episodeCount: number;
    createdAt: string;
    updatedAt: string;
};

export function listShortplays(signal?: AbortSignal) {
    return http.get<{ shortplays: ShortplayListItem[] | null }>("/shortplays", { signal });
}

/**
 * 建档：集边界由前端切好再提交，服务端只校验（单集字数上限、集数上限），不调模型分集。
 */
export function createShortplay(body: { name: string; aspectRatio: string; chapters: Array<{ title: string; text: string }>; settingsJson?: string }) {
    return http.post<{ projectId: string; shortplayId: string; episodes: ShortplayEpisodeSummary[] }>("/shortplays", body);
}

export function getShortplayDetail(projectId: string, signal?: AbortSignal) {
    return http.get<ShortplayDetail>(`/shortplays/${encodeURIComponent(projectId)}`, { signal });
}

/**
 * 删除整部作品（含全部集画布与域表行）。走上游 `DELETE /projects/:id`：
 * 级联删除只在一处实现，短剧域不该有第二份删除逻辑。
 */
export function deleteShortplay(projectId: string) {
    return http.delete<{ id: string }>(`/projects/${encodeURIComponent(projectId)}`);
}

/**
 * 保存设置（整份提交）。服务端归一后回传 settingsJson，回执里的值才是权威。
 * 整份提交而不是逐字段 patch：设置之间互相牵制，逐字段写会落库组合非法的中间态。
 */
export function updateShortplaySettings(projectId: string, settings: ShortplaySettings) {
    return http.patch<{ settingsJson: string }>(`/shortplays/${encodeURIComponent(projectId)}/settings`, { settings });
}
