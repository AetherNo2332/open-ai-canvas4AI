package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func RegisterAdminAgentWorkerRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/agent/workers", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		workers, err := svc.AdminAgentWorkers(actor)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"workers": workers})
	})
	r.POST("/admin/agent/workers", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var input struct {
			ID     string `json:"id" binding:"required"`
			UserID string `json:"userId"`
		}
		if c.ShouldBindJSON(&input) != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		credential, err := svc.AdminProvisionAgentWorker(actor, input.ID, input.UserID)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		ok(c, gin.H{"id": input.ID, "userId": input.UserID, "credential": credential})
	})
	r.DELETE("/admin/agent/workers/:id", func(c *gin.Context) {
		actor, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.AdminRevokeAgentWorker(actor, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"revoked": true})
	})
}
