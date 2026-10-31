import { http } from "@/services/api/request";

export type CanvasStoryboardSplitInput = {
    nodeId: string;
    rowIds?: string[];
    request?: string;
    shotCount?: number;
    shotDurationSeconds?: number;
    skillContext?: string;
    logicalModelId?: string;
    config?: Record<string, unknown>;
    quoteOnly?: boolean;
    quoteHash?: string;
    operationId?: string;
    clientId?: string;
};

export type CanvasStoryboardSplitQuote = {
    amountMicrocredits: number;
    model?: string;
    estimated?: boolean;
    expiresAt: string;
    quoteHash: string;
};

export type CanvasStoryboardSplitResult = {
    taskId?: string;
    quote: CanvasStoryboardSplitQuote;
    rowIds?: string[];
    partial: boolean;
};

type SplitRequest = (path: string, body: Record<string, unknown>, signal?: AbortSignal) => Promise<CanvasStoryboardSplitResult>;

const defaultRequest: SplitRequest = (path, body, signal) => http.post<CanvasStoryboardSplitResult>(path, body, signal ? { signal } : undefined);

export async function splitCanvasStoryboard(projectId: string, input: CanvasStoryboardSplitInput, request: SplitRequest = defaultRequest, signal?: AbortSignal) {
    const nodeId = input.nodeId.trim();
    if (!nodeId) throw new Error("缺少分镜节点");
    const body: Record<string, unknown> = { nodeId };
    if (input.rowIds?.length) body.rowIds = input.rowIds;
    if (input.request?.trim()) body.request = input.request.trim();
    if (input.shotCount !== undefined) body.shotCount = input.shotCount;
    if (input.shotDurationSeconds !== undefined) body.shotDurationSeconds = input.shotDurationSeconds;
    if (input.skillContext?.trim()) body.skillContext = input.skillContext.trim();
    if (input.logicalModelId?.trim()) body.logicalModelId = input.logicalModelId.trim();
    if (input.config && Object.keys(input.config).length) body.config = input.config;
    if (input.quoteOnly) body.quoteOnly = true;
    if (input.quoteHash?.trim()) body.quoteHash = input.quoteHash.trim();
    if (input.operationId?.trim()) body.operationId = input.operationId.trim();
    if (input.clientId?.trim()) body.clientId = input.clientId.trim();
    return request(`/canvas-projects/${encodeURIComponent(projectId)}/storyboard/split`, body, signal);
}
