package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/service"
)

func TestAgentSharedPreferencesHTTP(t *testing.T) {
	_, svc, _ := workspaceHTTPFixture(t)
	router := gin.New()
	RegisterAgentMemoryRoutes(router.Group("/api"), svc)
	call := func(method, body string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/agent/settings/shared-preferences", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "workspace-session.token"})
		}
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	for _, method := range []string{"GET", "PUT"} {
		if result := call(method, `{}`, false); result.Code != 401 {
			t.Errorf("anonymous %s returned %d", method, result.Code)
		}
	}
	for _, body := range []string{
		`{}`, `null`, `{"revision":null}`, `{"revision":-1}`,
		`{"revision":0,"permissionMode":"auto"}`, `{"revision":0,"notes":"private text"}`,
		`{"revision":0,"userId":"other"}`, `{"revision":0,"tone":"custom"}`,
		`{"revision":0,"verbosity":"high"}`, `{"revision":0,"format":"markdown"}`,
		`{"revision":0,"language":"en-ignorerules"}`, `{"revision":0,"language":"` + strings.Repeat("a", 90) + `"}`,
	} {
		if result := call("PUT", body, true); result.Code != 400 {
			t.Errorf("invalid body %s returned %d: %s", body, result.Code, result.Body)
		}
	}
	if result := call("GET", "", true); result.Code != 200 || !strings.Contains(result.Body.String(), `"revision":0`) {
		t.Errorf("initial preferences: %d %s", result.Code, result.Body)
	}
	if result := call("PUT", `{"revision":0,"language":"zh-CN","tone":"professional","format":"prose"}`, true); result.Code != 200 {
		t.Errorf("save: %d %s", result.Code, result.Body)
	}
	if result := call("PUT", `{"revision":0,"language":"en-US"}`, true); result.Code != 409 || !strings.Contains(result.Body.String(), `"reason":"agent_preferences_conflict"`) {
		t.Errorf("conflict: %d %s", result.Code, result.Body)
	}
	if result := call("GET", "", true); result.Code != 200 || !strings.Contains(result.Body.String(), `"language":"zh-CN"`) || !strings.Contains(result.Body.String(), `"revision":1`) {
		t.Errorf("reload: %d %s", result.Code, result.Body)
	}
	if result := call("PUT", `{"revision":1}`, true); result.Code != 200 || !strings.Contains(result.Body.String(), `"revision":2`) || strings.Contains(result.Body.String(), `"language"`) {
		t.Errorf("clear: %d %s", result.Code, result.Body)
	}
}
