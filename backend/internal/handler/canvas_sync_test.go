package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

func TestCanvasEditProofHeaderBoundsAndCanvasScopedDuplicates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(canvasEditProofMiddleware)
	router.POST("/write", func(c *gin.Context) { ok(c, nil) })
	proof := service.CanvasEditProof{CanvasID: "one", NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)}
	second := proof
	second.CanvasID = "two"
	cases := []struct {
		name, header string
		status       int
	}{
		{"cross-canvas", syncTestJSON(t, []service.CanvasEditProof{proof, second}), 200},
		{"duplicate", syncTestJSON(t, []service.CanvasEditProof{proof, proof}), 400},
		{"oversize", strings.Repeat("a", 32769), 400},
		{"too-many", syncTestJSON(t, make([]service.CanvasEditProof, 129)), 400},
		{"tail", syncTestJSON(t, []service.CanvasEditProof{proof}) + " {}", 400},
		{"null", "null", 400},
		{"unknown", `[{"canvasId":"one","nodeId":"text","sessionId":"tab","token":"` + strings.Repeat("a", 48) + `","unknown":true}]`, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/write", nil)
			r.Header.Set(service.CanvasEditProofHeader, tc.header)
			router.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCanvasLeaseHTTPAuthenticationAndLegacyProofHeader(t *testing.T) {
	_, svc, db := workspaceHTTPFixture(t)
	previousRuntime := runtimeService
	ConfigureRuntime(svc)
	t.Cleanup(func() { ConfigureRuntime(previousRuntime) })
	if err := db.AutoMigrate(&model.CanvasDocumentEpoch{}, &model.CanvasEntityVersion{}, &model.CanvasCommandReceipt{}, &model.CanvasCommandEvent{}, &model.CanvasActorHistory{}, &model.CanvasCommandResource{}, &model.CanvasCreateReceipt{}); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterCanvasAPI(router.Group("/api"), svc)
	call := func(method, path, body, proofHeader string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if proofHeader != "" {
			r.Header.Set(service.CanvasEditProofHeader, proofHeader)
		}
		if authenticated {
			r.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: "workspace-session.token"})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	raw := `{"id":"http-lease","revision":0,"title":"Lease","referenceFieldsVersion":1,"nodes":[{"id":"text","type":"text","metadata":{"content":"original"}}],"connections":[]}`
	if _, err := svc.UpsertUserCanvasProject("workspace-user", json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	body := syncTestJSON(t, service.CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)})
	path := "/canvas-projects/http-lease"
	if w := call("POST", path+"/edit-leases/acquire", body, "", false); w.Code != 401 {
		t.Fatalf("auth: %d %s", w.Code, w.Body)
	}
	if w := call("POST", "/canvas-projects/foreign/edit-leases/acquire", body, "", true); w.Code != 404 {
		t.Fatalf("ownership: %d %s", w.Code, w.Body)
	}
	granted := call("POST", path+"/edit-leases/acquire", body, "", true)
	var grant struct {
		Data service.CanvasEditLeaseGrant `json:"data"`
	}
	if err := json.Unmarshal(granted.Body.Bytes(), &grant); err != nil || granted.Code != 200 || granted.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("grant: %d %s %v", granted.Code, granted.Body, err)
	}
	state := call("GET", path+"/sync-status", "", "", true)
	if state.Code != 200 || strings.Contains(state.Body.String(), "token") || strings.Contains(state.Body.String(), grant.Data.Token) {
		t.Fatalf("unsafe state: %d %s", state.Code, state.Body)
	}
	raw = strings.Replace(raw, `"revision":0`, `"revision":1`, 1)
	raw = strings.Replace(raw, "original", "legacy", 1)
	saveBody := `{"project":` + raw + `}`
	if w := call("PUT", path, saveBody, "", true); w.Code != 409 || !strings.Contains(w.Body.String(), "node_locked") {
		t.Fatalf("legacy unproved: %d %s", w.Code, w.Body)
	}
	header := syncTestJSON(t, []service.CanvasEditProof{grant.Data.CanvasEditProof})
	if w := call("PUT", path, saveBody, header, true); w.Code != 200 {
		t.Fatalf("legacy proved: %d %s", w.Code, w.Body)
	}
}
func syncTestJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
