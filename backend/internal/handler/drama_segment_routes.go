package handler

import (
	"github.com/gin-gonic/gin"
	"strconv"
	"yingce/backend/internal/service"
)

func registerDramaSegmentRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.POST("/dramas/:id/episodes/:episodeId/segments/generate", dramaRoute(svc, 16<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.GenerateDramaSegmentsRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.GenerateDramaSegments(userID, c.Param("id"), c.Param("episodeId"), req)
	}))
	r.POST("/dramas/:id/episodes/:episodeId/segments/prompts", dramaRoute(svc, 16<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.GenerateDramaSegmentsRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.RequestDramaSegmentPrompts(userID, c.Param("id"), c.Param("episodeId"), req)
	}))
	r.POST("/dramas/:id/episodes/:episodeId/segments/:index/reprocess", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		index, err := strconv.Atoi(c.Param("index"))
		if err != nil || index < 0 {
			return nil, service.BadAuthRequest("剧情段序号无效")
		}
		return svc.ReprocessDramaSegmentVideo(userID, c.Param("id"), c.Param("episodeId"), index)
	}))
	r.PATCH("/dramas/:id/episodes/:episodeId/segments/:index", dramaRoute(svc, 64<<10, func(c *gin.Context, userID string) (any, error) {
		index, err := strconv.Atoi(c.Param("index"))
		if err != nil || index < 0 {
			return nil, service.BadAuthRequest("剧情段序号无效")
		}
		var req service.UpdateDramaSegmentRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return gin.H{"saved": true}, svc.UpdateDramaSegment(userID, c.Param("id"), c.Param("episodeId"), index, req)
	}))
	// 把精修画布上的视频采用为本段当前视频：默认只换本段，cascade 时后续已生成的段重新排队。
	r.PUT("/dramas/:id/episodes/:episodeId/segments/:index/video", dramaRoute(svc, 4<<10, func(c *gin.Context, userID string) (any, error) {
		index, err := strconv.Atoi(c.Param("index"))
		if err != nil || index < 0 {
			return nil, service.BadAuthRequest("剧情段序号无效")
		}
		var req service.AdoptDramaSegmentVideoRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.AdoptDramaSegmentVideo(userID, c.Param("id"), c.Param("episodeId"), index, req)
	}))
	// 作品的精修画布：返回最近使用的关联画布，没有时由前端新建。
}
