package handler

import (
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

func registerDramaProjectRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/dramas", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		page, err := parsePositiveQueryInt(c.Query("page"), 1)
		if err != nil {
			return nil, service.BadAuthRequest("页码无效")
		}
		pageSize, err := parsePositiveQueryInt(c.Query("pageSize"), 24)
		if err != nil || pageSize > 100 {
			return nil, service.BadAuthRequest("每页数量无效")
		}
		return svc.ListDramas(userID, service.DramaListRequest{Page: page, PageSize: pageSize, Query: c.Query("q"), Status: c.Query("status")})
	}))
	r.GET("/dramas/asset-schemes", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaAssetSchemes(), nil
	}))
	r.GET("/dramas/models", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaModelCatalog(userID)
	}))
	r.POST("/dramas/estimate", dramaRoute(svc, 64<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.DramaEstimateRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.EstimateDrama(userID, req)
	}))
	// 小说正文随创建请求一次提交；600 万字 UTF-8 约 18MB，留出 JSON 转义余量。保存草稿时还带一份原始正文，上限翻倍。
	r.POST("/dramas", dramaRoute(svc, 64<<20, func(c *gin.Context, userID string) (any, error) {
		var req service.CreateDramaRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.CreateDrama(userID, req)
	}))
	// 草稿：读取创建页表单，或整体替换（start=true 时替换后直接开工）；请求体同创建，另带一份原始正文。
	r.GET("/dramas/:id/draft", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaDraft(userID, c.Param("id"))
	}))
	r.PUT("/dramas/:id/draft", dramaRoute(svc, 64<<20, func(c *gin.Context, userID string) (any, error) {
		var req service.CreateDramaRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.SaveDramaDraft(userID, c.Param("id"), req)
	}))
	r.DELETE("/dramas/:id", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return gin.H{"deleted": true}, svc.DeleteDrama(userID, c.Param("id"))
	}))
	r.GET("/dramas/:id", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.DramaSummary(userID, c.Param("id"))
	}))
	r.PATCH("/dramas/:id/settings", dramaRoute(svc, 64<<10, func(c *gin.Context, userID string) (any, error) {
		var req service.UpdateDramaSettingsRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.UpdateDramaSettings(userID, c.Param("id"), req)
	}))
}
