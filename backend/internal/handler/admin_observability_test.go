package handler

import (
	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
	"testing"
)

func TestAdminObservabilityRouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAdminObservabilityRoutes(router.Group("/api"), &service.Service{})
	for _, route := range router.Routes() {
		if route.Method == "GET" && route.Path == "/api/admin/observability/overview" {
			return
		}
	}
	t.Fatal("observability overview route is not registered")
}
