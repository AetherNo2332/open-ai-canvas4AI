package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"infinite-canvas/backend/internal/service"
)

func TestInternalAgentRequiresVersionedPiWireIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", "wire-test-token")
	router := gin.New()
	RegisterInternalAgentRoutes(router, &service.Service{})

	request := func(body string, headers map[string]string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/internal-agent/claim", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer wire-test-token")
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		router.ServeHTTP(recorder, req)
		return recorder
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
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", "wire-test-token")
	router := gin.New()
	RegisterInternalAgentRoutes(router, &service.Service{})
	want := map[string]bool{
		"POST /internal-agent/runs/:id/context-compactions":                     false,
		"GET /internal-agent/runs/:id/context-compactions/:operationId":         false,
		"POST /internal-agent/runs/:id/context-compactions/:operationId/commit": false,
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
	t.Setenv("CANVAS_AGENT_INTERNAL_TOKEN", "wire-test-token")
	router := gin.New()
	RegisterInternalAgentRoutes(router, &service.Service{})
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet && route.Path == "/internal-agent/runs/:id/skills/:nativeName/file" {
			return
		}
	}
	t.Fatal("native Skill read route is missing")
}
