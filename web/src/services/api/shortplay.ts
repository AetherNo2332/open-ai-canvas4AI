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
    /** 音色样本的音频模型；为空跟随平台目录第一个可用项。 */
    audio?: ShortplayModelChoice;
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

/** 资产读模型：身份 + 设定 + 当前图/音投影 + 分集绑定 + 行版本。 */
export type ShortplayAssetSummary = {
    id: number;
    kind: string;
    name: string;
    aliases: string[];
    description: string;
    appearance: string;
    voice: string;
    locked: boolean;
    imageResourceId?: string;
    imageStatus: string;
    imageVersion: number;
    audioResourceId?: string;
    audioStatus?: string;
    audioVersion?: number;
    episodeNumbers: number[];
    revision: number;
    derivedFrom?: number;
    variantName?: string;
    renderRule?: string;
};

/** 资产写入：assetId>0 是更新，否则按作品内下一个序号新建。derivedFrom>0 表示新建衍生造型。 */
export type ShortplayAssetWrite = {
    assetId?: number;
    kind: string;
    name: string;
    aliases?: string[];
    description?: string;
    appearance?: string;
    voice?: string;
    locked?: boolean;
    derivedFrom?: number;
    variantName?: string;
    renderRule?: string;
};

/** 参考图入队筛选：单个资产、某类缺图或某类失败项。 */
export type ShortplayImageQueueRequest = {
    kind?: string;
    assetIds?: number[];
    onlyMissing?: boolean;
    onlyFailed?: boolean;
};

/** 三档计数：新排队 / 已在途复用 / 跳过（锁定或不在筛选范围）。 */
export type ShortplayImageQueueResult = { queued: number; reused: number; skipped: number; jobIds: string[] };

/** 音色样本入队筛选：单个角色、缺音或失败项；非角色与锁定行由服务端计入跳过。 */
export type ShortplayAudioQueueRequest = {
    assetIds?: number[];
    onlyMissing?: boolean;
    onlyFailed?: boolean;
};

/** 音色样本入队回执，与参考图同形。 */
export type ShortplayAudioQueueResult = { queued: number; reused: number; skipped: number; jobIds: string[] };

/** 提取入队回执：queued=false 表示已有在途提取，返回的是那一条。 */
export type ShortplayExtractQueueResult = { jobId: string; queued: boolean; cursor: number };

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

/** 资产库：角色 / 道具 / 场景的身份与设定；图与音的生成在 W05 接入。 */
export function listShortplayAssets(projectId: string, signal?: AbortSignal) {
    return http.get<{ assets: ShortplayAssetSummary[] | null }>(`/shortplays/${encodeURIComponent(projectId)}/assets`, { signal });
}

/** 写一条基础资产：带 assetId 是 revision CAS 更新，不带则新建。 */
export function upsertShortplayAsset(projectId: string, body: ShortplayAssetWrite) {
    return http.post<ShortplayAssetSummary>(`/shortplays/${encodeURIComponent(projectId)}/assets`, body);
}

/**
 * 把资产提取排进 job 队列。重复点击返回已有在途 job（queued=false），
 * 不在前端做去重，去重键与游标都在服务端。
 */
export function queueShortplayExtract(projectId: string) {
    return http.post<ShortplayExtractQueueResult>(`/shortplays/${encodeURIComponent(projectId)}/extract`);
}

/**
 * 参考图出图入队。页面只表达意图，任务提交与计费在服务端调度里；
 * 同一资产已有在途 job 时复用（reused ），不排第二条。
 */
export function queueShortplayAssetImages(projectId: string, body: ShortplayImageQueueRequest) {
    return http.post<ShortplayImageQueueResult>(`/shortplays/${encodeURIComponent(projectId)}/assets/images`, body);
}

/** 音色样本入队：只排角色；任务提交与计费在服务端调度里。 */
export function queueShortplayAssetAudio(projectId: string, body: ShortplayAudioQueueRequest) {
    return http.post<ShortplayAudioQueueResult>(`/shortplays/${encodeURIComponent(projectId)}/assets/audio`, body);
}

/** 集画布按需创建：第一次调用建出画布行，已存在返回 created=false。 */
export function ensureShortplayEpisodeCanvas(projectId: string, episodeId: string) {
    return http.post<{ canvasId: string; created: boolean }>(`/shortplays/${encodeURIComponent(projectId)}/episodes/${encodeURIComponent(episodeId)}/canvas`);
}

/** 按画布反查集归属；自由画布返回 linked=false，调用方按普通画布处理。 */
export function getShortplayCanvasBinding(canvasId: string, signal?: AbortSignal) {
    return http.get<{ linked: boolean; shortplayId?: string; projectId?: string; episodeId?: string; episodeNumber?: number; episodeTitle?: string }>(`/shortplays/by-canvas/${encodeURIComponent(canvasId)}`, { signal });
}

/**
 * 保存设置（整份提交）。服务端归一后回传 settingsJson，回执里的值才是权威。
 * 整份提交而不是逐字段 patch：设置之间互相牵制，逐字段写会落库组合非法的中间态。
 */
export function updateShortplaySettings(projectId: string, settings: ShortplaySettings) {
    return http.patch<{ settingsJson: string }>(`/shortplays/${encodeURIComponent(projectId)}/settings`, { settings });
}
