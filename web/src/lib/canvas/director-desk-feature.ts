import type { DirectorDeskPrevisMode } from "@/stores/use-user-store";

export type DirectorDeskRuntimePolicy = "legacy" | "canary" | "authoritative";

/**
 * Fail closed for unknown or missing server values. The canary mode keeps the
 * existing workbench available while the DirectorDesk runtime is observed.
 */
/**
 * Canary is a stable ten percent user cohort. Missing identity fails closed so
 * a partially hydrated session never receives the new runtime by accident.
 */
export function directorDeskRuntimePolicy(mode: unknown, userId?: string | null): DirectorDeskRuntimePolicy {
    if (mode === "on") return "authoritative";
    if (mode === "canary" && userId && directorDeskCanaryBucket(userId) < 10) return "canary";
    return "legacy";
}

export function directorDeskCanaryBucket(userId: string): number {
    let hash = 2166136261;
    for (const character of userId) hash = Math.imul(hash ^ character.charCodeAt(0), 16777619) >>> 0;
    return hash % 100;
}

export function directorDeskModeLabel(mode: DirectorDeskPrevisMode): string {
    if (mode === "on") return "全量开放";
    if (mode === "canary") return "灰度开放";
    return "已关闭";
}
