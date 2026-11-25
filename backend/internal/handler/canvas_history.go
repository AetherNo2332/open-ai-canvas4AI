package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func RegisterCanvasHistoryRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/canvas-projects/:id/history", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		history, err := svc.CanvasHistory(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, history)
	})

	r.GET("/canvas-projects/:id/history/:snapshotId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		snapshot, err := svc.CanvasHistorySnapshot(user.ID, c.Param("id"), c.Param("snapshotId"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, gin.H{"snapshot": snapshot, "project": json.RawMessage(snapshot.PayloadJSON)})
	})

	r.POST("/canvas-projects/:id/history/:snapshotId/restore", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "canvas-history-restore:"+user.ID, policy.Request.CanvasWritePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2048)
		var req struct {
			Revision *int64 `json:"revision"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, service.BadAuthRequest("历史恢复请求格式错误"))
			return
		}
		if _, err := svc.RestoreCanvasHistory(user.ID, c.Param("id"), c.Param("snapshotId"), req.Revision); err != nil {
			failService(c, err)
			return
		}
		project, err := svc.UserCanvasProject(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		okCanvasProject(c, project)
	})
}
