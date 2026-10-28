package handler

import (
	"encoding/json"
	"fmt"
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

func TestProjectAssetSyncHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := db.DB()
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	if err := db.AutoMigrate(&model.User{}, &model.AuthSession{}, &model.SystemSetting{}, &model.Project{}, &model.Asset{}, &model.AssetVersion{}, &model.ProjectAssetLink{}, &model.ProjectAssetFolder{}, &model.AssetRepresentation{}, &model.Shot{}, &model.ShotAssetReference{}, &model.AssetFolder{}, &model.CanvasProject{}, &model.UserDailyActivity{}); err != nil {
		t.Fatal(err)
	}
	for _, record := range []any{
		&model.User{ID: "owner", Username: "owner", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "owner", UserID: "owner", TokenHash: auth.HashToken("test-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.Project{ID: "project", UserID: "owner", Revision: 4},
		&model.Asset{ID: "asset", UserID: "owner", Kind: "text", Category: model.AssetCategoryMaterial, PayloadJSON: `{}`},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	router := gin.New()
	svc := service.New(repository.New(db), t.TempDir())
	previousRuntime := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() { ConfigureRuntime(previousRuntime) })
	RegisterProjectRoutes(router.Group("/api"), svc)
	RegisterUserDataRoutes(router.Group("/api"), svc)
	call := func(method, body string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/projects/project/assets/asset/sync", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if authenticated {
			r.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "owner.test-token"})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := call("GET", "", false); w.Code != 401 {
		t.Fatalf("auth: %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"asset":null`) || !strings.Contains(w.Body.String(), `"projectRevision":4`) {
		t.Fatalf("baseline: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{}`, `{"expectedProjectRevision":null}`, `{"expectedProjectRevision":-1}`, `{"expectedProjectRevision":1.5}`} {
		if w := call("PUT", body, true); w.Code != 400 {
			t.Fatalf("invalid body %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if w := call("PUT", `{"expectedProjectRevision":4,"category":"prop","folderId":""}`, true); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	w := call("PUT", `{"expectedProjectRevision":4,"category":"other"}`, true)
	var envelope struct {
		Reason    string `json:"reason"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if w.Code != 409 || envelope.Reason != "project_asset_sync_conflict" || !envelope.Retryable {
		t.Fatalf("conflict: %d %s", w.Code, w.Body.String())
	}
	for _, want := range []int{200, 412} {
		r := httptest.NewRequest("PUT", "/api/assets/fresh", strings.NewReader(`{"asset":{"id":"fresh","kind":"text","title":"new","coverUrl":"","tags":[],"data":{"content":"new"}}}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("If-None-Match", "*")
		r.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "owner.test-token"})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("create-only want=%d got=%d %s", want, w.Code, w.Body.String())
		}
		if want == 412 && !strings.Contains(w.Body.String(), `"reason":"asset_already_exists"`) {
			t.Fatalf("create-only reason: %s", w.Body.String())
		}
	}
}

// 角色库、我的画风、声音是画布与短剧共用的个人资料库，短剧关闭时不能一起被拦截；
// /projects 系列接口仍受短剧开关控制，且两类接口都必须先登录。
func TestShortDramaFeatureGateKeepsSharedLibraries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("shortDramaEnabled=%t", enabled), func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = sqlDB.Close() })
			if err := db.AutoMigrate(&model.User{}, &model.AuthSession{}, &model.SystemSetting{},
				&model.Asset{}, &model.AssetVersion{}, &model.AssetRepresentation{}, &model.CharacterVoiceBinding{},
				&model.VoiceProfile{}, &model.StyleProfile{}, &model.Project{}, &model.ProjectAssetLink{},
				&model.ProjectAssetFolder{}, &model.Resource{}, &model.Shot{}, &model.ShotAssetReference{},
				&model.ProjectUnit{}, &model.CanvasProject{}); err != nil {
				t.Fatal(err)
			}
			for _, record := range []any{
				&model.User{ID: "owner", Username: "owner", Email: "owner@example.invalid", Role: model.UserRoleUser, Status: model.UserStatusActive},
				&model.AuthSession{ID: "owner", UserID: "owner", TokenHash: auth.HashToken("test-token"), ExpiresAt: time.Now().Add(time.Hour)},
				&model.SystemSetting{Key: "feature_availability", ValueJSON: fmt.Sprintf(`{"shortDramaEnabled":%t}`, enabled)},
			} {
				if err := db.Create(record).Error; err != nil {
					t.Fatal(err)
				}
			}
			svc := service.New(repository.New(db), t.TempDir())
			character, err := svc.CreateCharacter("owner", service.CreateCharacterRequest{Name: "Existing character"})
			if err != nil {
				t.Fatal(err)
			}
			router := gin.New()
			RegisterCanvasAPI(router.Group("/api"), svc)
			call := func(method, path, body, actor string, want int) {
				t.Helper()
				req := httptest.NewRequest(method, "/api"+path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				if actor != "" {
					req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: actor + ".test-token"})
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != want {
					t.Fatalf("%s %s actor=%q: got %d want %d: %s", method, path, actor, w.Code, want, w.Body.String())
				}
				if want == http.StatusOK {
					var envelope struct {
						Code int `json:"code"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Code != 0 {
						t.Fatalf("%s %s: unsuccessful envelope: %s", method, path, w.Body.String())
					}
				}
			}
			for _, route := range []struct{ method, path, body string }{
				{http.MethodGet, "/characters", ""},
				{http.MethodGet, "/characters/" + character.Asset.ID, ""},
				{http.MethodPost, "/characters", `{"name":"New character"}`},
				{http.MethodGet, "/style-profiles", ""},
				{http.MethodGet, "/voice-profiles", ""},
			} {
				call(route.method, route.path, route.body, "", http.StatusUnauthorized)
				call(route.method, route.path, route.body, "owner", http.StatusOK)
			}
			projectWant := http.StatusForbidden
			if enabled {
				projectWant = http.StatusOK
			}
			call(http.MethodGet, "/projects", "", "", http.StatusUnauthorized)
			call(http.MethodGet, "/projects", "", "owner", projectWant)
		})
	}
}
