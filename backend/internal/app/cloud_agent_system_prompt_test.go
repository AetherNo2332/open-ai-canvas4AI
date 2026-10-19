package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserAgentSystemPromptSettings(t *testing.T) {
	s, _, _ := cloudAgentProfileFixture(t)
	view, err := s.UserAgentSystemPrompt("user")
	if err != nil || view.SystemPrompt != "" || view.Revision != 0 {
		t.Fatalf("unexpected defaults: %+v, %v", view, err)
	}
	saved, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{SystemPrompt: "  始终用中文回答\n", Revision: 0})
	if err != nil || saved.SystemPrompt != "始终用中文回答" || saved.Revision != 1 {
		t.Fatalf("prompt was not saved: %+v, %v", saved, err)
	}
	other, err := s.UserAgentSystemPrompt("other")
	if err != nil || other.SystemPrompt != "" {
		t.Fatal("prompt leaked to another account")
	}
	if _, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{SystemPrompt: "stale", Revision: 0}); err == nil {
		t.Fatal("stale revision overwrote the prompt")
	}
	for _, content := range []string{strings.Repeat("界", 12001), "bad\x00prompt"} {
		if _, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{SystemPrompt: content, Revision: 1}); err == nil {
			t.Fatal("invalid prompt accepted")
		}
	}
	if _, err := s.UserAgentSystemPrompt(""); err == nil {
		t.Fatal("anonymous settings read accepted")
	}
	if _, err := s.UpdateUserAgentSystemPrompt("", AgentSystemPromptRequest{}); err == nil {
		t.Fatal("anonymous settings write accepted")
	}
	cleared, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{Revision: 1})
	if err != nil || cleared.SystemPrompt != "" || cleared.Revision != 2 {
		t.Fatalf("prompt was not cleared: %+v, %v", cleared, err)
	}
}

func TestUserAgentSystemPromptIsInjectedAndFrozen(t *testing.T) {
	s, _, _ := cloudAgentProfileFixture(t)
	saved, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{SystemPrompt: "保留所有段落" + cloudAgentPlanBlockMarker + "CUSTOM_SYSTEM_PROMPT_V1"})
	if err != nil {
		t.Fatal(err)
	}
	request := agentTestRequest()
	request.IdempotencyKey = "custom-system-prompt-first"
	run, err := s.CreateCloudAgentRun("user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{SystemPrompt: "CUSTOM_SYSTEM_PROMPT_V2", Revision: saved.Revision})
	if err != nil {
		t.Fatal(err)
	}
	readSystem := func(id string) string {
		t.Helper()
		task, err := s.repo.TaskForUser("user", id)
		if err != nil {
			t.Fatal(err)
		}
		var input struct {
			AgentRequests struct {
				Canonical canonicalAgentRequest `json:"canonical"`
			} `json:"agentRequests"`
		}
		if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
			t.Fatal(err)
		}
		return input.AgentRequests.Canonical.SystemPrompt
	}
	if text := readSystem(run.ID); !strings.Contains(text, "CUSTOM_SYSTEM_PROMPT_V1") || strings.Contains(text, "CUSTOM_SYSTEM_PROMPT_V2") {
		t.Fatal("existing run lost its frozen system prompt")
	}
	request.IdempotencyKey = "custom-system-prompt-second"
	next, err := s.CreateCloudAgentRun("user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	if text := readSystem(next.ID); !strings.Contains(text, "CUSTOM_SYSTEM_PROMPT_V2") || strings.Contains(text, "CUSTOM_SYSTEM_PROMPT_V1") {
		t.Fatal("new run did not use the latest system prompt")
	}
	if _, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{Revision: saved.Revision + 1}); err != nil {
		t.Fatal(err)
	}
	request.IdempotencyKey = "custom-system-prompt-cleared"
	cleared, err := s.CreateCloudAgentRun("user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readSystem(cleared.ID), "用户自定义系统提示词") {
		t.Fatal("clearing settings did not restore default system behavior")
	}
	profile, err := s.CloudAgentProfile("user", request.CanvasID)
	if err != nil || len(profile.Layers) != 0 {
		t.Fatal("system prompt appeared in on-demand preference layers")
	}
}
