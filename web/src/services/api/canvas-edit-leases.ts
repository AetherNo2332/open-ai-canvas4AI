import { captureHTTP } from "./request";

export type CanvasEditProof = { canvasId: string; nodeId: string; sessionId: string; token: string };
export type CanvasEditLease = CanvasEditProof & { expiresAt: string };
export type CanvasPublicEditLease = Omit<CanvasEditLease, "token">;
export type CanvasSyncActivity = {
    revision: number;
    documentEpoch: string;
    eventSeq: number;
    leases: CanvasPublicEditLease[];
    activeAgents: Array<{ id: string; conversationId: string; title: string; status: string; updatedAt: string }>;
};

export function createCanvasEditLeaseAPI(api: Pick<ReturnType<typeof captureHTTP>, "get" | "post"> = captureHTTP()) {
    const path = (id: string) => `/canvas-projects/${encodeURIComponent(id)}`;
    const write = async (action: "acquire" | "renew", proof: CanvasEditProof, signal?: AbortSignal) => {
        const { canvasId, ...body } = proof;
        const lease = await api.post<CanvasEditLease>(`${path(canvasId)}/edit-leases/${action}`, body, { signal });
        if (lease.canvasId !== canvasId || lease.nodeId !== proof.nodeId || lease.sessionId !== proof.sessionId || lease.token !== proof.token || !Number.isFinite(Date.parse(lease.expiresAt))) throw new Error("编辑占用回执无效，草稿已保留");
        return lease;
    };
    return {
        acquire: (proof: CanvasEditProof, signal?: AbortSignal) => write("acquire", proof, signal),
        renew: (proof: CanvasEditProof, signal?: AbortSignal) => write("renew", proof, signal),
        release: ({ canvasId, ...body }: CanvasEditProof, signal?: AbortSignal) => api.post<{ released: boolean }>(`${path(canvasId)}/edit-leases/release`, body, { signal }),
        status: (canvasId: string, signal?: AbortSignal) => api.get<CanvasSyncActivity>(`${path(canvasId)}/sync-status`, { signal }),
    };
}
export type CanvasEditLeaseAPI = ReturnType<typeof createCanvasEditLeaseAPI>;
