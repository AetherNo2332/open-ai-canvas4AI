package handler

import (
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func registerDramaChapterRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/dramas/:id/chapters", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		chapters, err := svc.DramaChapterOutline(userID, c.Param("id"))
		return gin.H{"chapters": chapters}, err
	}))
	// 章节 ID 列表上限 3000 个，每个 36 字符加 JSON 引号与逗号。
	r.POST("/dramas/:id/chapters/produce", dramaRoute(svc, 256<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.ProduceDramaChaptersRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.ProduceDramaChapters(userID, c.Param("id"), req)
	}))
	r.GET("/dramas/:id/chapters/:chapterId", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		chapter, err := svc.DramaChapter(userID, c.Param("id"), c.Param("chapterId"))
		return gin.H{"chapter": chapter}, err
	}))
	r.PATCH("/dramas/:id/chapters/:chapterId", dramaRoute(svc, 1<<20, func(c *gin.Context, userID string) (any, error) {
		var req service.UpdateDramaChapterRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		chapter, err := svc.UpdateDramaChapter(userID, c.Param("id"), c.Param("chapterId"), req)
		return gin.H{"chapter": chapter}, err
	}))
	// 逐集追加或插到某一集前后：单集原文上限 1.2 万字，UTF-8 加 JSON 转义留足余量。
}
