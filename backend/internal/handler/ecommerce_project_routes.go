package handler

import (
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"yingce/backend/internal/service"
)

func registerEcommerceProjectRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/ecommerce/skills/prompt", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var req struct {
			SkillIDs []string `json:"skillIds"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		prompt, err := svc.EcommerceSkillPrompt(user.ID, req.SkillIDs)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"prompt": prompt})
	})
	r.GET("/ecommerce/projects", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, pageErr := strconv.Atoi(c.DefaultQuery("page", "1"))
		pageSize, sizeErr := strconv.Atoi(c.DefaultQuery("pageSize", "100"))
		if pageErr != nil || sizeErr != nil || page < 1 || page > 100000 || (pageSize != 20 && pageSize != 50 && pageSize != 100) {
			fail(c, http.StatusBadRequest, errors.New("分页参数无效"))
			return
		}
		projects, total, err := svc.EcommerceProjectPage(user.ID, page, pageSize)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"projects": projects, "total": total, "page": page, "pageSize": pageSize})
	})
	r.DELETE("/ecommerce/projects", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 80<<10)
		var req service.DeleteEcommerceProjectsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		deleted, err := svc.DeleteEcommerceProjects(user.ID, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"deleted": deleted})
	})
	r.POST("/ecommerce/projects", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		project, err := svc.CreateEcommerceProject(user.ID)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, project)
	})
	r.GET("/ecommerce/projects/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		project, err := svc.EcommerceProjectView(user.ID, c.Param("id"), c.Query("history") == "active")
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, project)
	})
	r.PUT("/ecommerce/projects/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<20)
		var req service.SaveEcommerceProjectRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				fail(c, http.StatusRequestEntityTooLarge, service.BadAuthRequest("保存内容超过 4 MiB 上限，请减少当前轮图片或参数后重试"))
				return
			}
			fail(c, http.StatusBadRequest, err)
			return
		}
		project, err := svc.SaveEcommerceProject(user.ID, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, project)
	})
}
