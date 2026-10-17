package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/service"
)

func toolSnapshotHTTP(t *testing.T, router *gin.Engine, method, path, body string, authenticated, internal bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "workspace-session.token"})
	}
	if internal {
		req.Header.Set("Authorization", "Bearer snapshot-test-token")
		req.Header.Set("X-Agent-Protocol-Version", internalAgentWireVersion)
		req.Header.Set("X-Pi-SDK-Version", internalAgentPiSDKVersion)
		req.Header.Set("X-Pi-Session-Format", internalAgentSessionFormat)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func TestToolSnapshotHTTPAdminAuthorizationAndDrainRevision(t *testing.T) {
	_, svc, db := workspaceHTTPFixture(t)
	router := gin.New()
	registerToolSnapshotAdminRoutes(router.Group("/api"), svc)
	path := "/api/admin/agent/tool-snapshot-health"
	if w := toolSnapshotHTTP(t, router, "GET", path, "", false, false); w.Code != 401 {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body)
	}
	if w := toolSnapshotHTTP(t, router, "GET", path, "", true, false); w.Code != 403 {
		t.Fatalf("non-admin: %d %s", w.Code, w.Body)
	}
	if err := db.Model(&model.User{}).Where("id = ?", "workspace-user").Update("role", model.UserRoleAdmin).Error; err != nil {
		t.Fatal(err)
	}
	w := toolSnapshotHTTP(t, router, "GET", path, "", true, false)
	if w.Code != 200 {
		t.Fatalf("health: %d %s", w.Code, w.Body)
	}
	var health struct {
		Data struct {
			Backend struct {
				ID string `json:"id"`
			} `json:"backend"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &health); err != nil || health.Data.Backend.ID == "" {
		t.Fatalf("backend identity: %v %s", err, w.Body)
	}
	w = toolSnapshotHTTP(t, router, "GET", "/api/admin/agent/tool-snapshots", "", true, false)
	if w.Code != 200 || strings.Contains(w.Body.String(), `"parameters"`) {
		t.Fatalf("list must be metadata only: %d %s", w.Code, w.Body)
	}
	w = toolSnapshotHTTP(t, router, "GET", "/api/admin/agent/tool-snapshots/"+health.Data.Backend.ID, "", true, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"parameters"`) {
		t.Fatalf("detail: %d %s", w.Code, w.Body)
	}

	in := service.ToolSnapshotRegistration{InstanceID: "http-worker", BootID: "boot", LoadGeneration: 1, Artifact: service.ToolSnapshotArtifact{SchemaVersion: "test/v1", Tools: []map[string]any{{"type": "function", "function": map[string]any{"name": "test", "parameters": map[string]any{"type": "object"}}}}}}
	if _, err := svc.RegisterToolSnapshot(in); err != nil {
		t.Fatal(err)
	}
	var runtime model.AgentToolRuntime
	if err := db.First(&runtime, "instance_id = ?", in.InstanceID).Error; err != nil {
		t.Fatal(err)
	}
	drainPath := "/api/admin/agent/runtime-instances/http-worker/drain"
	body := fmt.Sprintf(`{"expectedRevision":%d,"draining":true,"reason":"schema upgrade"}`, runtime.Revision)
	w = toolSnapshotHTTP(t, router, "PATCH", drainPath, body, true, false)
	if w.Code != 200 {
		t.Fatalf("drain: %d %s", w.Code, w.Body)
	}
	w = toolSnapshotHTTP(t, router, "PATCH", drainPath, body, true, false)
	if w.Code != 409 {
		t.Fatalf("stale revision: %d %s", w.Code, w.Body)
	}
	var audits int64
	if err := db.Model(&model.AdminAuditEvent{}).Where("action = ?", "agent_tool_runtime.drain").Count(&audits).Error; err != nil || audits != 1 {
		t.Fatalf("successful drain audited exactly once: %d %v", audits, err)
	}
}

func TestToolSnapshotHTTPRegistrationAndLostIdentity(t *testing.T) {
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", "snapshot-test-token")
	_, svc, _ := workspaceHTTPFixture(t)
	router := gin.New()
	RegisterInternalAgentRoutes(router, svc)
	path := "/internal-agent/tool-snapshots/register"
	body := `{"instanceId":"http-process","bootId":"boot","loadGeneration":1,"artifact":{"schemaVersion":"test/v1","tools":[{"type":"function","function":{"name":"test","parameters":{"type":"object"}}}]}}`
	if w := toolSnapshotHTTP(t, router, "POST", path, body, false, false); w.Code != 401 {
		t.Fatalf("internal auth: %d %s", w.Code, w.Body)
	}
	w := toolSnapshotHTTP(t, router, "POST", path, body, false, true)
	if w.Code != 200 {
		t.Fatalf("registration: %d %s", w.Code, w.Body)
	}
	var result struct {
		Data struct {
			SnapshotID string `json:"snapshotId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Data.SnapshotID == "" {
		t.Fatalf("registration ID: %v %s", err, w.Body)
	}
	wrong := fmt.Sprintf(`{"owner":"slot","instanceId":"http-process","bootId":"wrong","snapshotId":%q,"loadGeneration":1}`, result.Data.SnapshotID)
	w = toolSnapshotHTTP(t, router, "POST", "/internal-agent/claim", wrong, false, true)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "tool_snapshot_registration_required") {
		t.Fatalf("lost boot must re-register even with empty queue: %d %s", w.Code, w.Body)
	}
	invalid := `{"instanceId":"invalid-process","bootId":"boot","loadGeneration":1,"loadError":"Invalid tool schema","artifact":{"schemaVersion":"unavailable","tools":[]}}`
	w = toolSnapshotHTTP(t, router, "POST", path, invalid, false, true)
	if w.Code != 200 {
		t.Fatalf("invalid load must remain observable: %d %s", w.Code, w.Body)
	}
}
