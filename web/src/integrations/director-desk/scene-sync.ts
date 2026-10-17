import { DIRECTOR_CANVAS_EXTENSION_KEY, DIRECTOR_PLACEHOLDER_CAMERA_ID, type DirectorProject } from "./previs-runtime-types";

/** 场景身份标记：id + updatedAt（画布项目的修订标记）。 */
export type RuntimeSyncKey = { id: string; updatedAt: string };

/**
 * 外部/Agent 场景更新对 runtime 的刷新决策：
 * - session：首次同步或场景 id 变化 → 重置视图状态（选择、播放头、当前镜头）
 * - content：同一场景修订变化 → 重建文档并保留/回退视图状态
 * - noop：同一场景同修订 → 不重建，避免对象身份抖动造成无谓 rebuild
 */
export type RuntimeSyncDecision = { kind: "session" } | { kind: "content" } | { kind: "noop" };

export function decideRuntimeSync(previous: RuntimeSyncKey | null, next: RuntimeSyncKey | null): RuntimeSyncDecision {
    if (!previous || !next) return { kind: "session" };
    if (previous.id !== next.id) return { kind: "session" };
    if (previous.updatedAt === next.updatedAt) return { kind: "noop" };
    return { kind: "content" };
}

/** runtime 视图状态：选择、当前镜头、播放头。 */
export type RuntimeViewState = {
    selectedEntityId: string;
    activeShotId: string;
    time: number;
};

function firstShotId(document: DirectorProject): string {
    return document.extensions?.[DIRECTOR_CANVAS_EXTENSION_KEY]?.shots[0]?.id ?? "";
}

function liveShotId(document: DirectorProject, shotId: string): string {
    return shotId && document.extensions?.[DIRECTOR_CANVAS_EXTENSION_KEY]?.shots.some((shot) => shot.id === shotId) ? shotId : "";
}

/** 新会话的初始视图状态：无选择、播放头 0、当前镜头取文档 activeShot（存在时）否则第一个。 */
export function initialRuntimeViewState(document: DirectorProject): RuntimeViewState {
    const active = liveShotId(document, document.extensions?.[DIRECTOR_CANVAS_EXTENSION_KEY]?.scene.activeShotId ?? "");
    return { selectedEntityId: "", activeShotId: active || firstShotId(document), time: 0 };
}

/**
 * 文档刷新后按新文档解析视图状态：仍然存在的引用保留，
 * 被删除的引用确定性地回退（第一个实体/镜头、时长边界），绝不悬空。
 */
export function resolveRuntimeViewState(document: DirectorProject, state: RuntimeViewState): RuntimeViewState {
    // 占位机位不是影策场景实体,不能成为选择目标。
    const entities = document.entities.filter((entity) => entity.id !== DIRECTOR_PLACEHOLDER_CAMERA_ID);
    const selectedEntityId = entities.some((entity) => entity.id === state.selectedEntityId) ? state.selectedEntityId : (entities[0]?.id ?? "");
    const activeShotId = liveShotId(document, state.activeShotId) || firstShotId(document);
    const duration = Number.isFinite(document.duration) ? Math.max(0, document.duration) : 0;
    const time = Math.min(Math.max(0, state.time), duration);
    return { selectedEntityId, activeShotId, time };
}
