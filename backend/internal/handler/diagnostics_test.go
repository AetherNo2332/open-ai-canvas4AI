package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestDiagnosticsExportHTTPRedactsHeaderValues(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:diagnostics-http?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.AuthSession{}, &model.Task{}, &model.TaskLog{}, &model.ApiCallLog{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, item := range []any{
		&model.User{ID: "diagnostics-user", Username: "diagnostics-user", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "diagnostics-session", UserID: "diagnostics-user", TokenHash: auth.HashToken("diagnostics-token"), ExpiresAt: now.Add(time.Hour)},
		&model.Task{ID: "diagnostics-task", UserID: "diagnostics-user", Type: "canvas_text", Status: model.TaskStatusFailed, Error: "Authorization: Bearer http-task-secret", CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
		&model.Task{ID: "foreign-diagnostics-task", UserID: "other-user", Type: "canvas_text", Status: model.TaskStatusFailed, CreatedAt: now.Add(-time.Minute), UpdatedAt: now},
	} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := service.New(repository.New(db), t.TempDir())
	router := gin.New()
	RegisterDiagnosticsRoutes(router.Group("/api"), svc)
	requestBody, err := json.Marshal(service.DiagnosticExportRequest{
		From: now.Add(-2 * time.Minute).Format(time.RFC3339Nano), To: now.Add(time.Minute).Format(time.RFC3339Nano), TaskID: "diagnostics-task",
		ClientEvents: []service.DiagnosticClientEvent{{
			ID:      "diagnostics-event",
			Message: "request failed\nAuthorization: Bearer http-client-secret\nCookie: sid=http-cookie-secret; theme=http-safe",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	previewRequest := httptest.NewRequest(http.MethodPost, "/api/diagnostics/preview", bytes.NewReader(requestBody))
	previewRequest.Header.Set("Content-Type", "application/json")
	previewRequest.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "diagnostics-session.diagnostics-token"})
	previewResponse := httptest.NewRecorder()
	router.ServeHTTP(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body=%s", previewResponse.Code, previewResponse.Body.String())
	}
	var previewEnvelope struct {
		Data service.DiagnosticPreview `json:"data"`
	}
	if err := json.Unmarshal(previewResponse.Body.Bytes(), &previewEnvelope); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/diagnostics/export", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "diagnostics-session.diagnostics-token"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("export status = %d, body=%s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("export content type = %q", contentType)
	}

	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 7 {
		t.Fatalf("diagnostic ZIP entries = %d, want 7", len(archive.File))
	}
	var manifest struct {
		Counts struct {
			ClientEvents int `json:"clientEvents"`
			Tasks        int `json:"tasks"`
			TaskLogs     int `json:"taskLogs"`
			Upstream     int `json:"upstreamCalls"`
		} `json:"counts"`
	}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "manifest.json" {
			if err := json.Unmarshal(content, &manifest); err != nil {
				t.Fatal(err)
			}
		}
		for _, secret := range []string{"http-task-secret", "http-client-secret", "http-cookie-secret"} {
			if strings.Contains(string(content), secret) {
				t.Fatalf("diagnostic entry %s leaked a synthetic header value", file.Name)
			}
		}
	}
	if manifest.Counts.Tasks != previewEnvelope.Data.TaskCount || manifest.Counts.TaskLogs != previewEnvelope.Data.TaskLogCount ||
		manifest.Counts.Upstream != previewEnvelope.Data.APICallCount || manifest.Counts.ClientEvents > previewEnvelope.Data.ClientEventLimit {
		t.Fatalf("manifest counts = %+v, preview = %+v", manifest.Counts, previewEnvelope.Data)
	}
	if manifest.Counts.ClientEvents != 1 || previewEnvelope.Data.TaskCount != 1 || previewEnvelope.Data.ClientEventLimit < 1 {
		t.Fatalf("unexpected preview counts = %+v", previewEnvelope.Data)
	}

	foreignBody, err := json.Marshal(service.DiagnosticExportRequest{TaskID: "foreign-diagnostics-task"})
	if err != nil {
		t.Fatal(err)
	}
	foreignRequest := httptest.NewRequest(http.MethodPost, "/api/diagnostics/export", bytes.NewReader(foreignBody))
	foreignRequest.Header.Set("Content-Type", "application/json")
	foreignRequest.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "diagnostics-session.diagnostics-token"})
	foreignResponse := httptest.NewRecorder()
	router.ServeHTTP(foreignResponse, foreignRequest)
	if foreignResponse.Code == http.StatusOK || foreignResponse.Header().Get("Content-Type") == "application/zip" {
		t.Fatalf("foreign task export was not rejected: status=%d content-type=%q", foreignResponse.Code, foreignResponse.Header().Get("Content-Type"))
	}
}
