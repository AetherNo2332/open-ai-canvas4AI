package handler

import (
	"net/http"
	"strings"
	"testing"
)

func TestAgentMessageQueueHTTPStrictSettingsAndOwnership(t *testing.T) {
	call, _, _ := workspaceHTTPFixture(t)
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/agent/ui-settings", ""},
		{"PATCH", "/api/agent/ui-settings", `{"revision":0,"defaultSendMode":"queue"}`},
		{"GET", "/api/agent/conversations/foreign/queue", ""},
		{"POST", "/api/agent/conversations/foreign/messages", `{}`},
	} {
		if w := call(route.method, route.path, route.body, false); w.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: %d", route.path, w.Code)
		}
	}
	for _, body := range []string{`null`, `{}`, `{"defaultSendMode":"queue"}`, `{"revision":0}`, `{"revision":0,"defaultSendMode":"queue","userId":"other"}`, `{"revision":0,"defaultSendMode":"queue"} {}`} {
		if w := call("PATCH", "/api/agent/ui-settings", body, true); w.Code != 400 {
			t.Fatalf("invalid settings accepted: %s => %d", body, w.Code)
		}
	}
	if w := call("PATCH", "/api/agent/ui-settings", `{"revision":0,"defaultSendMode":"queue"}`, true); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/api/agent/ui-settings", "", true); w.Code != 200 || !strings.Contains(w.Body.String(), `"defaultSendMode":"queue"`) {
		t.Fatalf("read: %d %s", w.Code, w.Body.String())
	}
	if w := call("PATCH", "/api/agent/ui-settings", `{"revision":0,"defaultSendMode":"interject"}`, true); w.Code != 409 {
		t.Fatalf("CAS: %d %s", w.Code, w.Body.String())
	}
	if w := call("GET", "/api/agent/conversations/foreign/queue", "", true); w.Code != 404 {
		t.Fatalf("foreign queue: %d %s", w.Code, w.Body.String())
	}
	if w := call("POST", "/api/agent/conversations/foreign/messages", `{"mode":"queue","expectedRunId":"x","request":{"surprise":true}}`, true); w.Code != 400 {
		t.Fatalf("unknown nested field: %d", w.Code)
	}
}
