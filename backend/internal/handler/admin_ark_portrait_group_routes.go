package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerAdminArkPortraitGroupRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/admin/ark-portraits/groups", func(c *gin.Context) {
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
		result, err := svc.AdminArkGroups(user, service.AdminArkGroupQuery{
			Kind: c.Query("kind"), Scope: c.Query("scope"), Expiry: c.Query("expiry"), Keyword: c.Query("keyword"), Page: page, PageSize: pageSize,
		})
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, result)
	})
	r.POST("/admin/ark-portraits/groups", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.AdminArkGroupCreateRequest
		if !bindArkJSON(c, &req) {
			return
		}
		group, err := svc.AdminCreateArkGroup(c.Request.Context(), user, req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"group": group})
	})
	r.GET("/admin/ark-portraits/groups/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		group, err := svc.AdminArkGroup(user, c.Param("id"))
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"group": group})
	})
	r.PATCH("/admin/ark-portraits/groups/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		var req service.AdminArkGroupUpdateRequest
		if !bindArkJSON(c, &req) {
			return
		}
		group, err := svc.AdminUpdateArkGroup(user, c.Param("id"), req)
		if err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"group": group})
	})
	r.DELETE("/admin/ark-portraits/groups/:id", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.AdminDeleteArkGroup(c.Request.Context(), user, c.Param("id")); err != nil {
			failService(c, err)
			return
		}
		ok(c, gin.H{"id": c.Param("id")})
	})
}
