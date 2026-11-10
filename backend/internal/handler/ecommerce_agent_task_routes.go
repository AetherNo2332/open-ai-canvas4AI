package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerEcommerceAgentTaskRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/task/:taskId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.EcommerceAgentTask(user.ID, c.Param("taskId"))
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, view)
	})
	r.GET("/agent/projects/:projectId/task", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.LatestEcommerceAgentProjectTask(user.ID, c.Param("projectId"))
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, view)
	})
	r.POST("/task/:taskId/cancel", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		view, err := svc.CancelEcommerceAgent(user.ID, c.Param("taskId"))
		if err != nil {
			ecommerceAgentFailure(c, err)
			return
		}
		ok(c, view)
	})
}
