package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
	"yingce/backend/internal/service"
)

func registerEcommerceTaskSettingRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.PATCH("/admin/ecommerce/skills/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RequireAdmin(user); err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if req.Enabled == nil {
			fail(c, http.StatusBadRequest, service.BadAuthRequest("请指定 enabled"))
			return
		}
		if err := svc.SetEcommerceManagedSkillEnabled(user, c.Param("id"), *req.Enabled); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"enabled": *req.Enabled})
	})
	r.POST("/ecommerce/projects/:id/tasks", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "tasks:"+user.ID, policy.Request.TaskCreatePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		var req service.CreateTaskRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		req.TraceID, req.RequestID = TraceID(c), RequestID(c)
		task, err := svc.CreateEcommercePlanningTask(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, task)
	})
}
