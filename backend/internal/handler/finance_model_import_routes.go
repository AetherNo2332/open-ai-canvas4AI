package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
	"yingce/backend/internal/service"
)

func registerFinanceModelImportRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/admin/channels/:id/models/fetch", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceRateLimit(c, "admin-channel-models-fetch:"+user.ID+":"+c.Param("id"), 10, time.Minute) {
			return
		}
		// 上游密钥只在 service 内使用；点击拉取时仅返回目录，确认后才写入渠道模型。
		models, err := svc.PreviewAdminChannelModels(c.Request.Context(), user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"models": models})
	})
	r.POST("/admin/channels/:id/models/import", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceRateLimit(c, "admin-channel-models-import:"+user.ID+":"+c.Param("id"), 10, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.AdminChannelModelImportRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.ImportAdminChannelModels(c.Request.Context(), user, c.Param("id"), req.Models)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/admin/channels/:id/models/test", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceRateLimit(c, "admin-channel-model-test:"+user.ID+":"+c.Param("id"), 5, time.Minute) {
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.ChannelModelRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		result, err := svc.TestAdminChannelModel(c.Request.Context(), user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/admin/channels/:id/models", func(c *gin.Context) {
		saveChannelModel(c, svc, "")
	})
	r.POST("/admin/channels/:id/models/batch-delete", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req struct {
			ModelIDs []string `json:"modelIds"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		deleted, err := svc.DeleteAdminChannelModels(user, c.Param("id"), req.ModelIDs)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": deleted})
	})
}
