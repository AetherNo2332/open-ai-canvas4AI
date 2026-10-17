import type { PrevisScene } from "@/types/previs";

export type PrevisImageStagingStatus = "staging" | "approved" | "committed" | "rejected";

/**
 * 图片→预演台事务的 staging draft：独立于权威场景的工作副本。
 * 图片流程只做 inspect → draft → approve(连续性检查通过或用户确认) → 原子提交；
 * 失败/取消/超时一律回到 rejected，权威场景与画布结构不受影响。
 */
export type PrevisImageStagingDraft = {
    id: string;
    sceneId: string;
    /** staging 时权威场景的 updatedAt —— 提交的 CAS 基线。 */
    baseUpdatedAt: string;
    scene: PrevisScene;
    status: PrevisImageStagingStatus;
    createdAt: string;
    expiresAt: string;
};

export type ImageCommitResult =
    | { ok: true; scene: PrevisScene }
    | { ok: false; reason: "not-approved" | "rejected" | "scene-mismatch" | "scene-drift" | "expired" | "already-committed" };

export type ImageStagingOptions = { scene: PrevisScene; id: string; now: string; ttlMs: number };

function toIsoDate(iso: string): Date {
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) throw new Error("invalid_draft_timestamp");
    return date;
}

/** 从权威场景生成独立 staging 草稿（scene 深拷贝，原场景不受任何后续操作影响）。 */
export function createImageStagingDraft(options: ImageStagingOptions): PrevisImageStagingDraft {
    const { scene, id, now, ttlMs } = options;
    if (!scene?.id || !scene?.updatedAt || !id || !Number.isFinite(ttlMs) || ttlMs <= 0 || toIsoDate(now).getTime() === Number.NaN) throw new Error("invalid_staging_input");
    return {
        id,
        sceneId: scene.id,
        baseUpdatedAt: scene.updatedAt,
        scene: structuredClone(scene),
        status: "staging",
        createdAt: now,
        expiresAt: new Date(toIsoDate(now).getTime() + ttlMs).toISOString(),
    };
}

/** staging → approved（连续性检查通过或用户明确确认警告后调用）。 */
export function approveImageStagingDraft(draft: PrevisImageStagingDraft): PrevisImageStagingDraft {
    if (draft.status !== "staging") return { ...draft };
    return { ...draft, status: "approved" };
}

/** 取消草稿：进入终态 rejected，不再可提交。 */
export function cancelImageTransaction(draft: PrevisImageStagingDraft): PrevisImageStagingDraft {
    if (draft.status === "committed" || draft.status === "rejected") return { ...draft };
    return { ...draft, status: "rejected" };
}

/**
 * 原子提交：草稿必须已批准、sceneId 匹配、CAS 基线未漂移且在有效期内。
 * 提交把自己的状态机推入终态（committed/expired→rejected）——同一草稿
 * 无论重复点击还是网络重试，第二个提交必然得到 already-committed，
 * 不可能产生第二次场景提交。
 */
export function commitImageTransaction(input: { draft: PrevisImageStagingDraft; authoritative: PrevisScene; now: string }): ImageCommitResult {
    const { draft, authoritative, now } = input;
    const nowTime = toIsoDate(now).getTime();

    if (draft.status === "committed") return { ok: false, reason: "already-committed" };
    if (draft.status === "rejected") return { ok: false, reason: "rejected" };
    if (draft.sceneId !== authoritative.id) return { ok: false, reason: "scene-mismatch" };
    if (draft.status !== "approved") return { ok: false, reason: "not-approved" };
    if (draft.baseUpdatedAt !== authoritative.updatedAt) return { ok: false, reason: "scene-drift" };
    if (nowTime > toIsoDate(draft.expiresAt).getTime()) {
        draft.status = "rejected";
        return { ok: false, reason: "expired" };
    }

    draft.status = "committed";
    return { ok: true, scene: { ...structuredClone(draft.scene), updatedAt: now } };
}

/** 过期清理：staging/approved 超期草稿标记 rejected；按 id 去重（首次出现为准）。 */
export function expireImageDrafts(drafts: PrevisImageStagingDraft[], now: string): PrevisImageStagingDraft[] {
    const nowTime = toIsoDate(now).getTime();
    const seen = new Map<string, PrevisImageStagingDraft>();
    for (const draft of drafts) {
        if (!seen.has(draft.id)) seen.set(draft.id, draft);
    }
    const out: PrevisImageStagingDraft[] = [];
    for (const draft of seen.values()) {
        if ((draft.status === "staging" || draft.status === "approved") && nowTime > toIsoDate(draft.expiresAt).getTime()) {
            out.push({ ...draft, status: "rejected" });
        } else {
            out.push(draft);
        }
    }
    return out;
}

/** UI 用：权威场景已漂移（或场景不符）时草稿内容可能是陈旧的，需要提示重新 staging。 */
export function draftIsStale(draft: PrevisImageStagingDraft, authoritative: PrevisScene): boolean {
    return draft.sceneId !== authoritative.id || draft.baseUpdatedAt !== authoritative.updatedAt;
}