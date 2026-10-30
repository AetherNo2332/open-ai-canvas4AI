package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

// 长期记忆文件接口经 RegisterAgentMemoryRoutes / RegisterAgentMemoryAdminRoutes 注册：
// 需要登录，只操作当前用户自己的文件，管理员接口只对管理员开放；旧经验接口已退役。
func TestAgentMemoryDocumentHTTPOwnershipAndAdmin(t *testing.T) {
	_, svc, db := workspaceHTTPFixture(t)
	for _, row := range []any{
		&model.User{ID: "memory-admin", Username: "memory-admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive},
		&model.AuthSession{ID: "memory-admin-session", UserID: "memory-admin", TokenHash: auth.HashToken("admin"), ExpiresAt: time.Now().Add(time.Hour)},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	RegisterAgentMemoryRoutes(router.Group("/api"), svc)
	RegisterAgentMemoryAdminRoutes(router.Group("/api"), svc)
	call := func(method, path, body, session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if session != "" {
			req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: session})
		}
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	const user, admin = "workspace-session.token", "memory-admin-session.admin"
	for _, route := range [][2]string{{"GET", "/api/agent/memory"}, {"PUT", "/api/agent/memory"}, {"PATCH", "/api/agent/memory/settings"}, {"GET", "/api/agent/memory/revisions"}, {"GET", "/api/admin/agent-memory-documents"}} {
		if result := call(route[0], route[1], `{}`, ""); result.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s %s: %d", route[0], route[1], result.Code)
		}
	}
	if result := call("PUT", "/api/agent/memory", `{"revision":1,"entries":[{"section":"user","text":"称呼用户为长官"}]}`, user); result.Code != http.StatusOK {
		t.Fatalf("save: %d %s", result.Code, result.Body)
	}
	if result := call("PUT", "/api/agent/memory", `{"revision":1,"entries":[],"userId":"other"}`, user); result.Code != http.StatusBadRequest {
		t.Fatalf("unknown field accepted: %d", result.Code)
	}
	if result := call("PATCH", "/api/agent/memory/settings", `{"mode":"review"}`, user); result.Code != http.StatusOK {
		t.Fatalf("mode: %d %s", result.Code, result.Body)
	}
	revisions := call("GET", "/api/agent/memory/revisions", "", user)
	var envelope struct {
		Data struct {
			Revisions []struct {
				ID string `json:"id"`
			} `json:"revisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(revisions.Body.Bytes(), &envelope); err != nil || len(envelope.Data.Revisions) != 1 {
		t.Fatalf("revisions: %s %v", revisions.Body, err)
	}
	revisionID := envelope.Data.Revisions[0].ID
	// 管理员能看到别人的文件，但不能撤销别人的修订（撤销按当前用户归属查找）。
	if result := call("POST", "/api/agent/memory/revisions/"+revisionID+"/undo", `{}`, admin); result.Code != http.StatusNotFound {
		t.Fatalf("foreign undo: %d %s", result.Code, result.Body)
	}
	if result := call("GET", "/api/admin/agent-memory-documents?userId=workspace-user", "", user); result.Code != http.StatusForbidden {
		t.Fatalf("non-admin listed documents: %d", result.Code)
	}
	if result := call("GET", "/api/admin/agent-memory-documents?userId=workspace-user", "", admin); result.Code != http.StatusOK || !strings.Contains(result.Body.String(), "称呼用户为长官") {
		t.Fatalf("admin read: %d %s", result.Code, result.Body)
	}
	if result := call("POST", "/api/agent/memory/revisions/"+revisionID+"/undo", `{}`, user); result.Code != http.StatusOK {
		t.Fatalf("owner undo: %d %s", result.Code, result.Body)
	}
	for _, retired := range []string{"/api/agent/memories", "/api/admin/agent-lessons"} {
		if result := call("GET", retired, "", user); result.Code != http.StatusNotFound {
			t.Fatalf("retired lesson route %s still served: %d", retired, result.Code)
		}
	}
}
