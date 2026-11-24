package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/service"
)

func agentTargetScope(c *gin.Context) (service.AgentConversationSelector, bool) {
	query := c.Request.URL.Query()
	scope := service.AgentBusinessScope{Type: query.Get("scopeType"), ID: query.Get("scopeId")}
	provided := query.Has("scopeType") || query.Has("scopeId") || query.Has("agentDefinitionId")
	return service.AgentConversationSelector{Scope: scope, AgentDefinitionID: query.Get("agentDefinitionId")}, provided
}

func normalizedAgentTargetScope(c *gin.Context, svc *service.Service, userID string) (service.AgentConversationSelector, bool, error) {
	selector, provided := agentTargetScope(c)
	if !provided {
		return selector, false, nil
	}
	selector, err := svc.NormalizeAgentConversationSelector(userID, selector)
	return selector, true, err
}

func normalizeAgentRequestScopeFromQuery(c *gin.Context, svc *service.Service, userID string, req *service.CloudAgentRequest) error {
	if err := svc.NormalizeAgentRequestScope(userID, req); err != nil {
		return err
	}
	selector, provided, err := normalizedAgentTargetScope(c, svc, userID)
	if err != nil || !provided {
		return err
	}
	if selector.Scope.Type != "" || selector.Scope.ID != "" {
		if req.Scope != nil && *req.Scope != selector.Scope {
			scopeErr := kernel.BadAuthRequest("请求体与查询参数的业务范围不一致")
			scopeErr.Reason = "agent_scope_invalid"
			return scopeErr
		}
		scope := selector.Scope
		req.Scope = &scope
	}
	if selector.AgentDefinitionID != "" {
		if req.AgentDefinitionID != "" && req.AgentDefinitionID != selector.AgentDefinitionID {
			scopeErr := kernel.BadAuthRequest("请求体与查询参数的 Agent 配置不一致")
			scopeErr.Reason = "agent_scope_invalid"
			return scopeErr
		}
		req.AgentDefinitionID = selector.AgentDefinitionID
	}
	return nil
}

func agentTargetScopeGuard(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Param("id") == "" {
			c.Next()
			return
		}
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		selector, provided, scopeErr := normalizedAgentTargetScope(c, svc, user.ID)
		if scopeErr == nil && provided {
			err = svc.RequireAgentTargetScope(user.ID, c.Param("id"), strings.Contains(c.FullPath(), "/agent/conversations/"), selector)
		} else if scopeErr != nil {
			err = scopeErr
		}
		if err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		c.Next()
	}
}
