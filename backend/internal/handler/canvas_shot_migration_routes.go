package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"yingce/backend/internal/service"
)

// 旧分镜行一次性切换为镜头 submap：预览只读，应用会检查在途任务、留历史前像并按 revision CAS 写入。
func RegisterCanvasShotMigrationRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/canvas-projects/:id/shot-migration", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		preview, err := svc.PreviewCanvasShotMigration(user.ID, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, preview)
	})
	r.POST("/canvas-projects/:id/shot-migration", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "canvas-write:"+user.ID, policy.Request.CanvasWritePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var req service.CanvasShotMigrationRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, service.BadAuthRequest("分镜迁移请求格式错误"))
			return
		}
		result, err := svc.MigrateCanvasShots(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/canvas-projects/:id/shot-migration/rollback", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "canvas-write:"+user.ID, policy.Request.CanvasWritePerMinute, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.CanvasShotMigrationRollbackRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, service.BadAuthRequest("分镜迁移回退请求格式错误"))
			return
		}
		result, err := svc.RollbackCanvasShots(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}
