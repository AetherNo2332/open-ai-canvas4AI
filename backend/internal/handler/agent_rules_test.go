package handler

import (
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAgentRulesRetiredConfigurationRoutesAreAbsent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAgentRoutes(router.Group("/api"), nil)
	RegisterAgentWorkspaceRoutes(router.Group("/api"), nil)
	found := map[string]bool{}
	for _, route := range router.Routes() {
		if route.Path == "/api/agent/profile" || strings.HasSuffix(route.Path, "/workspaces/:canvasId/skills") {
			t.Fatalf("retired configuration route remains: %s %s", route.Method, route.Path)
		}
		found[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{"GET /api/agent/workspaces/:canvasId", "PATCH /api/agent/workspaces/:canvasId"} {
		if !found[route] {
			t.Fatalf("canvas document endpoint missing: %s", route)
		}
	}
}
