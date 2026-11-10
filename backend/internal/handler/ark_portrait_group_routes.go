package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerArkPortraitGroupRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/ark-portraits/groups", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if !enforceArkPortraitRate(c, svc, "ark-portrait-group:"+user.ID) {
			return
		}
		var req struct {
			Kind        string `json:"kind"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if !bindArkJSON(c, &req) {
			return
		}
		if req.Kind != "virtual" {
			failService(c, service.BadAuthRequest("真人素材组需要通过 H5 认证创建，版权 IP 由管理员分配"))
			return
		}
		group, err := svc.CreateArkVirtualGroup(c.Request.Context(), user, service.ArkGroupInput{Name: req.Name, Description: req.Description})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"group": group})
	})
	r.PATCH("/ark-portraits/groups/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.ArkGroupInput
		if !bindArkJSON(c, &req) {
			return
		}
		group, err := svc.UpdateArkGroup(user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"group": group})
	})
	r.DELETE("/ark-portraits/groups/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.DeleteArkGroup(c.Request.Context(), user, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"id": c.Param("id")})
	})
}
