package app

import (
	"strings"

	"yingce/backend/internal/kernel"
)

const (
	AgentPlatformBusinessType = "platform"
	AgentPlatformSelfScopeID  = "self"
)

// NormalizeAgentBusinessScope converts the public platform/self alias into the
// authenticated account's durable identity. Other business scopes pass through
// unchanged so canvas and drama contracts stay wire-compatible.
func NormalizeAgentBusinessScope(userID string, scope AgentBusinessScope) (AgentBusinessScope, error) {
	if scope.Type != AgentPlatformBusinessType {
		return scope, nil
	}
	if strings.TrimSpace(userID) == "" {
		return AgentBusinessScope{}, kernel.Unauthorized("请先登录")
	}
	switch scope.ID {
	case AgentPlatformSelfScopeID:
		return AgentBusinessScope{Type: AgentPlatformBusinessType, ID: userID}, nil
	case userID:
		return scope, nil
	default:
		err := kernel.BadAuthRequest("平台会话只能使用当前登录账号")
		err.Reason = "agent_scope_invalid"
		return AgentBusinessScope{}, err
	}
}

func normalizeAgentRequestScope(userID string, req *CloudAgentRequest) error {
	if req == nil || req.Scope == nil {
		return nil
	}
	scope, err := NormalizeAgentBusinessScope(userID, *req.Scope)
	if err != nil {
		return err
	}
	req.Scope = &scope
	return nil
}

// PublicAgentBusinessScope projects durable account identity back to the stable
// platform/self wire value without exposing user IDs.
func PublicAgentBusinessScope(scope AgentBusinessScope) AgentBusinessScope {
	if scope.Type == AgentPlatformBusinessType {
		return AgentBusinessScope{Type: AgentPlatformBusinessType, ID: AgentPlatformSelfScopeID}
	}
	return scope
}

func (s *Service) NormalizeAgentRequestScope(userID string, req *CloudAgentRequest) error {
	return normalizeAgentRequestScope(userID, req)
}

func (s *Service) NormalizeAgentConversationSelector(userID string, selector AgentConversationSelector) (AgentConversationSelector, error) {
	scope, err := NormalizeAgentBusinessScope(userID, selector.Scope)
	if err != nil {
		return AgentConversationSelector{}, err
	}
	selector.Scope = scope
	return selector, nil
}
