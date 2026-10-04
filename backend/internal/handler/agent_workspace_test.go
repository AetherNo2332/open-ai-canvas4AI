package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"infinite-canvas/backend/internal/service"
)

func workspaceHTTP(t *testing.T) func(string, string, string, bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.User{ID: "workspace-user", Username: "workspace-user", Status: model.UserStatusActive},
		&model.AuthSession{ID: "workspace-session", UserID: "workspace-user", TokenHash: auth.HashToken("token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CanvasProject{ID: "own", UserID: "workspace-user", PayloadJSON: `{"nodes":[]}`},
		&model.CanvasProject{ID: "foreign", UserID: "other", PayloadJSON: `{"nodes":[]}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	svc := service.New(repository.New(db), t.TempDir())
	RegisterAgentWorkspaceRoutes(router.Group("/api"), svc)
	RegisterAgentCrewRoutes(router.Group("/api"), svc)
	return func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		url := "/api/agent/workspaces/" + path
		if strings.HasPrefix(path, "/api/") {
			url = path
		}
		r := httptest.NewRequest(method, url, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if authenticated {
			r.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "workspace-session.token"})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
}

func TestAgentWorkspaceHTTPRevisionAndOwnership(t *testing.T) {
	call := workspaceHTTP(t)
	if w := call("GET", "own", "", false); w.Code != 401 {
		t.Fatalf("authentication: %d %s", w.Code, w.Body)
	}
	for _, path := range []string{"foreign", "missing", "foreign/skills"} {
		if w := call("GET", path, "", true); w.Code != 404 {
			t.Fatalf("ownership: %d %s", w.Code, w.Body)
		}
	}
	w := call("PATCH", "own", `{"revision":0,"agentsMd":"项目规则"}`, true)
	var response struct {
		Data service.AgentWorkspaceView `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Data.Revision != 1 || response.Data.AgentsMD != "项目规则" {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	w = call("PUT", "own/skills", `{"revision":0,"skills":[]}`, true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "agent_workspace_revision_conflict") {
		t.Fatalf("CAS: %d %s", w.Code, w.Body)
	}
	w = call("PUT", "own/skills", `{"revision":1,"skills":[]}`, true)
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 || response.Data.Revision != 2 {
		t.Fatalf("skill CAS: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{}`, `{"agentsMd":"lost revision"}`, `{"revision":2}`, `{"revision":2,"agentsMd":"x","unknown":true}`, `{"revision":2,"agentsMd":"x"} {}`, `null`} {
		if w := call("PATCH", "own", body, true); w.Code != 400 {
			t.Fatalf("invalid %s: %d %s", body, w.Code, w.Body)
		}
	}
	oversized, _ := json.Marshal(map[string]any{"revision": 2, "agentsMd": strings.Repeat("界", 22000)})
	if w := call("PATCH", "own", string(oversized), true); w.Code != 400 {
		t.Fatalf("byte limit: %d %s", w.Code, w.Body)
	}
	if w := call("PUT", "own/skills", `{"revision":2}`, true); w.Code != 400 {
		t.Fatalf("missing skills: %d %s", w.Code, w.Body)
	}
	for _, body := range []string{`{"revision":2,"skills":null}`, `{"revision":2,"skills":[{"skillId":"missing","skillVersionId":"v","position":0}]}`} {
		if w := call("PUT", "own/skills", body, true); w.Code != 400 {
			t.Fatalf("incomplete selection: %d %s", w.Code, w.Body)
		}
	}
}
