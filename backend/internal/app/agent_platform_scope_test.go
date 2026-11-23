package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func platformAgentTestRequest() CloudAgentRequest {
	req := agentTestRequest()
	req.CanvasID = ""
	req.Scope = &AgentBusinessScope{Type: AgentPlatformBusinessType, ID: AgentPlatformSelfScopeID}
	req.ContextScope = nil
	req.FocusNodeIDs = nil
	req.PermissionMode = "read_only"
	req.IdempotencyKey = "platform-agent-test-key"
	return req
}

func TestPlatformScopeIsAccountOwnedAndPubliclyProjected(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	req := platformAgentTestRequest()
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	if run.Scope != (AgentBusinessScope{Type: AgentPlatformBusinessType, ID: AgentPlatformSelfScopeID}) || run.CanvasID != "" || run.AgentDefinitionID != "platform.default" {
		t.Fatalf("platform receipt leaked internal identity or canvas: %+v", run)
	}
	stored, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(stored)
	if err != nil {
		t.Fatal(err)
	}
	if state.Request.Scope == nil || *state.Request.Scope != (AgentBusinessScope{Type: AgentPlatformBusinessType, ID: "user"}) {
		t.Fatalf("platform scope was not normalized internally: %+v", state.Request.Scope)
	}
	if state.Workspace != nil || state.Capabilities == nil || state.Capabilities.BusinessType != AgentPlatformBusinessType {
		t.Fatalf("platform inherited canvas state: workspace=%v capabilities=%+v", state.Workspace, state.Capabilities)
	}
	if len(state.Skills) != 0 {
		t.Fatalf("platform received default skills: %+v", state.Skills)
	}
	if strings.Contains(state.Canonical.SystemPrompt, "节点能力速查") || strings.Contains(state.Canonical.SystemPrompt, "ArrowFX Cloud Agent 媒体策略") ||
		strings.Contains(state.Canonical.SystemPrompt, "使用 read 的 drama:episode") || !strings.Contains(state.Canonical.SystemPrompt, "普通创作平台对话") {
		t.Fatalf("platform policy was not isolated:\n%s", state.Canonical.SystemPrompt)
	}
	names := cloudAgentToolNames(state.Canonical.Tools)
	if len(names) != 3 || !slices.Contains(names, "ask_user") || !slices.Contains(names, "finish_run") || !slices.Contains(names, "plan_update") {
		t.Fatalf("platform tools = %v", names)
	}
	var canvases int64
	db.Model(&model.CanvasProject{}).Count(&canvases)
	if canvases != 0 {
		t.Fatalf("platform created canvas rows: %d", canvases)
	}
	replay, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil || replay.ID != run.ID {
		t.Fatalf("platform idempotent replay changed: %+v %v", replay, err)
	}
	page, err := s.ScopedCloudAgentConversations("user", AgentConversationSelector{Scope: AgentBusinessScope{Type: AgentPlatformBusinessType, ID: AgentPlatformSelfScopeID}}, "", 30)
	if err != nil || len(page.Items) != 1 || page.Items[0].Scope != run.Scope {
		t.Fatalf("platform directory = %+v %v", page, err)
	}
	if err := s.RequireAgentTargetScope("user", run.ID, false, AgentConversationSelector{Scope: AgentBusinessScope{Type: AgentPlatformBusinessType, ID: AgentPlatformSelfScopeID}}); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformScopedCapabilitiesAndPermissionBoundary(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	view, err := s.AgentCapabilities("user", AgentBusinessScope{Type: AgentPlatformBusinessType, ID: AgentPlatformSelfScopeID}, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Scope != (AgentBusinessScope{Type: AgentPlatformBusinessType, ID: AgentPlatformSelfScopeID}) || view.AgentDefinitionID != "platform.default" ||
		strings.Join(view.PermissionModes, ",") != "read_only" || view.Skills || view.WriteTools || view.MemoryPolicy != "off" ||
		len(view.Tools) != 3 || !slices.Contains(view.Tools, "ask_user") || !slices.Contains(view.Tools, "finish_run") || !slices.Contains(view.Tools, "plan_update") {
		t.Fatalf("wrong platform capabilities: %+v", view)
	}
	run, err := s.CreateCloudAgentRun("user", platformAgentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.authorizePlatformAgent("other", CloudAgentRequest{Scope: &AgentBusinessScope{Type: AgentPlatformBusinessType, ID: "user"}}); err == nil {
		t.Fatal("platform object scope accepted another account")
	}
	if _, err := s.ChangeCloudAgentPermission("user", run.ID, "auto", "platform-escalation"); err == nil {
		t.Fatal("platform escalated from read-only")
	}
	write := platformAgentTestRequest()
	write.PermissionMode = "auto"
	write.IdempotencyKey = "platform-auto-denied"
	if _, err := s.CreateCloudAgentRun("user", write, ""); err == nil {
		t.Fatal("platform auto permission was admitted")
	}
	skill := platformAgentTestRequest()
	skill.SkillIDs = []string{"skill"}
	skill.IdempotencyKey = "platform-skill-denied"
	if _, err := s.CreateCloudAgentRun("user", skill, ""); err == nil {
		t.Fatal("platform account skill was admitted")
	}
}

func TestPlatformModelRequestCarriesGeneralPolicyAndControlTools(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("REDIS_URL", "")
	captured := make(chan map[string]any, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		captured <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"平台回复"}}]}`))
	}))
	defer upstream.Close()

	s, db, _, _ := creationTestService(t)
	s = New(s.repo, s.dataDir)
	t.Cleanup(func() { _ = s.Close() })
	if err := db.Model(&model.ModelChannel{}).Where("id = ?", "channel").
		Updates(map[string]any{"base_url": upstream.URL, "api_key": "test-only"}).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCloudAgentRun("user", platformAgentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPiAgent("platform-worker"); err != nil {
		t.Fatal(err)
	}
	leased, state := reloadPiRun(t, s, run.ID)
	framework, _ := piFirstStepRequest(state)
	step, err := s.PiModelStep("user", run.ID, leased.LeaseOwner, framework)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessNextTask(); err != nil {
		t.Fatal(err)
	}
	var task model.Task
	if err := db.First(&task, "id = ?", step.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskStatusSucceeded {
		t.Fatalf("platform model task failed: %s %s", task.Status, task.Error)
	}
	body := <-captured
	system, _ := json.Marshal(body["messages"])
	if !strings.Contains(string(system), "普通创作平台对话") ||
		strings.Contains(string(system), "节点能力速查") ||
		strings.Contains(string(system), "ArrowFX Cloud Agent 媒体策略") ||
		strings.Contains(string(system), "使用 read 的 drama:episode") {
		t.Fatalf("platform model request policy was not isolated: %s", system)
	}
	var names []string
	for _, raw := range creationMaps(body["tools"]) {
		function, _ := raw["function"].(map[string]any)
		if name, _ := function["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"ask_user", "finish_run", "plan_update"}) {
		t.Fatalf("platform model tools = %v", names)
	}
}
