package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerFinanceModelBatchRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/admin/channels/:id/models/batch-reprice", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		var req struct {
			Models []service.ChannelModelRepriceRequest `json:"models" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		updated, err := svc.RepriceAdminChannelModels(user, c.Param("id"), req.Models)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"updated": updated})
	})
	r.POST("/admin/channels/:id/models/batch-protocol", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req struct {
			ModelIDs []string `json:"modelIds" binding:"required"`
			Protocol string   `json:"protocol" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		updated, err := svc.UpdateAdminChannelModelsProtocol(user, c.Param("id"), req.ModelIDs, req.Protocol)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"updated": updated})
	})
	// 跨渠道批量修改协议：所选模型可来自不同系统渠道，整批提交。
	r.POST("/admin/channel-models/batch-protocol", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req struct {
			ModelIDs []string `json:"modelIds" binding:"required"`
			Protocol string   `json:"protocol" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		updated, err := svc.UpdateAdminAllChannelModelsProtocol(user, req.ModelIDs, req.Protocol)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"updated": updated})
	})
	// 跨渠道统一调价：所选模型可来自不同系统渠道，整批提交。
	r.POST("/admin/channel-models/batch-reprice", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		var req struct {
			Models []service.ChannelModelRepriceRequest `json:"models" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		updated, err := svc.RepriceAdminAllChannelModels(user, req.Models)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"updated": updated})
	})
	r.PATCH("/admin/channels/:id/models/:modelId", func(c *gin.Context) {
		saveChannelModel(c, svc, c.Param("modelId"))
	})
}
