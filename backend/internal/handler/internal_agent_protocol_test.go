package handler

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

func TestInternalAgentRequiresVersionedPiWireIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("s", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "wire-test-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	router := gin.New()
	_, svc, _ := workspaceHTTPFixture(t)
	RegisterInternalAgentRoutes(router, svc)

	request := func(body string, headers map[string]string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/internal-agent/claim", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("w", 48))
		req.Header.Set("X-Agent-Identity", "wire-test-worker")
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		router.ServeHTTP(recorder, req)
		return recorder
	}
	if got := request(`{"owner":"worker"}`, map[string]string{"Authorization": "Bearer " + strings.Repeat("s", 48)}); got.Code != http.StatusUnauthorized {
		t.Fatalf("control-plane signer accepted as worker: %d", got.Code)
	}
	if got := request(`{"owner":"worker"}`, map[string]string{"X-Agent-Identity": "other-worker"}); got.Code != http.StatusUnauthorized {
		t.Fatalf("worker identity mismatch accepted: %d", got.Code)
	}

	if got := request(`{"owner":"worker"}`, nil); got.Code != http.StatusUpgradeRequired || !strings.Contains(got.Body.String(), "agent_protocol_mismatch") {
		t.Fatalf("missing protocol identity status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(`{"owner":"worker"}`, map[string]string{
		"X-Agent-Protocol-Version": internalAgentWireVersion,
		"X-Pi-SDK-Version":         "0.86.0",
		"X-Pi-Session-Format":      internalAgentSessionFormat,
	}); got.Code != http.StatusUpgradeRequired {
		t.Fatalf("wrong SDK version status=%d body=%s", got.Code, got.Body.String())
	}
	if got := request(`{"owner":""}`, map[string]string{
		"X-Agent-Protocol-Version": internalAgentWireVersion,
		"X-Pi-SDK-Version":         internalAgentPiSDKVersion,
		"X-Pi-Session-Format":      internalAgentSessionFormat,
	}); got.Code != http.StatusBadRequest {
		t.Fatalf("supported protocol identity did not pass middleware: status=%d body=%s", got.Code, got.Body.String())
	}
	validHeaders := map[string]string{
		"X-Agent-Protocol-Version": internalAgentWireVersion,
		"X-Pi-SDK-Version":         internalAgentPiSDKVersion,
		"X-Pi-Session-Format":      internalAgentSessionFormat,
	}
	for _, owner := range []string{"other-worker:slot", "wire-test-worker:slot:extra", "wire-test-worker:"} {
		if got := request(`{"owner":"`+owner+`"}`, validHeaders); got.Code != http.StatusForbidden {
			t.Fatalf("claim accepted unauthorized owner %q: %d", owner, got.Code)
		}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/internal-agent/capacity", strings.NewReader(`{"owner":"`+owner+`","active":0,"capacity":1}`))
		req.Header.Set("Authorization", "Bearer "+strings.Repeat("w", 48))
		req.Header.Set("X-Agent-Identity", "wire-test-worker")
		for name, value := range validHeaders {
			req.Header.Set(name, value)
		}
		router.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("capacity accepted unauthorized owner %q: %d", owner, recorder.Code)
		}
	}
}

func TestInternalAgentEventsHideForeignRunAndTaskIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("s", 48))
	t.Setenv("CANVAS_AGENT_WORKER_ID", "scoped-worker")
	t.Setenv("CANVAS_AGENT_WORKER_TOKEN", strings.Repeat("w", 48))
	t.Setenv("CANVAS_AGENT_WORKER_USER_ID", "tenant-a")
	_, svc, db := workspaceHTTPFixture(t)
	events := []model.AgentWakeEvent{
		{Sequence: 1, UserID: "tenant-b", RunID: "foreign-run", TaskID: "foreign-task", Kind: "tool_changed"},
		{Sequence: 2, UserID: "tenant-a", RunID: "own-run", TaskID: "own-task", Kind: "tool_changed"},
		{Sequence: 3, TaskID: "unscoped-foreign-task", Kind: "task_changed"},
		{Sequence: 4, Kind: "scheduler_config_changed"},
	}
	if err := db.Create(&events).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterInternalAgentRoutes(router, svc)
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/internal-agent/events?after=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.Repeat("w", 48))
	req.Header.Set("X-Agent-Identity", "scoped-worker")
	req.Header.Set("X-Agent-Protocol-Version", internalAgentWireVersion)
	req.Header.Set("X-Pi-SDK-Version", internalAgentPiSDKVersion)
	req.Header.Set("X-Pi-Session-Format", internalAgentSessionFormat)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("events status: %d", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	var ids, payloads []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			ids = append(ids, strings.TrimPrefix(line, "id: "))
		}
		if strings.HasPrefix(line, "data: ") {
			payloads = append(payloads, strings.TrimPrefix(line, "data: "))
		}
		if len(payloads) == 2 {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids, ",") != "2,4" || len(payloads) != 2 {
		t.Fatalf("scoped SSE ids=%v data=%v", ids, payloads)
	}
	if !strings.Contains(payloads[0], "own-run") || !strings.Contains(payloads[0], "own-task") ||
		strings.Contains(strings.Join(payloads, " "), "foreign-run") || strings.Contains(strings.Join(payloads, " "), "foreign-task") {
		t.Fatalf("scoped SSE leaked or omitted identifiers: %v", payloads)
	}
}

func TestInternalAgentProtocolIdentityMatchesSharedArtifact(t *testing.T) {
	data, err := os.ReadFile("../../../agent/harness/PI_WIRE_IDENTITY.json")
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		ProtocolVersion      string `json:"protocolVersion"`
		PiSDKVersion         string `json:"piSdkVersion"`
		SessionFormatVersion string `json:"sessionFormatVersion"`
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.ProtocolVersion != internalAgentWireVersion ||
		identity.PiSDKVersion != internalAgentPiSDKVersion ||
		identity.SessionFormatVersion != internalAgentSessionFormat {
		t.Fatalf("Go protocol identity diverged from shared artifact: %#v", identity)
	}
}

func TestInternalAgentContextCompactionRoutesAreMounted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("s", 48))
	router := gin.New()
	RegisterInternalAgentRoutes(router, &service.Service{})
	want := map[string]bool{
		"POST /internal-agent/runs/:id/context-compactions":                       false,
		"GET /internal-agent/runs/:id/context-compactions/:operationId":           false,
		"POST /internal-agent/runs/:id/context-compactions/:operationId/commit":   false,
		"POST /internal-agent/runs/:id/context-compactions/:operationId/model":    false,
		"POST /internal-agent/runs/:id/context-compactions/:operationId/complete": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, mounted := range want {
		if !mounted {
			t.Errorf("Pi context compaction route is missing: %s", route)
		}
	}
}

func TestInternalAgentNativeSkillReadRouteIsMounted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", strings.Repeat("s", 48))
	router := gin.New()
	RegisterInternalAgentRoutes(router, &service.Service{})
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet && route.Path == "/internal-agent/runs/:id/skills/:nativeName/file" {
			return
		}
	}
	t.Fatal("native Skill read route is missing")
}
