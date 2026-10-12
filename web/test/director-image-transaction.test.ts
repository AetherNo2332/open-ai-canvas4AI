import { describe, expect, test } from "bun:test";
import {
    createImageStagingDraft,
    approveImageStagingDraft,
    commitImageTransaction,
    cancelImageTransaction,
    expireImageDrafts,
    type PrevisImageStagingDraft,
} from "../src/lib/canvas/previs/previs-image-transaction";
import type { PrevisScene } from "../src/types/previs";

const makeScene = (overrides: Partial<PrevisScene> = {}): PrevisScene => ({
    id: "scene-1",
    version: 1,
    title: "图片流程场景",
    background: "#222222",
    environmentIntensity: 1,
    gridVisible: true,
    objects: [],
    cameras: [
        { id: "cam-1", name: "主机位", transform: { position: [0, 1.6, 5], rotation: [0, 0, 0], scale: [1, 1, 1] }, target: [0, 1, 0], focalLength: 35, fov: 50, aperture: 2.8, focusDistance: 5, near: 0.05, far: 500, keyframes: [] },
    ],
    lights: [],
    shots: [{ id: "shot-1", name: "镜头 1", cameraId: "cam-1", duration: 4, fps: 24, shotSize: "medium", cameraMove: "static", prompt: "" }],
    activeShotId: "shot-1",
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T01:00:00Z",
    ...overrides,
});

const now = "2026-01-02T00:00:00Z";
const draftId = "draft-1";

describe("图片预演事务 — staging draft 与原子提交", () => {
    test("01 staging 生成独立草稿：基线 CAS、过期时间、不改动原场景", () => {
        const base = makeScene();
        const baseBefore = structuredClone(base);

        const draft = createImageStagingDraft({ scene: base, id: draftId, now, ttlMs: 15 * 60_000 });

        expect(draft.id).toBe(draftId);
        expect(draft.sceneId).toBe(base.id);
        expect(draft.baseUpdatedAt).toBe(base.updatedAt);
        expect(draft.status).toBe("staging");
        expect(draft.expiresAt).toBe("2026-01-02T00:15:00.000Z");
        // 草稿的 scene 是独立副本
        expect(draft.scene.objects).not.toBe(base.objects);
        // 原场景完全不变
        expect(base).toEqual(baseBefore);
    });

    test("02 未批准的 staging 草稿不能提交（图片流程必须先过连续性检查/用户确认）", () => {
        const draft = createImageStagingDraft({ scene: makeScene(), id: draftId, now, ttlMs: 60_000 });
        const result = commitImageTransaction({ draft, authoritative: makeScene(), now });
        expect(result.ok).toBe(false);
        if (!result.ok) expect(result.reason).toBe("not-approved");
        // 草稿状态不变，可后续批准
        expect(draft.status).toBe("staging");
    });

    test("03 批准后 CAS 提交成功：场景带 updatedAt 时间戳；重复提交同草稿被拒绝（幂等，重复点击不产生第二次提交）", () => {
        const base = makeScene();
        const draft = approveImageStagingDraft(
            { ...createImageStagingDraft({ scene: base, id: draftId, now, ttlMs: 60_000 }), scene: { ...base, title: "图片生成的标题" } },
        );
        expect(draft.status).toBe("approved");

        const first = commitImageTransaction({ draft, authoritative: base, now });
        expect(first.ok).toBe(true);
        if (first.ok) {
            expect(first.scene.title).toBe("图片生成的标题");
            expect(first.scene.id).toBe(base.id);
            expect(first.scene.updatedAt).toBe(now);
            expect(first.scene.baseUpdatedAt).toBeUndefined();
        }

        const second = commitImageTransaction({ draft, authoritative: base, now });
        expect(second.ok).toBe(false);
        if (!second.ok) expect(second.reason).toBe("already-committed");
    });

    test("04 权威场景漂移（其他写者已保存）时提交被 CAS 拒绝：reason=scene-drift，草稿保持可批准态，原场景不变", () => {
        const base = makeScene();
        const drifted: PrevisScene = { ...base, updatedAt: "2026-01-01T02:00:00Z" };
        const draft = approveImageStagingDraft(
            { ...createImageStagingDraft({ scene: base, id: draftId, now, ttlMs: 60_000 }), scene: { ...base, title: "草稿标题" } },
        );

        const result = commitImageTransaction({ draft, authoritative: drifted, now });
        expect(result.ok).toBe(false);
        if (!result.ok) expect(result.reason).toBe("scene-drift");
        expect(draft.status).toBe("approved");
        expect(drifted.title).toBe(base.title);
    });

    test("05 超时提交被拒绝：reason=expired，草稿被标记 rejected 且不可再提交", () => {
        const base = makeScene();
        const draft = approveImageStagingDraft(
            { ...createImageStagingDraft({ scene: base, id: draftId, now, ttlMs: 60_000 }), scene: { ...base } },
        );
        const later = "2026-01-02T00:20:00Z";

        const result = commitImageTransaction({ draft, authoritative: base, now: later });
        expect(result.ok).toBe(false);
        if (!result.ok) expect(result.reason).toBe("expired");
        expect(draft.status).toBe("rejected");

        const again = commitImageTransaction({ draft, authoritative: base, now });
        expect(again.ok).toBe(false);
        if (!again.ok) expect(again.reason).toBe("rejected");
    });

    test("06 取消后草稿不可提交；cancelled 草稿不污染原场景", () => {
        const base = makeScene();
        let draft = createImageStagingDraft({ scene: base, id: draftId, now, ttlMs: 60_000 });
        draft = approveImageStagingDraft(draft);
        draft = cancelImageTransaction(draft);
        expect(draft.status).toBe("rejected");

        const result = commitImageTransaction({ draft, authoritative: base, now });
        expect(result.ok).toBe(false);
        if (!result.ok) expect(result.reason).toBe("rejected");
        expect(base.objects).toHaveLength(0);
    });

    test("07 过期清理：staging/approved 草稿按 expiresAt 批量标记 rejected，fresh 草稿保留", () => {
        const base = makeScene();
        const expiredStaging = createImageStagingDraft({ scene: base, id: "d-exp-1", now, ttlMs: 60_000 });
        const expiredApproved = approveImageStagingDraft(createImageStagingDraft({ scene: base, id: "d-exp-2", now, ttlMs: 60_000 }));
        const fresh = createImageStagingDraft({ scene: base, id: "d-fresh", now, ttlMs: 3600_000 });

        const cleaned = expireImageDrafts([expiredStaging, expiredApproved, fresh, fresh], "2026-01-02T00:20:00Z");
        expect(cleaned.map((d) => d.status)).toEqual(["rejected", "rejected", "staging"]);
        // 幂等：已 rejected 的再次清理不变
        expect(expireImageDrafts(cleaned, "2026-01-02T00:20:00Z")).toHaveLength(3);
    });

    test("08 草稿 scene 漂移检测：staging 后权威场景变化，UI 可用 isDraftStale 提示用户", () => {
        const base = makeScene();
        const draft = createImageStagingDraft({ scene: base, id: draftId, now, ttlMs: 60_000 });
        expect(draftIsStale(draft, base)).toBe(false);
        const drifted: PrevisScene = { ...base, updatedAt: "2026-01-01T03:00:00Z" };
        expect(draftIsStale(draft, drifted)).toBe(true);
        const other: PrevisScene = { ...base, id: "scene-2" };
        expect(draftIsStale(draft, other)).toBe(true);
    });

    test("09 sceneId 不匹配的草稿不允许提交", () => {
        const base = makeScene();
        const draft = approveImageStagingDraft(
            { ...createImageStagingDraft({ scene: base, id: draftId, now, ttlMs: 60_000 }), scene: { ...base } },
        );
        const result = commitImageTransaction({ draft, authoritative: makeScene({ id: "scene-other" }), now });
        expect(result.ok).toBe(false);
        if (!result.ok) expect(result.reason).toBe("scene-mismatch");
    });
});

import { draftIsStale } from "../src/lib/canvas/previs/previs-image-transaction";
// 显式类型检查
const _typeCheck: PrevisImageStagingDraft | null = null;
void _typeCheck;