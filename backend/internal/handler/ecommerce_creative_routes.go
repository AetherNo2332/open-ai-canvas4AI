package handler

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"yingce/backend/internal/service"
)

func registerEcommerceCreativeRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.PUT("/ecommerce/knowledge/orchestration", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RequireAdmin(user); err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<10)
		var req service.EcommerceOrchestrationSetting
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		setting, err := svc.UpdateEcommerceOrchestrationSetting(user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, setting)
	})
	r.POST("/ecommerce/creative-directions", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
		var req service.EcommerceCreativeDirectionsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		response, err := svc.RecommendEcommerceCreativeDirectionsContext(c.Request.Context(), user.ID, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, response)
	})
}
