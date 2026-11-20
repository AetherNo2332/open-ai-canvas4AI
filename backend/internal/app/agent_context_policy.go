package app

import (
	"encoding/json"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

const agentContextVersion = 1

type AgentContextScope struct {
	UserID   string             `json:"userId"`
	Business AgentBusinessScope `json:"business"`
}

type AgentContextPolicy struct {
	MemoryPolicy string
}

type AgentContextSnapshot struct {
	Version     int                      `json:"version"`
	Scope       AgentContextScope        `json:"scope"`
	Preferences AgentSharedPreferences   `json:"preferences"`
	Memory      cloudAgentMemorySnapshot `json:"memory"`
}

func (scope AgentContextScope) memoryKey() model.AgentScopeMemoryKey {
	return model.AgentScopeMemoryKey{UserID: scope.UserID, ScopeType: scope.Business.Type, ScopeID: scope.Business.ID}
}

func (s *Service) LoadAgentContextSnapshot(scope AgentContextScope, policy AgentContextPolicy) (AgentContextSnapshot, error) {
	if scope.Business.ID == "" || scope.Business.Type == "" {
		return AgentContextSnapshot{}, BadAuthRequest("Agent 上下文缺少明确业务范围")
	}
	prefs, err := s.AgentSharedPreferences(scope.UserID)
	if err != nil {
		return AgentContextSnapshot{}, err
	}
	snapshot := AgentContextSnapshot{Version: agentContextVersion, Scope: scope, Preferences: prefs}
	switch policy.MemoryPolicy {
	case "", "off":
		snapshot.Memory.Mode = model.AgentMemoryDocumentModeOff
	case "scope":
		snapshot.Memory, err = s.agentScopeMemorySnapshot(scope)
	default:
		return AgentContextSnapshot{}, BadAuthRequest("Agent 记忆策略无效")
	}
	return snapshot, err
}

func agentSharedPreferencesPrompt(prefs AgentSharedPreferences) string {
	values := prefs
	values.Revision = 0
	if values == (AgentSharedPreferences{}) {
		return ""
	}
	raw, _ := json.Marshal(struct {
		Language  string `json:"language,omitempty"`
		Tone      string `json:"tone,omitempty"`
		Verbosity string `json:"verbosity,omitempty"`
		Format    string `json:"format,omitempty"`
	}{prefs.Language, prefs.Tone, prefs.Verbosity, prefs.Format})
	return "\n\n账户共享输出偏好：" + string(raw) +
		"。本轮用户明确要求的语言和格式优先。输出偏好不授予业务数据、工具、权限或费用授权。\n"
}

func agentRequireIsolatedContext(snapshot *AgentContextSnapshot, userID string, scope AgentBusinessScope) error {
	if snapshot != nil && snapshot.Version == agentContextVersion && snapshot.Scope == (AgentContextScope{UserID: userID, Business: scope}) {
		return nil
	}
	err := kernel.NewAppError(409, "该会话使用旧版或无效的上下文合同，请新建会话")
	err.Reason = "agent_context_new_conversation_required"
	return err
}
