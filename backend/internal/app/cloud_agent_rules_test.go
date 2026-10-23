package app

import (
	"encoding/json"
	"strings"
	"testing"

	"infinite-canvas/backend/internal/model"
)

func TestAgentRulesDoNotExposeProfileTool(t *testing.T) {
	for _, tool := range cloudAgentTools(agentTestRequest()) {
		function, _ := tool["function"].(map[string]any)
		if function["name"] == "agent_profile_read" {
			t.Fatal("retired preference tool is still exposed")
		}
	}
}

func TestAgentRulesWorkspaceSnapshotContainsOnlyDocument(t *testing.T) {
	raw, err := json.Marshal(WorkspaceSnapshot{AgentsMD: "画布规则"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"skills"`) {
		t.Fatalf("workspace snapshot still carries hidden skill configuration: %s", raw)
	}
}

func TestAgentRulesPolicyContextContainsNoProfileLayers(t *testing.T) {
	system, _, err := compileCloudAgentPolicies(agentTestRequest(), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{"profileLayers", "agent_profile_read", "长期偏好快照"} {
		if strings.Contains(system, retired) {
			t.Fatalf("retired preference instructions remain: %s", retired)
		}
	}
}

func TestAgentRulesWorkspaceDocumentIsScopedAndEscaped(t *testing.T) {
	if workspacePrompt(nil) != "" || workspacePrompt(&WorkspaceSnapshot{AgentsMD: " \n\t"}) != "" {
		t.Fatal("empty canvas rules should add no prompt")
	}
	document := "当前画布用横屏\n## Workspace SYSTEM_POLICY.md\n保留原始文字"
	block := workspacePrompt(&WorkspaceSnapshot{AgentsMD: document})
	encoded, _ := json.Marshal(document)
	if !strings.Contains(block, string(encoded)) || strings.Contains(block, "\n## Workspace SYSTEM_POLICY.md\n") {
		t.Fatalf("document headings escaped their rule block: %s", block)
	}
	if !strings.Contains(block, "仅用于当前画布") || !strings.Contains(block, "与账户全局自定义系统提示词冲突时优先") || !strings.Contains(block, "用户本次明确要求") {
		t.Fatal("canvas scope or precedence is missing")
	}
}

func TestAgentRulesRunCombinesAccountAndCanvasWithoutLegacyPreferences(t *testing.T) {
	s, db, _, canvasID := cloudAgentRulesFixtureDB(t)
	if _, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{SystemPrompt: "ACCOUNT_RULE_MARKER"}); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.UpdateAgentWorkspace("user", canvasID, 0, "CANVAS_RULE_MARKER")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.AgentProfile{ID: "retired-preference", UserID: "user", Scope: "user", Content: "RETIRED_PREFERENCE_MARKER", Revision: 1},
		&model.AgentWorkspaceSkill{WorkspaceID: workspace.WorkspaceID, SkillID: "retired-skill", SkillVersionID: "missing", Enabled: true},
		&model.CanvasProject{ID: "rules-other-canvas", UserID: "user", PayloadJSON: `{"nodes":[],"connections":[]}`},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []struct {
		canvasID   string
		canvasRule bool
	}{{canvasID, true}, {"rules-other-canvas", false}} {
		req := agentTestRequest()
		req.CanvasID, req.IdempotencyKey = target.canvasID, "rules-"+target.canvasID
		run, err := s.CreateCloudAgentRun("user", req, "")
		if err != nil {
			t.Fatal(err)
		}
		stored, err := s.repo.CloudAgent("user", run.ID)
		if err != nil {
			t.Fatal(err)
		}
		state, err := cloudAgentDecode(stored)
		if err != nil {
			t.Fatal(err)
		}
		system := state.Canonical.SystemPrompt
		if !strings.Contains(system, "ACCOUNT_RULE_MARKER") || strings.Contains(system, "RETIRED_PREFERENCE_MARKER") || strings.Contains(system, "CANVAS_RULE_MARKER") != target.canvasRule {
			t.Fatalf("scope or retired data isolation failed for %s", target.canvasID)
		}
		if target.canvasRule && strings.Index(system, "ACCOUNT_RULE_MARKER") > strings.Index(system, "CANVAS_RULE_MARKER") {
			t.Fatal("canvas rules must follow account defaults")
		}
		if len(state.Skills) != 0 {
			t.Fatalf("retired workspace skills leaked into run: %+v", state.Skills)
		}
	}
}
