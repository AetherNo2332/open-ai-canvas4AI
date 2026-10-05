package kernel

// AppError 是业务层对外公开的结构化错误。
// Message 必须可安全展示给用户，Cause 仅用于保留内部诊断链路，不得直接写入 HTTP 响应。
type AppError struct {
	Status    int
	Code      int
	Reason    ErrorReason
	Message   string
	Retryable bool
	Cause     error
	Details   map[string]any
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func NewAppError(status int, message string) *AppError {
	return &AppError{Status: status, Code: status, Reason: ReasonForStatus(status), Message: message}
}

func WrapAppError(status int, message string, cause error) *AppError {
	err := NewAppError(status, message)
	err.Cause = cause
	return err
}

func RateLimited(message string) *AppError {
	return &AppError{Status: CodeTooManyRequests, Code: CodeRateLimited, Reason: ReasonRateLimited, Message: message, Retryable: true}
}

func QuotaExceeded(message string) *AppError {
	return &AppError{Status: CodeForbidden, Code: CodeQuotaExceeded, Reason: ReasonQuotaExceeded, Message: message}
}

func BadAuthRequest(message string) *AppError {
	return NewAppError(400, message)
}

func NotFound(message string) *AppError {
	return NewAppError(404, message)
}

func Unauthorized(message string) *AppError {
	return NewAppError(401, message)
}

func Forbidden(message string) *AppError {
	return NewAppError(403, message)
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

func AgentCrewConflict(currentRevision int64) *AppError {
	return &AppError{Status: CodeConflict, Code: CodeConflict, Reason: "agent_crew_revision_conflict", Message: "剧组配置已变化，请重新读取", Details: map[string]any{"currentRevision": currentRevision}}
}

// AgentSkillDefaultsInvalid 表示默认技能集合引用了不存在、已禁用或私有的技能，message 含技能 ID。
func AgentSkillDefaultsInvalid(message string, details map[string]any) *AppError {
	return &AppError{Status: CodeInvalidArgument, Code: CodeInvalidArgument, Reason: ReasonAgentSkillDefaultsInvalid, Message: message, Details: details}
}
