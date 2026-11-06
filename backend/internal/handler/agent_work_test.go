package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The work, message, approval and budget routes behind the TS Agent SDK, over
// the real router with session cookies; actor kinds and owners come from the server.
func TestAgentWorkHTTPSurface(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(t.TempDir()+"/agent-work.db?_journal_mode=WAL&_busy_timeout=5000"), &gorm.Config{})
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
	decode := func(t *testing.T, envelope platformEnvelope[json.RawMessage], target any) {
		t.Helper()
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			t.Fatalf("decode %s: %v", envelope.Data, err)
		}
	}
	const a, b = "session-a.token-a", "session-b.token-b"

	w, envelope := call(http.MethodPost, "/api/agent/v2/work", gin.H{"goal": "一集短剧", "budgetMicrocredits": 1_000_000, "idempotencyKey": "w1", "sourceMessageId": "m0",
		"authorizedScopes": []gin.H{{"application": "drama", "spaceType": "drama_project", "spaceId": "drama-1"}}}, a)
	var work service.WorkView
	if w.Code != http.StatusOK {
		t.Fatalf("start work: %d %s", w.Code, w.Body.String())
	}
	decode(t, envelope, &work)
	if work.SupervisorSessionID == "" || work.BudgetGrantID == "" {
		t.Fatalf("work: %+v", work)
	}
	if w, _ := call(http.MethodGet, "/api/agent/v2/work/"+work.ID, nil, b); w.Code != http.StatusNotFound {
		t.Fatalf("foreign work read: %d %s", w.Code, w.Body.String())
	}
	// The supervisor Session is listed and readable; a message carries no client actor.
	w, envelope = call(http.MethodPost, "/api/agent/v2/sessions/"+work.SupervisorSessionID+"/messages", gin.H{"messageId": "u1", "text": "开始吧", "actor": "supervisor"}, a)
	if w.Code != http.StatusOK {
		t.Fatalf("send message: %d %s", w.Code, w.Body.String())
	}
	w, envelope = call(http.MethodGet, "/api/agent/v2/sessions/"+work.SupervisorSessionID+"/messages?after=0", nil, a)
	var page struct {
		Messages []struct {
			MessageID string `json:"messageId"`
			Actor     string `json:"actor"`
			Sequence  int64  `json:"sequence"`
		} `json:"messages"`
		NextAfter int64 `json:"nextAfter"`
	}
	decode(t, envelope, &page)
	if w.Code != http.StatusOK || len(page.Messages) != 1 || page.Messages[0].Actor != "user" || page.NextAfter != page.Messages[0].Sequence {
		t.Fatalf("messages: %d %s", w.Code, w.Body.String())
	}
	if w, _ := call(http.MethodGet, "/api/agent/v2/sessions/"+work.SupervisorSessionID+"/messages?after=0", nil, b); w.Code != http.StatusNotFound {
		t.Fatalf("foreign messages: %d", w.Code)
	}
	w, envelope = call(http.MethodGet, "/api/platform/v1/budget-grants/"+work.BudgetGrantID, nil, a)
	var grant struct {
		LimitMicrocredits     int64 `json:"limitMicrocredits"`
		AvailableMicrocredits int64 `json:"availableMicrocredits"`
	}
	decode(t, envelope, &grant)
	if w.Code != http.StatusOK || grant.LimitMicrocredits != 1_000_000 || grant.AvailableMicrocredits != 1_000_000 {
		t.Fatalf("budget read: %d %s", w.Code, w.Body.String())
	}
	if w, _ := call(http.MethodGet, "/api/platform/v1/budget-grants/"+work.BudgetGrantID, nil, b); w.Code != http.StatusNotFound {
		t.Fatalf("foreign budget read: %d", w.Code)
	}
	if w, envelope := call(http.MethodGet, "/api/agent/v2/work/"+work.ID+"/approvals", nil, a); w.Code != http.StatusOK || string(envelope.Data) != `{"approvals":[]}` {
		t.Fatalf("approvals: %d %s", w.Code, w.Body.String())
	}
	if w, envelope := call(http.MethodPost, "/api/agent/v2/approvals/missing/decision", gin.H{"decision": "approved", "expectedAuthorizationVersion": 1}, a); w.Code != http.StatusNotFound || envelope.Reason != "not_found" {
		t.Fatalf("missing approval: %d %s", w.Code, w.Body.String())
	}
	if w, envelope := call(http.MethodGet, "/api/agent/v2/work/"+work.ID+"/reports", nil, a); w.Code != http.StatusOK || string(envelope.Data) != `{"reports":[]}` {
		t.Fatalf("reports: %d %s", w.Code, w.Body.String())
	}
	w, envelope = call(http.MethodPost, "/api/agent/v2/work/"+work.ID+"/stop", gin.H{"expectedVersion": work.Version, "reason": "不做了"}, a)
	var stopped service.WorkView
	decode(t, envelope, &stopped)
	if w.Code != http.StatusOK || stopped.Status != "cancelled" {
		t.Fatalf("stop: %d %s", w.Code, w.Body.String())
	}
	if w, envelope := call(http.MethodPost, "/api/agent/v2/work/"+work.ID+"/stop", gin.H{"expectedVersion": 99}, b); w.Code != http.StatusNotFound || envelope.Reason != "not_found" {
		t.Fatalf("foreign stop: %d %s", w.Code, w.Body.String())
	}
}
