import { http, apiBaseURL } from "@/services/api/request";
import { consumeTaskTextStream, createTaskTextStreamParser } from "@/services/api/task-text-stream";

export type CrewRole = "coordinator" | "member";
export type CrewPermission = "read_only" | "propose";
export type CrewBudget = { maxCredits: number; maxSteps: number; maxGenerationTasks: number; maxVideoSeconds: number };
export type CrewMemberInput = {
    name: string;
    role: CrewRole;
    permissionMode: CrewPermission;
    enabled: boolean;
    position: number;
    focusNodeIds: string[];
    modelConfig: { model: string; channelId?: string; channelModelKey?: string; logicalModelId?: string };
    budget: CrewBudget;
};
export type CrewMemberView = CrewMemberInput & { id: string; skills: Array<{ skillId: string; skillVersionId: string; enabled: boolean; position: number; contentHash: string; skillName: string }> };
export type CrewView = { id: string; workspaceId: string; canvasId: string; revision: number; name: string; description: string; status: "enabled" | "disabled"; members: CrewMemberView[] };
export type CrewMemberRunView = {
    id: string;
    memberId: string;
    agentRunId: string;
    role: CrewRole;
    name: string;
    permissionMode: CrewPermission;
    status: "queued" | "running" | "waiting" | "completed" | "failed" | "cancelled";
    attempt: number;
    taskId?: string;
    summary?: string;
    errorCode?: string;
};
export type CrewApprovalView = { approvalId: string; snapshotHash: string; summary: string; preview: Record<string, unknown>; decision?: string; operationId?: string };
export type CrewRunView = {
    id: string;
    crewId: string;
    canvasId: string;
    coordinatorRunId: string;
    status: "queued" | "running" | "waiting_member" | "waiting_approval" | "completed" | "failed" | "cancelled";
    revision: number;
    workspaceHash: string;
    workspaceRevision: number;
    latestSequence: number;
    budget: CrewBudget & { maxConcurrentMembers: number };
    members: CrewMemberRunView[];
    createdAt: string;
    approval?: CrewApprovalView;
};
export type CrewEvent = { type: string; crewRunId: string; memberRunId?: string; sequence: number; payload: Record<string, unknown> };
export type CrewStreamItem = CrewEvent | { type: "crew_snapshot"; snapshot: CrewRunView };
const crewPath = (id: string) => `/agent/crews/${encodeURIComponent(id)}`;
const runPath = (id: string) => `/agent/crew-runs/${encodeURIComponent(id)}`;
export const listCrews = (canvasId: string, signal?: AbortSignal) => http.get<CrewView[]>(`/agent/workspaces/${encodeURIComponent(canvasId)}/crews`, { signal });
export const listCrewRuns = (canvasId: string, signal?: AbortSignal) => http.get<CrewRunView[]>(`/agent/workspaces/${encodeURIComponent(canvasId)}/crew-runs`, { signal });
export const createCrew = (canvasId: string, input: { name: string; description: string; status: string; members: CrewMemberInput[] }) => http.post<CrewView>(`/agent/workspaces/${encodeURIComponent(canvasId)}/crews`, input);
export const createCrewRun = (id: string, input: { prompt: string; skillIds?: string[]; idempotencyKey: string; maxConcurrentMembers?: number }) => http.post<CrewRunView>(`${crewPath(id)}/runs`, input);
export const getCrewRun = (id: string, signal?: AbortSignal) => http.get<CrewRunView>(runPath(id), { signal });
export const decideCrewApproval = (runId: string, approvalId: string, decision: "approve" | "reject", reason = "") => http.post<CrewRunView>(`${runPath(runId)}/approvals/${encodeURIComponent(approvalId)}`, { decision, reason });
export const commitCrewProposal = (runId: string, input: { approvalId: string; expectedSnapshotHash: string; idempotencyKey: string }) => http.post<Record<string, unknown>>(`${runPath(runId)}/commit`, input);
export const cancelCrewRun = (runId: string) => http.post<CrewRunView>(`${runPath(runId)}/cancel`, {});

export function createCrewStreamParser(onItem: (item: CrewStreamItem) => void, onCursor: (sequence: number) => void, expectedRunID?: string) {
    const parser = createTaskTextStreamParser();
    return {
        push(chunk: string, flush = false) {
            consumeTaskTextStream(
                parser,
                chunk,
                (item) => {
                    if (item.event === "error") throw new Error("Crew event stream failed");
                    if (item.event === "crew_event") {
                        const event = item.data as CrewEvent;
                        if (!event || !Number.isSafeInteger(event.sequence) || event.sequence < 1 || typeof event.crewRunId !== "string" || !event.payload || (item.id !== undefined && item.id !== event.sequence))
                            throw new Error("Invalid Crew business event");
                        if (expectedRunID && event.crewRunId !== expectedRunID) return;
                        onCursor(event.sequence);
                        onItem(event);
                    } else if (item.event === "crew_snapshot") {
                        const snapshot = item.data as CrewRunView;
                        if (!snapshot || typeof snapshot.id !== "string" || !Array.isArray(snapshot.members)) throw new Error("Invalid Crew snapshot");
                        if (expectedRunID && snapshot.id !== expectedRunID) return;
                        onItem({ type: "crew_snapshot", snapshot });
                    }
                },
                flush,
            );
        },
    };
}

export function subscribeCrewEvents(
    id: string,
    onItem: (item: CrewStreamItem) => void,
    options: { after?: number; onError?: (error: unknown) => void; onConnectionChange?: (status: "connecting" | "connected" | "reconnecting" | "disconnected") => void } = {},
) {
    const controller = new AbortController();
    let cursor = options.after ?? 0;
    void (async () => {
        let failures = 0;
        while (!controller.signal.aborted) {
            options.onConnectionChange?.(failures ? "reconnecting" : "connecting");
            let timer: ReturnType<typeof setTimeout> | undefined;
            const attempt = new AbortController();
            const abort = () => attempt.abort();
            controller.signal.addEventListener("abort", abort, { once: true });
            const touch = () => {
                clearTimeout(timer);
                timer = setTimeout(abort, 45000);
            };
            touch();
            let terminal = false;
            try {
                const response = await fetch(`${String(apiBaseURL).replace(/\/+$/, "")}${runPath(id)}/events?after=${cursor}`, { credentials: "include", signal: attempt.signal, headers: { Accept: "text/event-stream", "Last-Event-ID": String(cursor) } });
                if (!response.ok) {
                    await response.body?.cancel();
                    if ([401, 403, 404].includes(response.status)) {
                        options.onError?.(new Error(`Crew stream ${response.status}`));
                        return;
                    }
                    throw new Error(`Crew stream ${response.status}`);
                }
                if (!response.body || !response.headers.get("content-type")?.includes("text/event-stream")) throw new Error("Crew SSE response missing");
                options.onConnectionChange?.("connected");
                const parser = createCrewStreamParser(
                    (item) => {
                        if ("snapshot" in item) {
                            if (item.snapshot.id !== id) return;
                            terminal = ["completed", "failed", "cancelled"].includes(item.snapshot.status);
                        } else if (item.crewRunId !== id) return;
                        onItem(item);
                    },
                    (sequence) => {
                        cursor = Math.max(cursor, sequence);
                    },
                    id,
                );
                const reader = response.body.getReader();
                const decoder = new TextDecoder();
                const cancel = () => {
                    void reader.cancel().catch(() => undefined);
                };
                attempt.signal.addEventListener("abort", cancel, { once: true });
                try {
                    while (!attempt.signal.aborted) {
                        const { done, value } = await reader.read();
                        if (value?.length) touch();
                        parser.push(decoder.decode(value, { stream: !done }), done);
                        if (done) break;
                    }
                } finally {
                    attempt.signal.removeEventListener("abort", cancel);
                    await reader.cancel().catch(() => undefined);
                    reader.releaseLock();
                }
                failures = 0;
                if (terminal) return;
            } catch (error) {
                if (controller.signal.aborted) return;
                failures++;
                options.onError?.(error);
            } finally {
                clearTimeout(timer);
                controller.signal.removeEventListener("abort", abort);
            }
            options.onConnectionChange?.("reconnecting");
            await new Promise<void>((resolve) => {
                const finish = () => {
                    clearTimeout(delay);
                    controller.signal.removeEventListener("abort", finish);
                    resolve();
                };
                const delay = setTimeout(finish, Math.min(10000, 500 * 2 ** Math.min(failures, 5)));
                controller.signal.addEventListener("abort", finish, { once: true });
            });
        }
    })().finally(() => options.onConnectionChange?.("disconnected"));
    return () => controller.abort();
}
