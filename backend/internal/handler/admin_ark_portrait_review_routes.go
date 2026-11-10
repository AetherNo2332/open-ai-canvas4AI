package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerAdminArkPortraitReviewRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/ark-portraits/reviews", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		page, pageSize, err := parsePaginationQuery(c, 20)
		if err != nil {
			failService(c, err)
			return
		}
		result, err := svc.AdminArkReviewQueue(user, c.Query("status"), page, pageSize)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/admin/ark-portraits/reviews/approve", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "ark-portrait-review:"+user.ID) {
			return
		}
		var req struct {
			IDs []string `json:"ids"`
		}
		if !bindArkJSON(c, &req) {
			return
		}
		result, err := svc.AdminApproveArkAssets(c.Request.Context(), user, req.IDs)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/admin/ark-portraits/reviews/reject", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req struct {
			IDs    []string `json:"ids"`
			Reason string   `json:"reason"`
		}
		if !bindArkJSON(c, &req) {
			return
		}
		result, err := svc.AdminRejectArkAssets(user, req.IDs, req.Reason)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
}
