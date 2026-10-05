import { logicalModelIDForConfig, modelOptionName, resolveModelChannel, type AiConfig } from "@/stores/use-config-store";
import type { CrewMemberInput } from "@/services/api/agent-crew";

// Rebase untouched fields onto the refreshed server revision; keep local edits.
export function mergeCrewDraft<T>(base: T, draft: T, remote: T): T {
    if (JSON.stringify(base) === JSON.stringify(draft)) return remote;
    if (base && draft && remote && typeof base === "object" && typeof draft === "object" && typeof remote === "object" && !Array.isArray(base) && !Array.isArray(draft) && !Array.isArray(remote)) {
        const result = { ...remote } as Record<string, unknown>;
        for (const key of Object.keys(draft)) result[key] = mergeCrewDraft((base as Record<string, unknown>)[key], (draft as Record<string, unknown>)[key], (remote as Record<string, unknown>)[key]);
        return result as T;
    }
    return draft;
}
export function createCrewMutationGate() {
    let inFlight = false;
    return {
        async run<T>(action: () => Promise<T>): Promise<T> {
            if (inFlight) throw new Error("Crew 配置正在保存，请等待完成");
            inFlight = true;
            try {
                return await action();
            } finally {
                inFlight = false;
            }
        },
    };
}
export function canReleaseCrewPending(hadAmbiguousRequest: boolean, status: number) {
    return !hadAmbiguousRequest && [400, 401, 403, 404, 422].includes(status);
}
export function crewCommitToRefresh(run: { canvasId: string; approval?: { operationId?: string } } | null, canvasId: string, acknowledged: string | null): string | null {
    const operation = run?.approval?.operationId;
    return run?.canvasId === canvasId && operation && operation !== acknowledged ? operation : null;
}
export function crewModelSelection(config: AiConfig, value: string): CrewMemberInput["modelConfig"] {
    const model = modelOptionName(value);
    const channel = resolveModelChannel(config, value);
    if (channel.scope !== "system" || !channel.models.includes(model)) throw new Error("请选择后端受管文本模型");
    const logicalModelId = logicalModelIDForConfig({ ...config, model: value });
    return logicalModelId ? { model, logicalModelId } : { model, channelId: channel.id, channelModelKey: model };
}
