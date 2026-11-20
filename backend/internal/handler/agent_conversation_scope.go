package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func agentTargetScope(c *gin.Context) (service.AgentConversationSelector, bool) {
	query := c.Request.URL.Query()
	scope := service.AgentBusinessScope{Type: query.Get("scopeType"), ID: query.Get("scopeId")}
	provided := query.Has("scopeType") || query.Has("scopeId") || query.Has("agentDefinitionId")
	return service.AgentConversationSelector{Scope: scope, AgentDefinitionID: query.Get("agentDefinitionId")}, provided
}

func agentTargetScopeGuard(svc *service.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		selector, provided := agentTargetScope(c)
		if !provided || c.Param("id") == "" {
			c.Next()
			return
		}
		user, err := currentUser(c, svc)
		if err == nil {
			err = svc.RequireAgentTargetScope(user.ID, c.Param("id"), strings.Contains(c.FullPath(), "/agent/conversations/"), selector)
		}
		if err != nil {
			failService(c, err)
			c.Abort()
			return
		}
		c.Next()
	}
}
