package kernel

// Cloud Agent 专用的机器可读原因。Agent 运行时（Worker 租约、技能预算、默认技能与
// Workspace 的 revision CAS）独立于通用错误码维护，前端按 reason 判断。
const (
	ReasonAgentLeaseLost                     ErrorReason = "agent_lease_lost"
	ReasonAgentSkillBudgetExceeded           ErrorReason = "agent_skill_budget_exceeded"
	ReasonAgentSkillDefaultsRevisionConflict ErrorReason = "agent_skill_defaults_revision_conflict"
	ReasonAgentSkillDefaultsInvalid          ErrorReason = "agent_skill_defaults_invalid"
	ReasonAgentWorkspaceRevisionConflict     ErrorReason = "agent_workspace_revision_conflict"
	ReasonAgentUndoConflict                  ErrorReason = "agent_undo_conflict"
	// 运行中切换执行权限：运行已结束 / 子代理权限固定为只读。
	ReasonAgentRunFinished             ErrorReason = "agent_run_finished"
	ReasonAgentSubagentPermissionFixed ErrorReason = "agent_subagent_permission_fixed"
	// ReasonAgentApprovalNotPermitted：运行中收紧权限后，待批准的写入已不被当前权限允许。
	ReasonAgentApprovalNotPermitted ErrorReason = "agent_approval_not_permitted"
)

// AgentUndoConflict 表示 Agent 本轮改过的内容之后又被编辑过，撤销未写入；details 列出冲突的实体。
func AgentUndoConflict(message string, details map[string]any) *AppError {
	return &AppError{Status: CodeConflict, Code: CodeConflict, Reason: ReasonAgentUndoConflict, Message: message, Details: details}
}

func AgentLeaseLost(message string) *AppError {
	return &AppError{Status: CodeForbidden, Code: CodeForbidden, Reason: ReasonAgentLeaseLost, Message: message}
}

// AgentSkillBudgetExceeded 表示技能集合的文件数/字节数/上下文预算超限，details 携带预算名、上限与实际值。
func AgentSkillBudgetExceeded(details map[string]any) *AppError {
	return &AppError{Status: CodeInvalidArgument, Code: CodeInvalidArgument, Reason: ReasonAgentSkillBudgetExceeded, Message: "技能集合超出上下文预算", Details: details}
}

// AgentSkillDefaultsConflict 表示默认技能保存的 revision CAS 失败，details 携带服务端当前 revision。
func AgentSkillDefaultsConflict(currentRevision int64) *AppError {
	return &AppError{Status: CodeConflict, Code: CodeConflict, Reason: ReasonAgentSkillDefaultsRevisionConflict, Message: "默认技能配置已被其他管理员修改，请刷新后重试", Details: map[string]any{"currentRevision": currentRevision}}
}

func AgentWorkspaceConflict(currentRevision int64) *AppError {
	return &AppError{Status: CodeConflict, Code: CodeConflict, Reason: ReasonAgentWorkspaceRevisionConflict, Message: "Workspace 已被其他页面修改，请刷新后重试", Details: map[string]any{"currentRevision": currentRevision}}
}

// AgentSkillDefaultsInvalid 表示默认技能集合引用了不存在、已禁用或私有的技能，message 含技能 ID。
func AgentSkillDefaultsInvalid(message string, details map[string]any) *AppError {
	return &AppError{Status: CodeInvalidArgument, Code: CodeInvalidArgument, Reason: ReasonAgentSkillDefaultsInvalid, Message: message, Details: details}
}
