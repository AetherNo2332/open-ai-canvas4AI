package handler

import (
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func registerDramaLifecycleRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/dramas/:id/start", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.SetDramaRunning(userID, c.Param("id"), true)
	}))
	r.POST("/dramas/:id/pause", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.SetDramaRunning(userID, c.Param("id"), false)
	}))
	r.POST("/dramas/:id/retry-failed", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.RetryFailedDrama(userID, c.Param("id"))
	}))
	r.GET("/dramas/:id/jobs", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		limit, err := parsePositiveQueryInt(c.Query("limit"), 60)
		if err != nil {
			return nil, service.BadAuthRequest("数量无效")
		}
		jobs, err := svc.DramaJobs(userID, c.Param("id"), limit)
		return gin.H{"jobs": jobs}, err
	}))
	r.POST("/dramas/:id/media/release", dramaRoute(svc, 16<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.ReleaseDramaMediaRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.ReleaseDramaMedia(userID, c.Param("id"), req)
	}))
	r.GET("/dramas/:id/jobs/:jobId", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaJobDetail(userID, c.Param("id"), c.Param("jobId"))
	}))
	r.POST("/dramas/:id/cast/regenerate-all", dramaRoute(svc, 16<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.RegenerateDramaCastsRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.RegenerateDramaCasts(userID, c.Param("id"), req)
	}))
}
