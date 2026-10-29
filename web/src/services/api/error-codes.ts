/**
 * 后端机器可读错误原因（`ApiError.reason`）。前端按 reason 分支，不解析 msg。
 * 完整列表与 HTTP 状态见 `docs/content/docs/backend/http-api.mdx` 的“错误码”一节。
 */
export const API_ERROR_REASONS = {
    /** `POST /agent/runs/:id/undo`：本次运行改过的内容之后又被编辑过，撤销未写入。 */
    agentUndoConflict: "agent_undo_conflict",
} as const;

export type ApiErrorReason = (typeof API_ERROR_REASONS)[keyof typeof API_ERROR_REASONS];
