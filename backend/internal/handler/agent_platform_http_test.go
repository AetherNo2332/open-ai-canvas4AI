package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/auth"
	"yingce/backend/internal/database"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
	"yingce/backend/internal/service"
)

func TestPlatformAgentHTTPContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("i", 48))
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	db := agentSDKOpenDatabase(t)
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"平台回复"}}]}`))
	}))
	defer upstream.Close()
	seedFourCapabilityChannels(t, db, upstream.URL)
	for _, row := range []any{
		&model.SystemSetting{Key: "inspiration_feed", ValueJSON: `{"enabled":false}`},
		&model.User{ID: "platform-user", Username: "platform-user", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "platform-session", UserID: "platform-user", TokenHash: auth.HashToken("platform-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: "platform-user", AvailableMicrocredits: 100_000_000},
		&model.User{ID: "platform-other", Username: "platform-other", Role: model.UserRoleUser, Status: model.UserStatusActive},
		&model.AuthSession{ID: "platform-other-session", UserID: "platform-other", TokenHash: auth.HashToken("platform-other-token"), ExpiresAt: time.Now().Add(time.Hour)},
		&model.CreditAccount{UserID: "platform-other", AvailableMicrocredits: 100_000_000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	svc := service.New(repository.New(db), t.TempDir())
	previous := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() {
		runtimeService = previous
		_ = svc.Close()
	})
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), svc)
	server := httptest.NewServer(router)
	defer server.Close()

	call := func(cookie string, method, path string, body any) (int, map[string]any) {
		t.Helper()
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, server.URL+"/api"+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: cookie})
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var envelope map[string]any
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, envelope
	}

	const cookie = "platform-session.platform-token"
	status, envelope := call(cookie, http.MethodGet, "/agent/capabilities?scopeType=platform&scopeId=self", nil)
	if status != http.StatusOK || envelope["code"] != float64(0) {
		t.Fatalf("platform capabilities: %d %+v", status, envelope)
	}
	data := envelope["data"].(map[string]any)
	if data["scope"].(map[string]any)["type"] != "platform" || data["scope"].(map[string]any)["id"] != "self" ||
		data["agentDefinitionId"] != "platform.default" || data["skills"] != false || data["writeTools"] != false {
		t.Fatalf("platform capabilities leaked canvas contract: %+v", data)
	}
	if tools, _ := data["tools"].([]any); len(tools) != 3 || !containsAll(tools, "ask_user", "finish_run", "plan_update") {
		t.Fatalf("platform tools: %+v", data["tools"])
	}

	request := map[string]any{
		"scope": map[string]any{"type": "platform", "id": "self"}, "canvasId": "",
		"prompt": "你好，平台首句", "channelId": "sdk-text", "channelModelKey": "text-e2e",
		"permissionMode": "read_only", "idempotencyKey": "platform-http-first",
		"budget": map[string]any{"maxCredits": 20},
	}
	status, envelope = call(cookie, http.MethodPost, "/agent/runs", request)
	if status != http.StatusOK || envelope["code"] != float64(0) {
		t.Fatalf("platform first turn: %d %+v", status, envelope)
	}
	run := envelope["data"].(map[string]any)["run"].(map[string]any)
	runID, _ := run["id"].(string)
	if runID == "" || run["scope"].(map[string]any)["id"] != "self" || run["canvasId"] != "" || run["agentDefinitionId"] != "platform.default" {
		t.Fatalf("platform receipt: %+v", run)
	}
	status, replay := call(cookie, http.MethodPost, "/agent/runs", request)
	if status != http.StatusOK || replay["data"].(map[string]any)["run"].(map[string]any)["id"] != runID {
		t.Fatalf("platform replay: %d %+v", status, replay)
	}
	status, envelope = call(cookie, http.MethodGet, "/agent/runs/"+runID+"?scopeType=platform&scopeId=self", nil)
	if status != http.StatusOK || envelope["data"].(map[string]any)["run"].(map[string]any)["id"] != runID {
		t.Fatalf("platform run read: %d %+v", status, envelope)
	}
	status, envelope = call(cookie, http.MethodGet, "/agent/conversations?scopeType=platform&scopeId=self", nil)
	if status != http.StatusOK || len(envelope["data"].(map[string]any)["items"].([]any)) != 1 {
		t.Fatalf("platform directory: %d %+v", status, envelope)
	}

	const otherCookie = "platform-other-session.platform-other-token"
	status, _ = call(otherCookie, http.MethodGet, "/agent/runs/"+runID+"?scopeType=platform&scopeId=self", nil)
	if status != http.StatusNotFound {
		t.Fatalf("platform run crossed accounts: %d", status)
	}
	status, envelope = call(cookie, http.MethodPost, "/agent/runs?scopeType=platform&scopeId=other", request)
	if status != http.StatusBadRequest || envelope["reason"] != "agent_scope_invalid" {
		t.Fatalf("platform scope conflict: %d %+v", status, envelope)
	}
	write := map[string]any{}
	for key, value := range request {
		write[key] = value
	}
	write["permissionMode"] = "auto"
	write["idempotencyKey"] = "platform-http-write-denied"
	status, envelope = call(cookie, http.MethodPost, "/agent/runs", write)
	if status != http.StatusBadRequest {
		t.Fatalf("platform write was admitted: %d %+v", status, envelope)
	}
}

func containsAll(values []any, expected ...string) bool {
	found := map[string]bool{}
	for _, value := range values {
		if name, ok := value.(string); ok {
			found[name] = true
		}
	}
	for _, name := range expected {
		if !found[name] {
			return false
		}
	}
	return true
}
