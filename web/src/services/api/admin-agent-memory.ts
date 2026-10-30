import { compactApiParams, http } from "./request";
import type { AgentMemoryDocument } from "./agent-memory";

/** 管理员只读查看的用户长期记忆文件（GET /admin/agent-memory-documents）。 */
export type AdminAgentMemoryDocument = AgentMemoryDocument & {
    userId: string;
    username?: string;
    displayName?: string;
};

/** 按最近更新排序；userId 为空时列出全部用户，后端单次最多 200 份。 */
export function listAdminAgentMemoryDocuments(params: { userId?: string; limit?: number } = {}, signal?: AbortSignal) {
    const { userId, limit = 50 } = params;
    return http.get<{ documents: AdminAgentMemoryDocument[] }>("/admin/agent-memory-documents", {
        params: compactApiParams({ userId, limit }),
        signal,
        timeout: 15_000,
    });
}
