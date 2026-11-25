package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestCanvasHistoryHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "history.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, row := range []any{
		&model.User{ID: "history-owner", Username: "history-owner", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.User{ID: "history-other", Username: "history-other", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "history-owner-session", UserID: "history-owner", TokenHash: auth.HashToken("owner-token"), ExpiresAt: now.Add(time.Hour)},
		&model.AuthSession{ID: "history-other-session", UserID: "history-other", TokenHash: auth.HashToken("other-token"), ExpiresAt: now.Add(time.Hour)},
		&model.CanvasProject{
			ID: "history-canvas", UserID: "history-owner", Title: "当前版本", Revision: 3,
			PayloadJSON: `{"id":"history-canvas","title":"当前版本","revision":3,"nodes":[{"id":"current-secret","type":"text"}],"connections":[]}`,
			CreatedAt:   now.Add(-time.Hour), UpdatedAt: now,
		},
		&model.CanvasSnapshot{
			ID: "history-snapshot", CanvasID: "history-canvas", UserID: "history-owner", Revision: 1,
			Title: "旧版本", NodeCount: 1, ConnectionCount: 0, PayloadBytes: 120, Reason: "automatic",
			PayloadJSON:      `{"id":"history-canvas","title":"旧版本","revision":1,"nodes":[{"id":"old-secret","type":"text"}],"connections":[]}`,
			ContentUpdatedAt: now.Add(-30 * time.Minute), CreatedAt: now.Add(-20 * time.Minute),
		},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	previous := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() {
		ConfigureRuntime(previous)
		svc.Close()
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	router := gin.New()
	RegisterCanvasHistoryRoutes(router.Group("/api"), svc)
	call := func(method, path, body, cookie string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookie})
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	if response := call(http.MethodGet, "/api/canvas-projects/history-canvas/history", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous history = %d %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/api/canvas-projects/history-canvas/history", "", "history-other-session.other-token"); response.Code != http.StatusNotFound {
		t.Fatalf("foreign history = %d %s", response.Code, response.Body.String())
	}
	listed := call(http.MethodGet, "/api/canvas-projects/history-canvas/history", "", "history-owner-session.owner-token")
	if listed.Code != http.StatusOK || listed.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("history list = %d %v %s", listed.Code, listed.Header(), listed.Body.String())
	}
	if body := listed.Body.String(); strings.Contains(body, "old-secret") || strings.Contains(body, "payloadJson") || !strings.Contains(body, `"currentRevision":3`) {
		t.Fatalf("history list leaked payload or revision: %s", body)
	}
	preview := call(http.MethodGet, "/api/canvas-projects/history-canvas/history/history-snapshot", "", "history-owner-session.owner-token")
	if preview.Code != http.StatusOK || preview.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("history preview = %d %v %s", preview.Code, preview.Header(), preview.Body.String())
	}
	var previewEnvelope struct {
		Data struct {
			Snapshot map[string]any  `json:"snapshot"`
			Project  json.RawMessage `json:"project"`
		} `json:"data"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &previewEnvelope); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(previewEnvelope.Data.Project, []byte("old-secret")) || previewEnvelope.Data.Snapshot["revision"] != float64(1) {
		t.Fatalf("history preview lost snapshot content: %s", preview.Body.String())
	}

	if response := call(http.MethodPost, "/api/canvas-projects/history-canvas/history/history-snapshot/restore", `{}`, "history-owner-session.owner-token"); response.Code != http.StatusPreconditionRequired {
		t.Fatalf("restore without revision = %d %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/canvas-projects/history-canvas/history/history-snapshot/restore", `{"revision":2}`, "history-owner-session.owner-token"); response.Code != http.StatusConflict {
		t.Fatalf("stale restore = %d %s", response.Code, response.Body.String())
	}
	restored := call(http.MethodPost, "/api/canvas-projects/history-canvas/history/history-snapshot/restore", `{"revision":3}`, "history-owner-session.owner-token")
	if restored.Code != http.StatusOK {
		t.Fatalf("restore = %d %s", restored.Code, restored.Body.String())
	}
	var restoredEnvelope struct {
		Data struct {
			Project json.RawMessage `json:"project"`
		} `json:"data"`
	}
	if err := json.Unmarshal(restored.Body.Bytes(), &restoredEnvelope); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(restoredEnvelope.Data.Project, []byte("old-secret")) || !bytes.Contains(restoredEnvelope.Data.Project, []byte(`"revision":4`)) {
		t.Fatalf("restore response lost content or revision: %s", restored.Body.String())
	}
	var current model.CanvasProject
	if err := db.First(&current, "id = ?", "history-canvas").Error; err != nil {
		t.Fatal(err)
	}
	if current.Revision != 4 || !strings.Contains(current.PayloadJSON, "old-secret") {
		t.Fatalf("restore was not persisted: revision=%d payload=%s", current.Revision, current.PayloadJSON)
	}
	var backup model.CanvasSnapshot
	if err := db.First(&backup, "canvas_id = ? AND reason = ?", "history-canvas", "before_restore").Error; err != nil {
		t.Fatal(err)
	}
	if backup.Revision != 3 || !strings.Contains(backup.PayloadJSON, "current-secret") {
		t.Fatalf("pre-restore backup missing current content: %+v", backup)
	}
}
