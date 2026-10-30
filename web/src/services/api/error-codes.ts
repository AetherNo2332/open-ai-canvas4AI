/**
 * 后端机器可读错误原因（`ApiError.reason`）。前端按 reason 分支，不解析 msg。
 * 完整列表与 HTTP 状态见 `docs/content/docs/backend/http-api.mdx` 的“错误码”一节。
 */
export const API_ERROR_REASONS = {
    /** `POST /agent/runs/:id/undo`：本次运行改过的内容之后又被编辑过，撤销未写入。 */
    agentUndoConflict: "agent_undo_conflict",
    /** `POST /agent/runs/:id/director-preview`：运行不属于当前用户，或 requestId 未知、已完成、已超时。 */
    directorPreviewNotFound: "director_preview_not_found",
    /** `POST /agent/runs/:id/permission`：运行已结束，所选权限只用于下一轮。 */
    agentRunFinished: "agent_run_finished",
    /** `POST /agent/runs/:id/permission`：子代理固定为只读，不能切换权限。 */
    agentSubagentPermissionFixed: "agent_subagent_permission_fixed",
} as const;

export type ApiErrorReason = (typeof API_ERROR_REASONS)[keyof typeof API_ERROR_REASONS];
