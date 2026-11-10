package handler

import (
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func registerAdminArkPortraitAssetSessionRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/admin/ark-portraits/groups/:id/assets", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "ark-portrait-admin-asset:"+user.ID) {
			return
		}
		var req service.ArkAssetUploadInput
		if !bindArkJSON(c, &req) {
			return
		}
		asset, err := svc.AdminCreateArkPortraitAsset(c.Request.Context(), user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"asset": asset})
	})
	r.DELETE("/admin/ark-portraits/assets/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.AdminDeleteArkPortraitAsset(c.Request.Context(), user, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"id": c.Param("id")})
	})
	r.POST("/admin/ark-portraits/sessions", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "ark-portrait-session:"+user.ID) {
			return
		}
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Lang        string `json:"lang"`
		}
		if !bindArkJSON(c, &req) {
			return
		}
		session, err := svc.AdminCreateArkPortraitSession(c.Request.Context(), user, req.Name, req.Description, req.Lang)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"session": session})
	})
}
