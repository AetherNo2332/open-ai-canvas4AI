package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerAdminSkillCatalogRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/skills", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, pageSize, err := parsePaginationQuery(c, 20)
		if err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		list, err := svc.AdminSkills(user, service.AdminSkillListRequest{
			Page: page, PageSize: pageSize, Keyword: c.Query("keyword"), Status: c.Query("status"), SourceType: c.Query("sourceType"), Tag: c.Query("tag"),
		})
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, list)
	})

	r.GET("/admin/skills/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		skill, err := svc.AdminSkillDetail(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, gin.H{"skill": skill})
	})

	r.PUT("/admin/skills/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.AdminSkillInfoRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("技能信息格式无效"))
			return
		}
		skill, err := svc.AdminUpdateSkillInfo(user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"skill": skill})
	})

	r.PATCH("/admin/skills/:id/status", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
			failService(c, service.BadAuthRequest("请指定 enabled"))
			return
		}
		skill, err := svc.AdminSetSkillEnabled(user, c.Param("id"), *req.Enabled)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"skill": skill})
	})

	r.DELETE("/admin/skills/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.AdminDeleteSkill(user, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": true})
	})
}
