package handler

import (
	"mime"
	"net/http"
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func registerAdminSkillFileRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/skills/:id/files", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		files, err := svc.AdminSkillPackageFiles(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, gin.H{"files": files})
	})

	r.PUT("/admin/skills/:id/files", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.SkillPackageEditMaxBytes)
		var req service.SkillPackageEditRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("技能文件修改数据格式无效或超过大小限制"))
			return
		}
		result, err := svc.AdminEditSkillPackageFiles(user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})

	r.GET("/admin/skills/:id/file", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		file, err := svc.AdminSkillPackageFile(user, c.Param("id"), c.Query("path"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, gin.H{"file": file})
	})

	r.GET("/admin/skills/:id/file/raw", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		data, mimeType, fileName, err := svc.AdminSkillPackageRawFile(user, c.Param("id"), c.Query("path"))
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
		c.Header("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": fileName}))
		c.Data(http.StatusOK, mimeType, data)
	})
}
