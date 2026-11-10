package handler

import (
	"net/http"
	"time"
	"yingce/backend/internal/service"
	"github.com/gin-gonic/gin"
)

// RegisterArkPortraitRoutes 挂载方舟素材库接口：用户自建素材组、平台授权素材组与管理员管理。
// 除认证回调外都要求登录，归属与管理员权限都在 service 校验。
func RegisterArkPortraitRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerArkPortraitSessionRoutes(r, svc)
	registerArkPortraitGroupRoutes(r, svc)
	registerArkPortraitAssetRoutes(r, svc)
}

func registerAdminArkPortraitRoutes(r *gin.RouterGroup, svc *service.Service) {
	registerAdminArkPortraitGroupRoutes(r, svc)
	registerAdminArkPortraitAssetSessionRoutes(r, svc)
	registerAdminArkPortraitReviewRoutes(r, svc)
}

// bindArkJSON 限制请求体大小并解析 JSON；解析失败时已写入 400 响应。
func bindArkJSON(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if err := c.ShouldBindJSON(target); err != nil {
		fail(c, http.StatusBadRequest, err)
		return false
	}
	return true
}

// 真人认证与素材上传会调用方舟控制面（账号级 3-10 QPS），沿用资源导入的每分钟限额。
func enforceArkPortraitRate(c *gin.Context, svc *service.Service, key string) bool {
	policy, available := loadRuntimePolicy(c, svc)
	return available && enforceRateLimit(c, key, policy.Request.ResourceImportPerMinute, time.Minute)
}
