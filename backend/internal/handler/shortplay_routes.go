package handler

import (
	"yingce/backend/internal/service"
	"yingce/backend/internal/shortplay"

	"github.com/gin-gonic/gin"
)

// registerShortplayRoutes 注册独立 shortplay 工作台的 HTTP 合同。
//
// W03 只含工作台骨架的四个接口：列表、建档（含全部分集原文）、详情、设置整份保存。
// 资产、提取、画布与收录按后续分片接入同一路由组；请求体上限、鉴权与响应封装沿用
// 同包的 dramaRoute / bindDramaJSON，与 /dramas 保持同一口径。
func registerShortplayRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.GET("/shortplays", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		items, err := svc.ListShortplays(userID)
		return gin.H{"shortplays": items}, err
	}))
	// 整篇剧本随建档请求一次提交（32MB），与 /dramas 同口径；服务端只校验切分，不调模型。
	r.POST("/shortplays", dramaRoute(svc, 32<<20, func(c *gin.Context, userID string) (any, error) {
		var req shortplay.CreateShortplayRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.CreateShortplay(userID, req)
	}))
	// :id 是 projects 容器 ID，与 /dramas/:id 同口径。
	r.GET("/shortplays/:id", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.ShortplayDetail(userID, c.Param("id"))
	}))
	// 资产列表与写入：:id 同时接受 shortplayId 与 projectId。POST 一次提交身份行 + 设定，
	// assetId=0 新建、非 0 更新；跨作品一律 404。
	r.GET("/shortplays/:id/assets", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		items, err := svc.ShortplayAssets(userID, c.Param("id"))
		if err != nil {
			return nil, err
		}
		return gin.H{"assets": items}, nil
	}))
	r.POST("/shortplays/:id/assets", dramaRoute(svc, 1<<20, func(c *gin.Context, userID string) (any, error) {
		var req shortplay.UpsertAssetRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.UpsertShortplayAsset(userID, c.Param("id"), req)
	}))
	// 集画布按需创建：第一次「进精修画布」时建出画布行；已存在返回 created=false。
	r.POST("/shortplays/:id/episodes/:episodeId/canvas", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.EnsureShortplayEpisodeCanvas(userID, c.Param("id"), c.Param("episodeId"))
	}))
	// 按画布反查集归属；未命中返回 {linked:false} 而不是 404（自由画布是正常状态）。
	r.GET("/shortplays/by-canvas/:canvasId", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		binding, found, err := svc.ShortplayByCanvas(userID, c.Param("canvasId"))
		if err != nil {
			return nil, err
		}
		if !found {
			return gin.H{"linked": false}, nil
		}
		return gin.H{
			"linked":        true,
			"shortplayId":   binding.ShortplayID,
			"projectId":     binding.ProjectID,
			"episodeId":     binding.EpisodeID,
			"episodeNumber": binding.EpisodeNumber,
			"episodeTitle":  binding.EpisodeTitle,
		}, nil
	}))
	// 提取入队：已在途时返回那一条（queued=false），不排第二条。
	r.POST("/shortplays/:id/extract", dramaRoute(svc, 0, func(c *gin.Context, userID string) (any, error) {
		return svc.QueueShortplayExtract(userID, c.Param("id"))
	}))
	// 页面出图入口：只入队 asset_image job，与后续 Agent 工具走同一条去重与锁定判断。
	r.POST("/shortplays/:id/assets/images", dramaRoute(svc, 1<<20, func(c *gin.Context, userID string) (any, error) {
		var req shortplay.QueueAssetImagesRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.QueueShortplayAssetImages(userID, c.Param("id"), req)
	}))
	// 设置：整份提交（分步骤模型 + 参考图方案 + 风格与语言），服务端归一后回传 settingsJson。
	// 整份写而不是逐字段 patch：设置之间互相牵制，逐字段写会落库「各自合法、组合非法」的中间态。
	r.PATCH("/shortplays/:id/settings", dramaRoute(svc, 64<<10, func(c *gin.Context, userID string) (any, error) {
		var req shortplay.UpdateSettingsRequest
		if err := bindDramaJSON(c, &req); err != nil {
			return nil, err
		}
		return svc.UpdateShortplaySettings(userID, c.Param("id"), req.Settings)
	}))
}
