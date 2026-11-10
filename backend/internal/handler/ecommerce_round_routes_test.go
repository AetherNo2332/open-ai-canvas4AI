package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/auth"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestEcommerceRoundRoutesConvertListAndUpdate(t *testing.T) {
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
	if err := db.AutoMigrate(&model.User{}, &model.AuthSession{}, &model.SystemSetting{}, &model.Task{}, &model.EcommerceProject{}, &model.EcommerceRound{}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{
		&model.User{ID: "round-owner", Username: "round-owner", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "round-session", UserID: "round-owner", TokenHash: auth.HashToken("round-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.SystemSetting{Key: "feature_availability", ValueJSON: `{"ecommerceEnabled":true}`},
		&model.Task{ID: "old-task", UserID: "round-owner", Type: "canvas_image", Status: "succeeded"},
		&model.EcommerceProject{ID: "round-project", UserID: "round-owner", Name: "legacy", DraftJSON: "{}", PlanJSON: `{"previousImages":[{"id":"old-image","taskId":"old-task","prompt":"old"}]}`, Revision: 1},
		&model.EcommerceProject{ID: "foreign-project", UserID: "other-user", Name: "foreign", DraftJSON: "{}", PlanJSON: "{}", Revision: 1},
	} {
		if err := db.Create(value).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	t.Cleanup(func() { _ = svc.Close() })
	router := gin.New()
	RegisterEcommerceRoutes(router.Group("/api"), svc)
	call := func(method, path, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api/ecommerce/projects/"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "round-session.round-token"})
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != status {
			t.Fatalf("%s %s status=%d, want=%d: %s", method, path, out.Code, status, out.Body.String())
		}
		return out
	}

	call("GET", "round-project/rounds?before=-1", "", http.StatusBadRequest)
	call("GET", "round-project/rounds?limit=invalid", "", http.StatusBadRequest)
	call("GET", "foreign-project/rounds", "", http.StatusNotFound)
	call("POST", "foreign-project/rounds-convert", `{"revision":1}`, http.StatusNotFound)
	call("POST", "round-project/rounds-convert", `{"revision":0}`, http.StatusBadRequest)
	converted := call("POST", "round-project/rounds-convert", `{"revision":1}`, http.StatusOK)
	if !strings.Contains(converted.Body.String(), `"createdRounds":1`) {
		t.Fatalf("conversion response=%s", converted.Body.String())
	}
	listed := call("GET", "round-project/rounds", "", http.StatusOK)
	if listed.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("history cache-control=%q", listed.Header().Get("Cache-Control"))
	}
	var page struct {
		Data struct {
			Rounds []struct {
				ID      string              `json:"id"`
				Version int64               `json:"version"`
				Plan    model.EcommercePlan `json:"plan"`
			} `json:"rounds"`
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Data.Total != 1 || len(page.Data.Rounds) != 1 || len(page.Data.Rounds[0].Plan.Images) != 1 {
		t.Fatalf("history response=%s", listed.Body.String())
	}
	round := page.Data.Rounds[0]
	if round.Version != 1 || round.Plan.Images[0].RoundID != round.ID {
		t.Fatalf("round version=%d images=%+v", round.Version, round.Plan.Images)
	}
	imagePath := "round-project/rounds/" + round.ID + "/images/old-image"
	updated := call("PATCH", imagePath, `{"version":1,"image":{"id":"old-image","taskId":"old-task","prompt":"updated"}}`, http.StatusOK)
	if !strings.Contains(updated.Body.String(), `"version":2`) || !strings.Contains(updated.Body.String(), `"prompt":"updated"`) {
		t.Fatalf("update response=%s", updated.Body.String())
	}
	call("PATCH", imagePath, `{"version":1,"image":{"id":"old-image","taskId":"old-task","prompt":"stale"}}`, http.StatusConflict)
	call("PATCH", "foreign-project/rounds/"+round.ID+"/images/old-image", `{"version":2,"remove":true}`, http.StatusNotFound)
	removed := call("PATCH", imagePath, `{"version":2,"remove":true}`, http.StatusOK)
	if !strings.Contains(removed.Body.String(), `"imageCount":0`) || !strings.Contains(removed.Body.String(), `"version":3`) {
		t.Fatalf("remove response=%s", removed.Body.String())
	}
	call("PUT", "round-project", fmt.Sprintf(`{"name":%q}`, strings.Repeat("x", 4<<20)), http.StatusRequestEntityTooLarge)
}
