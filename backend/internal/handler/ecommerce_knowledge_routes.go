package handler

import (
	"errors"
	"net/http"
	"strconv"
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerEcommerceKnowledgeRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/ecommerce/creative-directions/:id/select", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var req service.EcommerceStyleChoice
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		selection, err := svc.SelectEcommerceCreativeStyle(c.Request.Context(), user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, selection)
	})
	r.GET("/ecommerce/knowledge/stats", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RequireAdmin(user); err != nil {
			failService(c, err)
			return
		}
		stats, err := svc.EcommerceKnowledgeStats()
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, stats)
	})
	r.POST("/ecommerce/knowledge/sync", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		result, err := svc.SyncEcommerceKnowledge(c.Request.Context(), user)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.PATCH("/ecommerce/knowledge/sources/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
			fail(c, http.StatusBadRequest, errors.New("缺少知识源启用状态"))
			return
		}
		if err := svc.SetEcommerceKnowledgeSourceEnabled(user, c.Param("id"), *req.Enabled); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"enabled": *req.Enabled})
	})
	r.GET("/ecommerce/knowledge/entries", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
		limit, limitErr := strconv.Atoi(c.DefaultQuery("limit", "20"))
		if pageErr != nil || limitErr != nil {
			fail(c, http.StatusBadRequest, errors.New("分页参数无效"))
			return
		}
		result, err := svc.EcommerceKnowledgeEntryPage(user, c.Query("keyword"), c.Query("status"), page, limit)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}
