package handler

import (
	"net/http"
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerEcommerceSkillInstallRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/admin/ecommerce/skills/install", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RequireAdmin(user); err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (1<<30)+(1<<20))
		defer func() {
			if c.Request.MultipartForm != nil {
				_ = c.Request.MultipartForm.RemoveAll()
			}
		}()
		file, err := c.FormFile("file")
		if err != nil {
			failService(c, service.BadAuthRequest("请选择技能文件，文件太大时请缩小至 1GB 以内"))
			return
		}
		if err := svc.InstallEcommerceSkillUpload(user, file); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"installed": true})
	})
	r.POST("/admin/ecommerce/skills/install/github", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RequireAdmin(user); err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
		var req struct {
			URL    string `json:"url"`
			Subdir string `json:"subdir"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("GitHub 安装参数无效"))
			return
		}
		if err := svc.InstallEcommerceSkillGitHub(c.Request.Context(), user, req.URL, req.Subdir); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"installed": true})
	})
	r.PATCH("/admin/ecommerce/skills/:id/name", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
		var req struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			failService(c, service.BadAuthRequest("名称无效"))
			return
		}
		if err := svc.RenameEcommerceSkill(user, c.Param("id"), req.Name); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"name": req.Name})
	})
}
