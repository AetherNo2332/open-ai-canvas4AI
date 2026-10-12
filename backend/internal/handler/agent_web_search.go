package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
	"net/http"
)

func registerAgentWebSearchAdminRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/settings/agent-web-search", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		setting, err := svc.AdminAgentWebSearchSetting(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"setting": setting})
	})
	r.PUT("/admin/settings/agent-web-search", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var input service.AgentWebSearchSettingRequest
		if err := c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		setting, err := svc.UpdateAgentWebSearchSetting(user, input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"setting": setting})
	})
}
