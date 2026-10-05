package handler

import (
	"net/http"

	"infinite-canvas/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func RegisterAdminAgentSkillDefaultsRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/agent/skill-defaults", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.AdminAgentSkillDefaults(user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, view)
	})
	r.PUT("/admin/agent/skill-defaults", func(c *gin.Context) {
		saveAgentSkillDefaults(c, svc)
	})
}

func saveAgentSkillDefaults(c *gin.Context, svc *service.Service) {
	user, err := currentUser(c, svc)
	if err != nil {
		failService(c, err)
		return
	}
	var req struct {
		Revision int64                           `json:"revision"`
		Items    []service.AgentSkillDefaultItem `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	revision, err := svc.ReplaceAgentSkillDefaults(user, req.Revision, req.Items)
	if err != nil {
		failService(c, err)
		return
	}
	ok(c, gin.H{"revision": revision})
}
