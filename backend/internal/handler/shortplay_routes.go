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
