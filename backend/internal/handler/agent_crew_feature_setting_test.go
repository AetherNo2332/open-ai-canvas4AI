package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/service"
)

func TestCrewFeaturePatchPreservesOtherSettingsAndEnforcesAdmin(t *testing.T) {
	crewCall, svc, db := workspaceHTTPFixture(t)
	view, err := svc.FeatureAvailability()
	if err != nil {
		t.Fatal(err)
	}
	initial := view.FeatureAvailability
	initial.WelcomeEnabled = false
	initial.ShortDramaEnabled = false
	initial.CustomChannelsEnabled = false
	initial.AgentCrewEnabled = false
	encoded, _ := json.Marshal(initial)
	if err := db.Model(&model.SystemSetting{}).Where("key = ?", "feature_availability").Update("value_json", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterFeatureAvailabilityRoutes(router.Group("/api"), svc)
	request := func(method, body string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/admin/settings/features", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if authenticated {
			r.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "workspace-session.token"})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := request("PATCH", `{"agentCrewEnabled":true}`, false); w.Code != 401 {
		t.Fatalf("anonymous change: %d", w.Code)
	}
	if w := request("PATCH", `{"agentCrewEnabled":true}`, true); w.Code != 403 {
		t.Fatalf("non-admin change: %d", w.Code)
	}
	if err := db.Model(&model.User{}).Where("id = ?", "workspace-user").Update("role", model.UserRoleAdmin).Error; err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false, true} {
		payload, _ := json.Marshal(map[string]bool{"agentCrewEnabled": enabled})
		if w := request("PATCH", string(payload), true); w.Code != 200 {
			t.Fatalf("admin change: %d %s", w.Code, w.Body.String())
		}
		w := request("GET", "", true)
		var result struct {
			Data struct {
				Features service.FeatureAvailability `json:"features"`
			} `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("read saved setting: %d", w.Code)
		}
		want := initial
		want.AgentCrewEnabled = enabled
		if result.Data.Features != want {
			t.Fatalf("unrelated settings changed: got %+v want %+v", result.Data.Features, want)
		}
		status := http.StatusForbidden
		if enabled {
			status = http.StatusOK
		}
		if w := crewCall("GET", "own/crews", "", true); w.Code != status {
			t.Fatalf("Crew gate: enabled=%v status=%d", enabled, w.Code)
		}
	}
}
