package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerDramaCastRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.DELETE("/dramas/:id/shots/:shotId", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return gin.H{"deleted": true}, svc.DeleteDramaShot(userID, c.Param("id"), c.Param("shotId"))
	}))
	r.GET("/dramas/:id/cast", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		casts, err := svc.DramaCasts(userID, c.Param("id"))
		return gin.H{"cast": casts}, err
	}))
	r.PATCH("/dramas/:id/cast/:castId", dramaRoute(svc, 64<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.UpdateDramaCastRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		cast, err := svc.UpdateDramaCast(userID, c.Param("id"), c.Param("castId"), req)
		return gin.H{"cast": cast}, err
	}))
	r.PUT("/dramas/:id/cast/:castId/image", dramaRoute(svc, 4<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.SetDramaCastImageRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.SetDramaCastImage(userID, c.Param("id"), c.Param("castId"), req)
	}))
	r.POST("/dramas/:id/cast/:castId/looks", dramaRoute(svc, 16<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.DramaCastLookRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.AddDramaCastLook(userID, c.Param("id"), c.Param("castId"), req)
	}))
	r.PATCH("/dramas/:id/looks/:lookId", dramaRoute(svc, 16<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.DramaCastLookRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.UpdateDramaCastLook(userID, c.Param("id"), c.Param("lookId"), req)
	}))
	r.DELETE("/dramas/:id/looks/:lookId", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DeleteDramaCastLook(userID, c.Param("id"), c.Param("lookId"), c.Query("restyleSegments") == "true")
	}))
}
