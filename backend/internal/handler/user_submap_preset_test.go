package handler

import (
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

func TestUserSubmapPresetHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "presets.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, row := range []any{
		&model.User{ID: "preset-owner", Username: "preset-owner", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.User{ID: "preset-other", Username: "preset-other", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "preset-owner-session", UserID: "preset-owner", TokenHash: auth.HashToken("owner-token"), ExpiresAt: now.Add(time.Hour)},
		&model.AuthSession{ID: "preset-other-session", UserID: "preset-other", TokenHash: auth.HashToken("other-token"), ExpiresAt: now.Add(time.Hour)},
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
	RegisterUserSubmapPresetRoutes(router.Group("/api"), svc)
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
	valid := presetRequestBody(t, `{"version":1,"id":"forged","title":"我的镜头","nodes":[{"localId":"text-1","type":"text","title":"提示词","metadata":{"content":"正文"}}]}`)
	if response := call(http.MethodGet, "/api/user-submap-presets", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list = %d %s", response.Code, response.Body.String())
	}
	created := call(http.MethodPost, "/api/user-submap-presets", valid, "preset-owner-session.owner-token")
	if created.Code != http.StatusOK {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var createdEnvelope struct {
		Data struct {
			Preset model.UserSubmapPreset `json:"preset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdEnvelope); err != nil {
		t.Fatal(err)
	}
	preset := createdEnvelope.Data.Preset
	if preset.ID == "" || preset.ID == "forged" || preset.UserID != "preset-owner" || preset.Name != "我的镜头" || preset.Revision != 1 {
		t.Fatalf("server did not own preset identity: %+v", preset)
	}
	if !strings.Contains(preset.ManifestJSON, `"id":"`+preset.ID+`"`) || !strings.Contains(preset.ManifestJSON, `"content":"正文"`) {
		t.Fatalf("normalized manifest lost identity or content: %s", preset.ManifestJSON)
	}

	list := func(cookie string) []model.UserSubmapPreset {
		t.Helper()
		response := call(http.MethodGet, "/api/user-submap-presets", "", cookie)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("list = %d cache=%q %s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
		var envelope struct {
			Data struct {
				Presets []model.UserSubmapPreset `json:"presets"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data.Presets
	}
	if presets := list("preset-owner-session.owner-token"); len(presets) != 1 || presets[0].ID != preset.ID {
		t.Fatalf("owner list = %+v", presets)
	}
	if presets := list("preset-other-session.other-token"); len(presets) != 0 {
		t.Fatalf("cross-user list must be empty: %+v", presets)
	}

	updatedBody := presetRequestBody(t, `{"version":1,"title":"改名后的镜头","nodes":[{"localId":"text-1","type":"text","title":"提示词"}]}`)
	if response := call(http.MethodPatch, "/api/user-submap-presets/"+preset.ID, updatedBody, "preset-other-session.other-token"); response.Code != http.StatusNotFound {
		t.Fatalf("cross-user update = %d %s", response.Code, response.Body.String())
	}
	updated := call(http.MethodPatch, "/api/user-submap-presets/"+preset.ID, updatedBody, "preset-owner-session.owner-token")
	if updated.Code != http.StatusOK {
		t.Fatalf("update = %d %s", updated.Code, updated.Body.String())
	}
	var updatedEnvelope struct {
		Data struct {
			Preset model.UserSubmapPreset `json:"preset"`
		} `json:"data"`
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &updatedEnvelope); err != nil {
		t.Fatal(err)
	}
	if updatedEnvelope.Data.Preset.Revision != 2 || updatedEnvelope.Data.Preset.Name != "改名后的镜头" {
		t.Fatalf("update did not bump revision or rename: %+v", updatedEnvelope.Data.Preset)
	}

	for name, body := range map[string]string{
		"wrong version": presetRequestBody(t, `{"version":2,"title":"未来","nodes":[{"localId":"text-1","type":"text","title":"提示词"}]}`),
		"dangling ref":  presetRequestBody(t, `{"version":1,"title":"坏引用","nodes":[{"localId":"image-1","type":"image","title":"首帧","metadata":{"referenceTextNodeIds":["text-9"]}}]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if response := call(http.MethodPost, "/api/user-submap-presets", body, "preset-owner-session.owner-token"); response.Code != http.StatusBadRequest {
				t.Fatalf("invalid create = %d %s", response.Code, response.Body.String())
			}
		})
	}

	if response := call(http.MethodDelete, "/api/user-submap-presets/"+preset.ID, "", "preset-other-session.other-token"); response.Code != http.StatusNotFound {
		t.Fatalf("cross-user delete = %d %s", response.Code, response.Body.String())
	}
	if response := call(http.MethodDelete, "/api/user-submap-presets/"+preset.ID, "", "preset-owner-session.owner-token"); response.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", response.Code, response.Body.String())
	}
	if presets := list("preset-owner-session.owner-token"); len(presets) != 0 {
		t.Fatalf("preset was not deleted: %+v", presets)
	}
}

func presetRequestBody(t *testing.T, manifest string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]string{"manifestJson": manifest})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
