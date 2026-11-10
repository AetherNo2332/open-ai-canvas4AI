package handler

import (
	"net/http"
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func registerFinanceModelMutationRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.PATCH("/admin/channels/:id/models/:modelId/sort", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.ChannelModelSortRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			fail(c, http.StatusBadRequest, err)
			return
		}
		if err := svc.UpdateAdminChannelModelSort(user, c.Param("id"), c.Param("modelId"), req); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"updated": true})
	})
	r.DELETE("/admin/channels/:id/models/:modelId", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.DeleteAdminChannelModel(user, c.Param("id"), c.Param("modelId")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"ok": true})
	})
}
