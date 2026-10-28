package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

func realtimeHTTPFixture(t *testing.T) (*httptest.Server, *service.Service, func()) {
	_, svc, db := workspaceHTTPFixture(t)
	if err := db.AutoMigrate(&model.CanvasDocumentEpoch{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{&model.CanvasProject{ID: "deleted", UserID: "workspace-user", PayloadJSON: `{"nodes":[]}`}, &model.CanvasDocumentEpoch{CanvasID: "deleted", OwnerID: "workspace-user", Epoch: "deleted-epoch", Deleted: true}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	r.GET("/api/canvas-projects/:id/realtime", func(c *gin.Context) { serveCanvasRealtime(c, svc, 25*time.Millisecond) })
	server := httptest.NewServer(r)
	t.Cleanup(func() { svc.Close(); server.Close() })
	return server, svc, func() {
		if err := db.Delete(&model.AuthSession{}, "id = ?", "workspace-session").Error; err != nil {
			t.Error(err)
		}
	}
}
func dialRealtime(t *testing.T, server *httptest.Server, path, origin string, authenticated bool) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	headers := http.Header{}
	headers.Set("Origin", origin)
	if authenticated {
		headers.Set("Cookie", service.SessionCookieName+"=workspace-session.token")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/api/canvas-projects/"+path+"/realtime", &websocket.DialOptions{HTTPHeader: headers, CompressionMode: websocket.CompressionDisabled})
	if conn != nil {
		conn.SetReadLimit(512 << 10)
		t.Cleanup(func() { conn.CloseNow() })
	}
	return conn, response, err
}
func readRealtimeType(t *testing.T, c *websocket.Conn, want string) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		_, raw, err := c.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var msg map[string]any
		if err = json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg["type"] == want {
			return msg
		}
	}
}
func TestRealtimeWebSocketAuthOriginProtocolAndRevocation(t *testing.T) {
	server, _, revoke := realtimeHTTPFixture(t)
	for _, tc := range []struct {
		path, origin string
		auth         bool
		status       int
	}{{"own", server.URL, false, 401}, {"foreign", server.URL, true, 404}, {"missing", server.URL, true, 404}, {"deleted", server.URL, true, 404}, {"own", "https://evil.invalid", true, 403}, {"own", "", true, 403}} {
		conn, response, err := dialRealtime(t, server, tc.path, tc.origin, tc.auth)
		if conn != nil || err == nil || response == nil || response.StatusCode != tc.status {
			t.Fatalf("handshake %+v: %v %v", tc, response, err)
		}
	}
	conn, _, err := dialRealtime(t, server, "own", server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	ready := readRealtimeType(t, conn, "ready")
	if ready["protocolVersion"] != float64(1) || ready["coordination"] != "local" || ready["deviceNumber"].(float64) < 1 {
		t.Fatalf("ready: %+v", ready)
	}
	readRealtimeType(t, conn, "sync")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"presence","protocolVersion":1,"cursor":{"x":1,"y":2},"drag":[],"deviceLabel":"HTTP test"}`)); err != nil {
		t.Fatal(err)
	}
	for {
		peers := readRealtimeType(t, conn, "peers")
		list := peers["peers"].([]any)
		if len(list) == 1 && list[0].(map[string]any)["deviceLabel"] == "HTTP test" {
			break
		}
	}
	revoke()
	for {
		_, _, err = conn.Read(ctx)
		if err != nil {
			break
		}
	}
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("revocation: %v", err)
	}
}
func TestRealtimeWebSocketInvalidFramesAndShutdown(t *testing.T) {
	server, svc, _ := realtimeHTTPFixture(t)
	for _, tc := range []struct {
		name string
		kind websocket.MessageType
		raw  string
	}{{"version", websocket.MessageText, `{"type":"heartbeat"}`}, {"binary", websocket.MessageBinary, `{}`}, {"large", websocket.MessageText, strings.Repeat("a", 32769)}} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, err := dialRealtime(t, server, "own", server.URL, true)
			if err != nil {
				t.Fatal(err)
			}
			readRealtimeType(t, c, "ready")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err = c.Write(ctx, tc.kind, []byte(tc.raw)); err != nil {
				t.Fatal(err)
			}
			for {
				_, _, err = c.Read(ctx)
				if err != nil {
					break
				}
			}
			want := websocket.StatusPolicyViolation
			if tc.name == "large" {
				want = websocket.StatusMessageTooBig
			}
			if websocket.CloseStatus(err) != want {
				t.Fatalf("close: %v", err)
			}
		})
	}
	c, _, err := dialRealtime(t, server, "own", server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	readRealtimeType(t, c, "ready")
	if err = svc.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		_, _, err = c.Read(ctx)
		if err != nil {
			break
		}
	}
	if websocket.CloseStatus(err) != websocket.StatusGoingAway {
		t.Fatalf("shutdown: %v", err)
	}
}
