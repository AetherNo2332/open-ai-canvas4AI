package handler

import (
	"errors"
	"net/http"
	"strconv"
	"github.com/gin-gonic/gin"
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
		project, err := svc.EcommerceProject(user.ID, c.Param("id"))
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
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
		var req service.SaveEcommerceProjectRequest
		if err := c.ShouldBindJSON(&req); err != nil {
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
