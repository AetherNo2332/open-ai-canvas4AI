package handler

import (
	"github.com/gin-gonic/gin"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"yingce/backend/internal/service"
)

func registerEcommerceSkillCatalogRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.Match([]string{http.MethodGet, http.MethodPost}, "/ecommerce/download", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		ids := c.QueryArray("resourceId")
		if c.Request.Method == http.MethodPost {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
			ids = c.PostFormArray("resourceId")
		}
		files, err := svc.EcommerceDownloadResources(user.ID, ids)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Content-Type", "application/zip")
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "电商图片.zip"}))
		c.Header("Cache-Control", "private, no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)
		c.Writer.Flush()
		if err := svc.WriteEcommerceDownload(c.Request.Context(), user.ID, files, c.Writer); err != nil {
			slog.Warn("ecommerce download interrupted", "error", err)
		}
	})
	r.GET("/admin/ecommerce/categories", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
		data, err := svc.EcommerceIndustryCategories(user, c.Query("parentId"), c.Query("q"), page, limit)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, data)
	})
	r.GET("/admin/ecommerce/skills", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		items, err := svc.EcommerceManagedSkills(user)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, gin.H{"skills": items})
	})
}
