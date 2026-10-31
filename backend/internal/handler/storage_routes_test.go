package handler

import (
	"testing"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func TestStorageConnectionTestRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	RegisterAdminRoutes(group, &service.Service{})
	RegisterUserDataRoutes(group, &service.Service{})
	wanted := map[string]bool{
		"POST /api/admin/settings/oss/test": false,
		"POST /api/settings/oss/test":       false,
		// 模型服务拉取参考素材前可能先发 HEAD；公开签名地址必须同时接受 GET 与 HEAD。
		"GET /api/public/resources/:id/file":            false,
		"HEAD /api/public/resources/:id/file":           false,
		"HEAD /api/public/resources/:id/file/:filename": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, exists := wanted[key]; exists {
			wanted[key] = true
		}
	}
	for route, found := range wanted {
		if !found {
			t.Errorf("route %s is not registered", route)
		}
	}
}
