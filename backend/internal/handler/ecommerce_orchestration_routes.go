package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerEcommerceOrchestrationRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/ecommerce/agent-setting", func(c *gin.Context) {
		if _, err := currentUser(c, svc); err != nil {
			failService(c, err)
			return
		}
		setting, err := svc.EcommerceAgentSetting()
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, setting)
	})
	r.GET("/ecommerce/knowledge/orchestration", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		setting, err := svc.EcommerceOrchestrationSetting(user)
		if err != nil {
			failService(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		ok(c, setting)
	})
}
