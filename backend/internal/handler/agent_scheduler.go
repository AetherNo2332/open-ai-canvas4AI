package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
	"net/http"
)

func registerAgentSchedulerAdminRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/settings/agent-scheduler", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		setting, err := svc.AdminAgentSchedulerSetting(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"setting": setting})
	})
	r.PUT("/admin/settings/agent-scheduler", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var input service.AgentSchedulerUpdate
		if err = c.ShouldBindJSON(&input); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		setting, err := svc.UpdateAgentSchedulerSetting(user, input)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"setting": setting})
	})
	r.GET("/admin/settings/agent-scheduler/status", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		rows, err := svc.AdminAgentSchedulerStatus(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"instances": rows})
	})
}
