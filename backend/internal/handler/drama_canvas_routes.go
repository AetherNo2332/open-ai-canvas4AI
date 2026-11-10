package handler

import (
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func registerDramaCanvasRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/dramas/:id/canvas", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaCanvas(userID, c.Param("id"))
	}))
	// 为投送到画布的短剧媒体登记素材记录（只建入口，不复制媒体）。
	r.POST("/dramas/:id/canvas-assets", dramaRoute(svc, 32<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.DramaCanvasAssetsRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.DramaCanvasAssets(userID, c.Param("id"), req)
	}))
	// 产物版本：kind=cast|video|final，ownerId 为设定、剧情段或集 ID。
	r.GET("/dramas/:id/takes", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaTakes(userID, c.Param("id"), c.Query("ownerId"), c.Query("kind"))
	}))
	r.PATCH("/dramas/:id/shots/:shotId", dramaRoute(svc, 64<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.UpdateDramaShotRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		shot, err := svc.UpdateDramaShot(userID, c.Param("id"), c.Param("shotId"), req)
		return gin.H{"shot": shot}, err
	}))
	r.POST("/dramas/:id/shots/:shotId/insert", dramaRoute(svc, 4<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.InsertDramaShotRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		shot, err := svc.InsertDramaShot(userID, c.Param("id"), c.Param("shotId"), req)
		return gin.H{"shot": shot}, err
	}))
	// 导出剪映草稿：表单字段 draftRoot 是用户本机的剪映草稿目录，captions=1 时附带台词字幕轨。
	// 用表单 POST 而不是 GET，避免本机路径进入访问日志与浏览器历史。
}
