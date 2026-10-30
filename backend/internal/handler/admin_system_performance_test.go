package handler

import (
	"testing"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func TestAdminSystemPerformanceRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAdminSystemPerformanceRoutes(router.Group("/api"), &service.Service{})
	wanted := map[string]bool{
		// 本仓库的 Agent 并发由「Agent（beta）」调度配置维护，不注册上游的 agent-limit 路由。
		"GET /api/admin/system-performance":              false,
		"POST /api/admin/system-performance/cache/clear": false,
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
