package handler

import (
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerFinanceModelReadRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/channels/:id/models", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		items, err := svc.AdminChannelModels(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		models := make([]adminChannelModelResponse, 0, len(items))
		for _, item := range items {
			models = append(models, adminChannelModel(item))
		}
		ok(c, gin.H{"models": models})
	})
	// 跨渠道模型列表：用于按模型名称在全部系统渠道中筛选。
	r.GET("/admin/channel-models", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		items, err := svc.AdminAllChannelModels(user)
		if err != nil {
			failService(c, err)
			return
		}
		models := make([]adminChannelModelListResponse, 0, len(items))
		for _, item := range items {
			models = append(models, adminChannelModelListResponse{adminChannelModelResponse: adminChannelModel(item.ChannelModel), ChannelName: item.ChannelName, ChannelEnabled: item.ChannelEnabled})
		}
		ok(c, gin.H{"models": models})
	})
	r.GET("/admin/channel-models/health", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		report, err := svc.AdminChannelModelHealth(user, c.Query("channelId"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, report)
	})
	r.PATCH("/admin/channels/:id/models/:modelId/enabled", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
			fail(c, http.StatusBadRequest, errors.New("请提供 enabled"))
			return
		}
		item, err := svc.SetAdminChannelModelEnabled(user, c.Param("id"), c.Param("modelId"), *req.Enabled)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"model": adminChannelModel(*item)})
	})
}
