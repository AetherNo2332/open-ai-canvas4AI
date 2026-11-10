package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerDramaVoiceRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.PUT("/dramas/:id/cast/:castId/voice", dramaRoute(svc, 4<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.SetDramaCastVoiceRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.SetDramaCastVoice(userID, c.Param("id"), c.Param("castId"), req, false)
	}))
	r.DELETE("/dramas/:id/cast/:castId/voice", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		req := service.SetDramaCastVoiceRequest{RestyleSegments: c.Query("restyleSegments") == "true"}
		return svc.SetDramaCastVoice(userID, c.Param("id"), c.Param("castId"), req, true)
	}))
	r.GET("/dramas/:id/cast/:castId/impact", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaCastImpact(userID, c.Param("id"), c.Param("castId"))
	}))
	r.POST("/dramas/:id/cast/:castId/regenerate", dramaRoute(svc, 16<<10, func(c *gin.Context, userID string) (any, error) {
		var req struct {
			RestyleSegments bool `json:"restyleSegments"`
		}
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.RegenerateDramaCast(userID, c.Param("id"), c.Param("castId"), req.RestyleSegments)
	}))
}
