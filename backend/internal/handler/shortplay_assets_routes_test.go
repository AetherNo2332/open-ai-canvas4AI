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
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
	"yingce/backend/internal/shortplay"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// W04 资产读写与提取入队的 HTTP 合同：归属、跨账号隔离、revision 写入与连点去重。
func TestShortplayAssetRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
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
	if err := db.AutoMigrate(&model.User{}, &model.AuthSession{}, &model.SystemSetting{}, &model.Project{},
		&model.Shortplay{}, &model.ShortplayEpisode{}, &model.ShortplayAsset{}, &model.ShortplayAssetImage{},
		&model.ShortplayAssetAudio{}, &model.ShortplayJob{}, &model.Resource{},
		&model.UserStorageGrant{}, &model.UserStorageState{}); err != nil {
		t.Fatal(err)
	}
	for _, record := range []any{
		&model.User{ID: "owner", Username: "owner", Email: "owner@example.invalid", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.User{ID: "other", Username: "other", Email: "other@example.invalid", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "owner", UserID: "owner", TokenHash: auth.HashToken("owner-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.AuthSession{ID: "other", UserID: "other", TokenHash: auth.HashToken("other-token"), ExpiresAt: time.Now().Add(time.Hour)},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	created, err := svc.CreateShortplay("owner", shortplay.CreateShortplayRequest{
		Name: "雨夜回眸", AspectRatio: "9:16", Chapters: []shortplay.EpisodeInput{{Title: "一", Text: "沈青禾撑伞站在巷口。"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), svc)
	call := func(method, path, body, actor string, want int) map[string]any {
		t.Helper()
		req := httptest.NewRequest(method, "/api"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if actor != "" {
			req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: actor + "." + actor + "-token"})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s actor=%q: got %d want %d: %s", method, path, actor, w.Code, want, w.Body.String())
		}
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if want == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("%s %s: %v: %s", method, path, err, w.Body.String())
			}
		}
		return envelope.Data
	}

	body := `{"kind":"character","name":"沈青禾","aliases":["青禾"],"description":"二十岁女性","appearance":"齐肩黑发","voice":"青年女声"}`
	createdAsset := call(http.MethodPost, "/shortplays/"+created.ProjectID+"/assets", body, "owner", http.StatusOK)
	if createdAsset["id"] != float64(1) || createdAsset["kind"] != "character" {
		t.Fatalf("created asset = %#v", createdAsset)
	}
	listed := call(http.MethodGet, "/shortplays/"+created.ShortplayID+"/assets", "", "owner", http.StatusOK)
	assets, _ := listed["assets"].([]any)
	if len(assets) != 1 {
		t.Fatalf("assets = %#v", listed)
	}

	updated := call(http.MethodPost, "/shortplays/"+created.ProjectID+"/assets",
		`{"assetId":1,"aliases":["沈姑娘"],"locked":true}`, "owner", http.StatusOK)
	aliases, _ := updated["aliases"].([]any)
	if len(aliases) != 2 || updated["locked"] != true || updated["description"] != "二十岁女性" {
		t.Fatalf("updated asset = %#v", updated)
	}
	if updated["revision"] != float64(1) {
		t.Fatalf("revision = %#v", updated["revision"])
	}

	first := call(http.MethodPost, "/shortplays/"+created.ProjectID+"/extract", "", "owner", http.StatusOK)
	if first["queued"] != true || first["jobId"] == "" {
		t.Fatalf("first extract = %#v", first)
	}
	second := call(http.MethodPost, "/shortplays/"+created.ProjectID+"/extract", "", "owner", http.StatusOK)
	if second["queued"] != false || second["jobId"] != first["jobId"] {
		t.Fatalf("second extract = %#v, first = %#v", second, first)
	}

	call(http.MethodGet, "/shortplays/"+created.ProjectID+"/assets", "", "other", http.StatusNotFound)
	call(http.MethodPost, "/shortplays/"+created.ProjectID+"/assets", `{"kind":"vehicle","name":"马车"}`, "owner", http.StatusBadRequest)
	call(http.MethodPost, "/shortplays/"+created.ProjectID+"/assets", `{"assetId":99,"description":"不存在"}`, "owner", http.StatusNotFound)
}
