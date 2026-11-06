package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	as "yingce/backend/internal/agentservice"
	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// 真实路由 + 真实 service + 迁移后的 SQLite：Session 只按登录身份读写，伪造范围与跨用户访问被拒绝。
func TestAgentSessionHTTPScopeAndOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/agent-sessions.db?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.User{ID: "http-a", Username: "http-a", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.User{ID: "http-b", Username: "http-b", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "session-a", UserID: "http-a", TokenHash: auth.HashToken("token-a"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.AuthSession{ID: "session-b", UserID: "http-b", TokenHash: auth.HashToken("token-b"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.Project{ID: "drama-1", UserID: "http-a", Name: "短剧", Type: "short-drama", Status: model.ProjectStatusActive, Revision: 1},
		&model.CanvasProject{ID: "drama-1", UserID: "http-b", Title: "B 的画布", PayloadJSON: "{}"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), svc)
	call := func(method, path string, body any, cookie string) (*httptest.ResponseRecorder, platformEnvelope[json.RawMessage]) {
		raw := []byte(nil)
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: cookie})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		var envelope platformEnvelope[json.RawMessage]
		_ = json.Unmarshal(w.Body.Bytes(), &envelope)
		return w, envelope
	}
	drama := as.Scope{Application: as.ApplicationDrama, SpaceType: as.SpaceDramaProject, SpaceID: "drama-1"}
	create := as.CreateSessionRequest{DefinitionID: "drama.agent", Scope: drama, IdempotencyKey: "http-1"}

	if w, _ := call(http.MethodPost, "/api/agent/v2/sessions", create, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous create: %d %s", w.Code, w.Body.String())
	}
	w, envelope := call(http.MethodPost, "/api/agent/v2/sessions", create, "session-a.token-a")
	var first as.SessionView
	if w.Code != http.StatusOK || json.Unmarshal(envelope.Data, &first) != nil || first.ID == "" || first.Scope != drama {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	second := create
	second.IdempotencyKey = "http-2"
	w, envelope = call(http.MethodPost, "/api/agent/v2/sessions", second, "session-a.token-a")
	var other as.SessionView
	if w.Code != http.StatusOK || json.Unmarshal(envelope.Data, &other) != nil || other.ID == first.ID {
		t.Fatalf("second session in the same space: %d %s", w.Code, w.Body.String())
	}

	// The same short space ID owned by user B as a canvas is not reachable by A,
	// and B cannot reach A's drama project with a forged drama scope.
	forged := as.CreateSessionRequest{DefinitionID: "canvas.agent", Scope: as.Scope{Application: as.ApplicationCanvas, SpaceType: as.SpaceCanvas, SpaceID: "drama-1"}, IdempotencyKey: "forged"}
	if w, envelope := call(http.MethodPost, "/api/agent/v2/sessions", forged, "session-a.token-a"); w.Code != http.StatusNotFound || envelope.Reason != string(as.CodeNotFound) {
		t.Fatalf("forged canvas scope: %d %s", w.Code, w.Body.String())
	}
	if w, envelope := call(http.MethodPost, "/api/agent/v2/sessions", create, "session-b.token-b"); w.Code != http.StatusNotFound || envelope.Reason != string(as.CodeNotFound) {
		t.Fatalf("foreign drama scope: %d %s", w.Code, w.Body.String())
	}
	mismatch := create
	mismatch.Scope.Application, mismatch.IdempotencyKey = as.ApplicationEcommerce, "mismatch"
	if w, envelope := call(http.MethodPost, "/api/agent/v2/sessions", mismatch, "session-a.token-a"); w.Code != http.StatusForbidden || envelope.Reason != string(as.CodeScopeMismatch) {
		t.Fatalf("definition/scope mismatch: %d %s", w.Code, w.Body.String())
	}

	if w, envelope := call(http.MethodGet, "/api/agent/v2/sessions/"+first.ID, nil, "session-b.token-b"); w.Code != http.StatusNotFound || envelope.Reason != string(as.CodeNotFound) {
		t.Fatalf("foreign read: %d %s", w.Code, w.Body.String())
	}
	if w, _ := call(http.MethodPost, "/api/agent/v2/sessions/"+first.ID+"/archive", gin.H{"expectedRevision": 1}, "session-b.token-b"); w.Code != http.StatusNotFound {
		t.Fatalf("foreign archive: %d %s", w.Code, w.Body.String())
	}
	w, envelope = call(http.MethodGet, "/api/agent/v2/sessions?application=drama&spaceType=drama_project&spaceId=drama-1&limit=1", nil, "session-a.token-a")
	var page as.SessionPage
	if w.Code != http.StatusOK || json.Unmarshal(envelope.Data, &page) != nil || len(page.Sessions) != 1 || page.NextCursor == "" {
		t.Fatalf("first page: %d %s", w.Code, w.Body.String())
	}
	w, envelope = call(http.MethodGet, "/api/agent/v2/sessions?application=drama&spaceType=drama_project&spaceId=drama-1&limit=1&cursor="+page.NextCursor, nil, "session-a.token-a")
	var next as.SessionPage
	if w.Code != http.StatusOK || json.Unmarshal(envelope.Data, &next) != nil || len(next.Sessions) != 1 || next.Sessions[0].ID == page.Sessions[0].ID || next.NextCursor != "" {
		t.Fatalf("second page: %d %s", w.Code, w.Body.String())
	}
	w, envelope = call(http.MethodGet, "/api/agent/v2/sessions", nil, "session-b.token-b")
	var foreign as.SessionPage
	if w.Code != http.StatusOK || json.Unmarshal(envelope.Data, &foreign) != nil || len(foreign.Sessions) != 0 {
		t.Fatalf("foreign list: %d %s", w.Code, w.Body.String())
	}
	if w, envelope := call(http.MethodPost, "/api/agent/v2/sessions/"+first.ID+"/archive", gin.H{"expectedRevision": 9}, "session-a.token-a"); w.Code != http.StatusConflict || envelope.Reason != string(as.CodeSessionConflict) {
		t.Fatalf("stale archive: %d %s", w.Code, w.Body.String())
	}
	if w, _ := call(http.MethodPost, "/api/agent/v2/sessions/"+first.ID+"/archive", gin.H{"expectedRevision": first.Revision}, "session-a.token-a"); w.Code != http.StatusOK {
		t.Fatalf("archive: %d %s", w.Code, w.Body.String())
	}
	w, envelope = call(http.MethodGet, "/api/agent/v2/definitions?application=drama", nil, "session-a.token-a")
	var defs struct {
		Definitions []as.DefinitionView `json:"definitions"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(envelope.Data, &defs) != nil || len(defs.Definitions) != 1 || defs.Definitions[0].ID != "drama.agent" {
		t.Fatalf("definitions: %d %s", w.Code, w.Body.String())
	}
}
