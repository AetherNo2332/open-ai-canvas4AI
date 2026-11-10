package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerEcommerceSkillPromptRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.PATCH("/ecommerce/knowledge/entries/:id/review", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var req struct {
			Status string `json:"status"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if err := svc.ReviewEcommerceKnowledgeEntry(user, c.Param("id"), req.Status); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"status": req.Status})
	})
}
