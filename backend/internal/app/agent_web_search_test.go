package app

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

type tavilyTestTransport func(*http.Request) (*http.Response, error)

func (f tavilyTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAgentWebSearchSettingsSecretAndSwitch(t *testing.T) {
	s, _ := newAgentSkillDefaultsService(t)
	admin := adminSkillDefaultsActor()
	if _, err := s.AdminAgentWebSearchSetting(&model.User{ID: "ordinary", Role: model.UserRoleUser}); err == nil {
		t.Fatal("non-admin read allowed")
	}
	if _, err := s.UpdateAgentWebSearchSetting(&model.User{ID: "ordinary", Role: model.UserRoleUser}, AgentWebSearchSettingRequest{}); err == nil {
		t.Fatal("non-admin write allowed")
	}
	initial, err := s.AdminAgentWebSearchSetting(admin)
	if err != nil || initial.Enabled || initial.HasAPIKey {
		t.Fatalf("default setting: %+v %v", initial, err)
	}
	if _, err = s.UpdateAgentWebSearchSetting(admin, AgentWebSearchSettingRequest{Enabled: true}); err == nil {
		t.Fatal("enabled without key")
	}
	saved, err := s.UpdateAgentWebSearchSetting(admin, AgentWebSearchSettingRequest{Enabled: true, APIKey: "tvly-test-secret", ExpectedRevision: 0})
	if err != nil || !saved.Enabled || !saved.HasAPIKey {
		t.Fatalf("save failed: %+v %v", saved, err)
	}
	row, err := s.repo.SystemSetting(agentWebSearchSettingKey)
	if err != nil || strings.Contains(row.ValueJSON, "tvly-test-secret") || !strings.Contains(row.ValueJSON, encryptedSettingPrefix) {
		t.Fatal("API key not encrypted")
	}
	public, _ := json.Marshal(saved)
	if strings.Contains(string(public), "tvly-test-secret") {
		t.Fatal("API key exposed")
	}
	if _, err = s.UpdateAgentWebSearchSetting(admin, AgentWebSearchSettingRequest{Enabled: false, ExpectedRevision: 0}); err == nil {
		t.Fatal("stale config accepted")
	}
	disabled, err := s.UpdateAgentWebSearchSetting(admin, AgentWebSearchSettingRequest{ExpectedRevision: saved.Revision})
	if err != nil || disabled.Enabled || !disabled.HasAPIKey {
		t.Fatalf("disable lost key: %+v %v", disabled, err)
	}
	if _, err = s.agentWebSearch("news"); err == nil {
		t.Fatal("disabled search was allowed")
	}
	cleared, err := s.UpdateAgentWebSearchSetting(admin, AgentWebSearchSettingRequest{ClearAPIKey: true, ExpectedRevision: disabled.Revision})
	if err != nil || cleared.HasAPIKey {
		t.Fatalf("clear failed: %+v %v", cleared, err)
	}
}

func TestAgentWebSearchExecutionAndDisclosure(t *testing.T) {
	s, _ := newAgentSkillDefaultsService(t)
	saved, err := s.UpdateAgentWebSearchSetting(adminSkillDefaultsActor(), AgentWebSearchSettingRequest{Enabled: true, APIKey: "tvly-test-secret"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.agentWebSearchHTTPClient = &http.Client{Transport: tavilyTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.tavily.com/search" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer tvly-test-secret" {
			t.Fatal("wrong Tavily request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["query"] != "latest film news" || body["search_depth"] != "basic" || body["max_results"] != float64(5) || body["include_raw_content"] != false {
			t.Fatalf("wrong search options: %v", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"query":"latest film news","results":[{"title":"News","url":"https://example.com/news","content":"A source summary"},{"title":"Unsafe","url":"javascript:alert(1)","content":"bad"}]}`)), Header: make(http.Header)}, nil
	})}
	for _, req := range []CloudAgentRequest{{WebSearchEnabled: true, PermissionMode: "read_only"}, {WebSearchEnabled: true, PermissionMode: "read_only", subagent: &SubagentRuntime{Depth: 1}}} {
		if !cloudAgentToolAllowed(req, "web_search") {
			t.Fatal("search missing from parent/child tool catalog")
		}
	}
	if cloudAgentToolAllowed(CloudAgentRequest{}, "web_search") {
		t.Fatal("search exposed when disabled")
	}
	state := &cloudAgentRuntime{}
	state.Request.WebSearchEnabled = true
	var call cloudAgentCall
	call.Function.Name, call.Function.Arguments = "web_search", `{"query":"latest film news"}`
	result, err := cloudAgentReadTool(s.repo, "user", state, call, s)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), "https://example.com/news") || strings.Contains(string(encoded), "javascript:") || strings.Contains(string(encoded), "tvly-test-secret") {
		t.Fatalf("unsafe result: %s", encoded)
	}
	if _, err = s.UpdateAgentWebSearchSetting(adminSkillDefaultsActor(), AgentWebSearchSettingRequest{ExpectedRevision: saved.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = cloudAgentReadTool(s.repo, "user", state, call, s); err == nil || calls != 1 {
		t.Fatal("old run bypassed disabled setting")
	}
}

func TestAgentWebSearchProviderErrorsHideSecrets(t *testing.T) {
	for _, status := range []int{401, 429, 432, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, _ := newAgentSkillDefaultsService(t)
			if _, err := s.UpdateAgentWebSearchSetting(adminSkillDefaultsActor(), AgentWebSearchSettingRequest{Enabled: true, APIKey: "tvly-test-secret"}); err != nil {
				t.Fatal(err)
			}
			s.agentWebSearchHTTPClient = &http.Client{Transport: tavilyTestTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("tvly-test-secret raw error")), Header: make(http.Header)}, nil
			})}
			_, err := s.agentWebSearch("query")
			if err == nil || strings.Contains(err.Error(), "tvly-test-secret") || strings.Contains(err.Error(), "raw error") {
				t.Fatalf("unsafe provider error: %v", err)
			}
		})
	}
}

func TestAgentWebSearchRuntimeDispatchAndLiveDisable(t *testing.T) {
	s, _, args := agentMediaFixture(t)
	saved, err := s.UpdateAgentWebSearchSetting(adminSkillDefaultsActor(), AgentWebSearchSettingRequest{Enabled: true, APIKey: "tvly-runtime-test"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.agentWebSearchHTTPClient = &http.Client{Transport: tavilyTestTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[{"title":"Source","url":"https://example.com/source","content":"Verified summary"}]}`)), Header: make(http.Header)}, nil
	})}
	run, state := agentMediaRun(t, s, args, "auto")
	if !state.Request.WebSearchEnabled {
		t.Fatal("server did not derive search capability")
	}
	call := readOnlyCall("search-1", "web_search")
	call.Function.Arguments = `{"query":"news"}`
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{call})
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, detail := lastAgentToolEvent(t, s, run.ID)
	if calls != 1 || detail["error"] != nil || detail["sources"] == nil {
		t.Fatalf("runtime did not execute search: calls=%d payload=%v", calls, payload)
	}
	if _, err := s.UpdateAgentWebSearchSetting(adminSkillDefaultsActor(), AgentWebSearchSettingRequest{ExpectedRevision: saved.Revision}); err != nil {
		t.Fatal(err)
	}
	run, state = reloadAgentRun(t, s, run.ID)
	state.CallIndex = 0
	call.ID = "search-2"
	run, state = writeBatch(t, s, run, &state, []cloudAgentCall{call})
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	payload, detail = lastAgentToolEvent(t, s, run.ID)
	if calls != 1 || detail["error"] == nil {
		t.Fatalf("disabled old run reused search: calls=%d payload=%v", calls, payload)
	}
}
