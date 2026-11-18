package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestAgentContextIsolation(t *testing.T) {
	s, db := agentRunFixture(t)
	for _, row := range []any{
		&model.CanvasProject{ID: "context-canvas-b", UserID: "user", PayloadJSON: `{"nodes":[]}`},
		&model.Project{ID: "context-drama", UserID: "user", Name: "Context drama", Status: model.ProjectStatusActive},
		&model.DramaProduction{ProjectID: "context-drama", UserID: "user", Status: "paused", Stage: "script"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{SystemPrompt: "LEGACY_ACCOUNT_PROMPT_MARKER"}); err != nil {
		t.Fatal(err)
	}
	doc, err := s.AgentMemoryDocument("user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentMemoryDocument("user", AgentMemoryDocUpdateRequest{
		Revision: doc.Revision,
		Entries:  []AgentMemoryEntryInput{{Section: model.AgentMemorySectionNotes, Text: "LEGACY_ACCOUNT_MEMORY_MARKER"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentSharedPreferences("user", AgentSharedPreferences{Language: "ja-JP", Tone: "professional", Verbosity: "concise", Format: "bullets"}); err != nil {
		t.Fatal(err)
	}
	firstScope := AgentBusinessScope{Type: "canvas", ID: "agent-canvas"}
	firstReq := agentTestRequest()
	firstReq.Scope = &firstScope
	firstReq.Prompt = "CANVAS_A_HISTORY_MARKER"
	firstReq.PermissionMode = "auto"
	enableScopeMemoryForTest(t, s)
	first, err := s.CreateCloudAgentRun("user", firstReq, "")
	if err != nil {
		t.Fatal(err)
	}
	_, firstState := startPiAgentFirstStep(t, s, first.ID)
	if _, err := s.cloudAgentHandleMemoryUpdate(s.repo, "user", first.ID, firstState,
		piAgentTestCall("add-scope-memory", "memory_update", `{"operations":[{"action":"add","section":"notes","text":"CANVAS_A_MEMORY_MARKER"}],"reason":"用户要求保留本画布记录"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelCloudAgent(context.Background(), "user", first.ID); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []AgentBusinessScope{
		{Type: "canvas", ID: "agent-canvas"},
		{Type: "canvas", ID: "context-canvas-b"},
		{Type: "drama", ID: "context-drama"},
	} {
		t.Run(scope.ID, func(t *testing.T) {
			req := agentTestRequest()
			req.IdempotencyKey = scope.ID
			req.Scope = &scope
			req.CanvasID, req.ContextScope = scope.ID, nil
			if scope.Type == "drama" {
				req.CanvasID = ""
			}
			run, err := s.CreateCloudAgentRun("user", req, "")
			if err != nil {
				t.Fatal(err)
			}
			payload := agentContextModelInput(t, s, run.ID)
			for _, marker := range []string{"LEGACY_ACCOUNT_PROMPT_MARKER", "LEGACY_ACCOUNT_MEMORY_MARKER", "CANVAS_A_HISTORY_MARKER"} {
				if strings.Contains(payload, marker) {
					t.Errorf("model task contains %s", marker)
				}
			}
			for _, preference := range []string{"ja-JP", "professional", "concise", "bullets"} {
				if !strings.Contains(payload, preference) {
					t.Errorf("model task lacks shared preference %s", preference)
				}
			}
			if strings.Contains(payload, "CANVAS_A_MEMORY_MARKER") != (scope == firstScope) {
				t.Error("model task did not respect the business memory boundary")
			}
			if err := s.CancelCloudAgent(context.Background(), "user", run.ID); err != nil {
				t.Fatal(err)
			}
		})
	}
	s.agentDefinitions = nil
	firstReq.Prompt, firstReq.IdempotencyKey = "new run with default policy", "agent-context-default-off"
	off, err := s.CreateCloudAgentRun("user", firstReq, "")
	if err != nil {
		t.Fatal(err)
	}
	if payload := agentContextModelInput(t, s, off.ID); strings.Contains(payload, "CANVAS_A_MEMORY_MARKER") || strings.Contains(payload, `"name":"memory_update"`) {
		t.Fatal("default policy injected or exposed scoped memory")
	}
}

func enableScopeMemoryForTest(t *testing.T, s *Service) {
	t.Helper()
	var definitions []AgentDefinition
	for _, scopeType := range []string{"canvas", "drama"} {
		definition, err := s.ResolveAgentDefinition(scopeType, "")
		if err != nil {
			t.Fatal(err)
		}
		definition.MemoryPolicy = "scope"
		definitions = append(definitions, definition)
	}
	registry, err := newAgentDefinitionRegistry(definitions)
	if err != nil {
		t.Fatal(err)
	}
	s.agentDefinitions = registry
}

func agentContextModelInput(t *testing.T, s *Service, runID string) string {
	t.Helper()
	_, state := startPiAgentFirstStep(t, s, runID)
	task, err := s.repo.TaskForUser("user", state.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	return task.InputJSON
}

func TestAgentPreferenceSnapshot(t *testing.T) {
	s, db := agentRunFixture(t)
	req := agentTestRequest()
	first, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	firstPrefs := AgentSharedPreferences{Language: "zh-CN", Tone: "casual"}
	if _, err := s.UpdateAgentSharedPreferences("user", firstPrefs); err != nil {
		t.Fatal(err)
	}
	req.IdempotencyKey = "agent-context-zh"
	second, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentSharedPreferences("user", AgentSharedPreferences{Revision: 1, Language: "ja-JP"}); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil || replayed.ID != second.ID {
		t.Fatalf("retry lost the original run: %+v %v", replayed, err)
	}
	req.IdempotencyKey = "agent-context-ja"
	third, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		runID string
		want  AgentSharedPreferences
	}{
		{first.ID, AgentSharedPreferences{}},
		{second.ID, AgentSharedPreferences{Revision: 1, Language: "zh-CN", Tone: "casual"}},
		{third.ID, AgentSharedPreferences{Revision: 2, Language: "ja-JP"}},
	} {
		_, state := reloadPiRun(t, s, test.runID)
		if state.Context.Preferences != test.want {
			t.Fatalf("persisted revision drifted for %s: %+v", test.runID, state.Context.Preferences)
		}
		if test.want.Language != "" && !strings.Contains(state.Canonical.SystemPrompt, test.want.Language) {
			t.Fatal("frozen preference not in the actual model system prompt")
		}
	}
	// An old run can be read and replayed, but its potentially contaminated
	// conversation must not become a new isolated run.
	completePiRunWithAssistantForTest(t, s, first.ID, "old assistant history")
	req.IdempotencyKey = "agent-context-fresh-child"
	child, err := s.CreateCloudAgentRun("user", req, first.ID)
	if err != nil || child.ConversationID != first.ConversationID {
		t.Fatalf("isolated continuation lost the conversation: %+v %v", child, err)
	}
	_, childState := reloadPiRun(t, s, child.ID)
	if childState.Context.Preferences.Revision != 2 {
		t.Fatal("new turn did not load the current preference revision")
	}
	stored, err := s.repo.CloudAgent("user", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stored.StateJSON), &original); err != nil {
		t.Fatal(err)
	}
	delete(original, "context")
	legacyJSON, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(stored).Update("state_json", string(legacyJSON)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.CloudAgentRun("user", first.ID); err != nil {
		t.Fatalf("legacy history unavailable: %v", err)
	}
	req.IdempotencyKey = "agent-context-legacy-child"
	if _, err := s.CreateCloudAgentRun("user", req, first.ID); err == nil || !strings.Contains(err.Error(), "上下文合同") {
		t.Fatalf("legacy history did not receive a clear continuation rejection: %v", err)
	}
}
