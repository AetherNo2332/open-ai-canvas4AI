import type { DirectorDeskPrevisMode } from "@/stores/use-user-store";

export type DirectorDeskRuntimePolicy = "legacy" | "canary" | "authoritative";

/**
 * Fail closed for unknown or missing server values. The canary mode keeps the
 * existing workbench available while the DirectorDesk runtime is observed.
 */
export function directorDeskRuntimePolicy(mode: unknown): DirectorDeskRuntimePolicy {
    if (mode === "on") return "authoritative";
    if (mode === "canary") return "canary";
    return "legacy";
}

export function directorDeskModeLabel(mode: DirectorDeskPrevisMode): string {
    if (mode === "on") return "全量开放";
    if (mode === "canary") return "灰度开放";
    return "已关闭";
}
