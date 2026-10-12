package handler

import (
	"testing"

	"infinite-canvas/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func TestAdminAgentSkillDefaultsRoutesAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAdminAgentSkillDefaultsRoutes(router.Group("/api"), &service.Service{})
	for _, route := range router.Routes() {
		if route.Method == "GET" && route.Path == "/api/admin/agent/skill-defaults" {
			return
		}
	}
	t.Fatal("admin agent skill-defaults GET route is not registered")
}

func TestAdminAgentSkillDefaultsPutRouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAdminAgentSkillDefaultsRoutes(router.Group("/api"), &service.Service{})
	for _, route := range router.Routes() {
		if route.Method == "PUT" && route.Path == "/api/admin/agent/skill-defaults" {
			return
		}
	}
	t.Fatal("admin agent skill-defaults PUT route is not registered")
}
