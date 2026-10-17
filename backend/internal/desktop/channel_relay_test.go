package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/app"
	"infinite-canvas/backend/internal/auth"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChannelDTOHasNoSecrets(t *testing.T) {
	dir := t.TempDir()
	db, err := database.Open(database.Config{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	if err = database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateDesktop(db); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	_, user, err := repo.BootstrapDesktopOwner(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.NewDesktop(repo, dir, StaticSecretProvider(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err = svc.SetDesktopPrivateHosts(&user, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	channel, err := svc.CreateSystemChannel(&user, app.ChannelRequest{Name: "stub", BaseURL: "http://127.0.0.1:31415/v1", APIKey: "private-api-fixture", SecretKey: "private-secret-fixture", Headers: []app.OutboundHeader{{Name: "X-Model-Tenant", Value: "private-header-fixture"}}, Models: []string{"fixture-model"}})
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := svc.DesktopChannels(&user)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(summaries)
	var stored model.ModelChannel
	if err = db.First(&stored, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	disk := []byte(stored.APIKey + stored.SecretKey + stored.HeadersJSON)
	for _, secret := range []string{"private-api-fixture", "private-secret-fixture", "private-header-fixture"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("read DTO leaked secret")
		}
		if strings.Contains(string(disk), secret) {
			t.Fatal("database stored secret in plaintext")
		}
	}
	loaded, err := svc.SystemChannel(channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKey != "private-api-fixture" || !strings.Contains(loaded.HeadersJSON, "private-header-fixture") {
		t.Fatal("host failed to decrypt")
	}
	restarted, err := app.NewDesktop(repo, dir, StaticSecretProvider(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err = restarted.ValidateChannelOutboundURL("http://127.0.0.1:31415/v1"); err != nil {
		t.Fatal("opt-in did not survive restart")
	}
	if order, err := svc.ReserveProxyBilling(user.ID, channel.ID, "fixture-model", "text", "test", "op", 1); err != nil || order != nil {
		t.Fatalf("desktop tried to charge website credits: %v", err)
	}
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		if r.Header.Get("Authorization") != "Bearer private-api-fixture" || r.Header.Get("X-Model-Tenant") != "private-header-fixture" {
			t.Error("persisted credentials were not used")
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected destination: %s", r.URL.Path)
		}
		var payload map[string]any
		json.NewDecoder(r.Body).Decode(&payload)
		if payload["model"] != "fixture-model" {
			t.Error("caller model override accepted")
		}
		if payload["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"text\":\"private-api-fixture\"}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"stub answer private-api-fixture"}}]}`)
	}))
	defer upstream.Close()
	if _, err = svc.UpdateSystemChannel(&user, channel.ID, app.ChannelRequest{BaseURL: upstream.URL + "/v1"}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	configured, err := svc.SaveDesktopChannelModel(&user, channel.ID, summaries[0].Models[0].ID, app.ChannelModelRequest{ModelKey: "fixture-model", Capability: "text", Protocol: "chat-completion", Enabled: &enabled, CapabilityConfig: app.DefaultModelCapabilityConfig("chat-completion")})
	if err != nil {
		t.Fatal(err)
	}
	session, err := svc.IssueLocalSession(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouter(svc, RouterConfig{Owner: identity.Principal{ID: user.ID}, WebDir: t.TempDir()})
	request := func(body string, header bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/desktop/relay", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session.Session})
		if header {
			req.Header.Set("X-Canvas-Upstream-URL", "http://evil.example")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	normal := fmt.Sprintf(`{"channelId":%q,"modelId":%q,"operation":"chat.completions","payload":{"model":"evil-model","messages":[{"role":"user","content":"hello"}]}}`, channel.ID, configured.ID)
	for _, body := range []string{normal, strings.Replace(normal, `"payload":{`, `"payload":{"stream":true,`, 1)} {
		w := request(body, false)
		if w.Code != 200 || strings.Contains(w.Body.String(), "private-api-fixture") {
			t.Fatalf("relay failed or leaked credential: status %d", w.Code)
		}
	}
	if upstreamCalls != 2 {
		t.Fatalf("upstream calls=%d", upstreamCalls)
	}
	for _, body := range []string{strings.TrimSuffix(normal, "}") + `,"url":"http://evil.example"}`, strings.TrimSuffix(normal, "}") + `,"headers":{"Authorization":"evil"}}`} {
		if w := request(body, false); w.Code != 400 {
			t.Fatalf("caller URL/header accepted: %d", w.Code)
		}
	}
	if w := request(normal, true); w.Code != 400 {
		t.Fatal("caller upstream header accepted")
	}
	if upstreamCalls != 2 {
		t.Fatal("invalid relay reached upstream")
	}
}
