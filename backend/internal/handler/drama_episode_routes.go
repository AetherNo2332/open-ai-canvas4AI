package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerDramaEpisodeRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/dramas/:id/episodes", dramaRoute(svc, 1<<20, func(c *gin.Context, userID string) (any, error) {
		var req service.AddDramaEpisodeRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.AddDramaEpisode(userID, c.Param("id"), req)
	}))
	r.GET("/dramas/:id/episodes/:episodeId", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaEpisodeDetail(userID, c.Param("id"), c.Param("episodeId"))
	}))
	// confirm 是用户输入的确认文字；本集生成过视频时必须是“第 N 集”。
	r.DELETE("/dramas/:id/episodes/:episodeId", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return gin.H{"deleted": true}, svc.DeleteDramaEpisode(userID, c.Param("id"), c.Param("episodeId"), c.Query("confirm"))
	}))
	r.PATCH("/dramas/:id/episodes/:episodeId", dramaRoute(svc, 16<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.UpdateDramaEpisodeRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		episode, err := svc.UpdateDramaEpisode(userID, c.Param("id"), c.Param("episodeId"), req)
		return gin.H{"episode": episode}, err
	}))
	r.POST("/dramas/:id/episodes/:episodeId/approve", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.ApproveDramaEpisode(userID, c.Param("id"), c.Param("episodeId"))
	}))
	r.POST("/dramas/:id/episodes/:episodeId/rescript", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.RescriptDramaEpisode(userID, c.Param("id"), c.Param("episodeId"))
	}))
}
