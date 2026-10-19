package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

func TestAgentSystemPromptHTTP(t *testing.T) {
	_, svc, _ := workspaceHTTPFixture(t)
	router := gin.New()
	RegisterAgentMemoryRoutes(router.Group("/api"), svc)
	call := func(method, body string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/agent/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "workspace-session.token"})
		}
		result := httptest.NewRecorder()
		router.ServeHTTP(result, req)
		return result
	}
	for _, method := range []string{"GET", "PATCH"} {
		if result := call(method, `{}`, false); result.Code != 401 {
			t.Fatalf("anonymous %s: %d", method, result.Code)
		}
	}
	for _, body := range []string{`{}`, `null`, `{"revision":0}`, `{"systemPrompt":"x"}`, `{"revision":0,"systemPrompt":null}`, `{"revision":0,"systemPrompt":"x","userId":"other"}`, `{"revision":0,"systemPrompt":"x"} {}`} {
		if result := call("PATCH", body, true); result.Code != 400 {
			t.Fatalf("invalid body accepted: %s: %d %s", body, result.Code, result.Body)
		}
	}
	if result := call("PATCH", `{"revision":0,"systemPrompt":"始终用中文回答"}`, true); result.Code != 200 {
		t.Fatalf("save: %d %s", result.Code, result.Body)
	}
	if result := call("PATCH", `{"revision":0,"systemPrompt":"stale"}`, true); result.Code != 409 {
		t.Fatalf("conflict: %d %s", result.Code, result.Body)
	}
	if result := call("GET", "", true); result.Code != 200 || !strings.Contains(result.Body.String(), "始终用中文回答") {
		t.Fatalf("read: %d %s", result.Code, result.Body)
	}
	if result := call("PATCH", `{"revision":1,"systemPrompt":""}`, true); result.Code != 200 {
		t.Fatalf("clear: %d %s", result.Code, result.Body)
	}
}
