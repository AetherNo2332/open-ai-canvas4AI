package app

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
	"infinite-canvas/backend/internal/model"
)

func workspaceService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	s, db := newRunSkillSelectionService(t)
	if err := db.AutoMigrate(&model.CanvasProject{}, &model.AgentWorkspace{}, &model.AgentWorkspaceSkill{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasProject{ID: "workspace-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	return s, db
}

func TestAgentWorkspaceDocumentCASOwnershipAndFreeze(t *testing.T) {
	s, _ := workspaceService(t)
	initial, err := s.GetAgentWorkspace("user", "workspace-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if initial.Revision != 0 {
		t.Fatalf("initial revision: %d", initial.Revision)
	}
	if _, err := s.GetAgentWorkspace("other", "workspace-canvas"); err == nil {
		t.Fatal("cross-account read allowed")
	}
	saved, err := s.UpdateAgentWorkspace("user", "workspace-canvas", 0, "项目规则")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Revision != 1 || saved.AgentsMDHash != fmt.Sprintf("%x", sha256.Sum256([]byte("项目规则"))) {
		t.Fatalf("invalid saved document: %+v", saved)
	}
	frozen, err := s.FreezeWorkspaceSnapshot("user", "workspace-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentWorkspace("user", "workspace-canvas", 0, "stale"); err == nil {
		t.Fatal("stale update accepted")
	}
	if _, err := s.UpdateAgentWorkspace("user", "workspace-canvas", 1, "changed"); err != nil {
		t.Fatal(err)
	}
	if frozen.AgentsMD != "项目规则" || frozen.Revision != 1 {
		t.Fatal("frozen snapshot changed")
	}
	if _, err := s.UpdateAgentWorkspace("user", "workspace-canvas", 2, strings.Repeat("x", 64*1024+1)); err == nil {
		t.Fatal("oversized document accepted")
	}
}

func TestAgentWorkspaceSkillsValidationAndSources(t *testing.T) {
	s, db := workspaceService(t)
	seedSelectionSkill(t, s, db, "global", 1024, false, 1)
	seedDefault(t, db, "global", 0)
	seedSelectionSkill(t, s, db, "project", 1024, false, 1)
	seedSelectionSkillFor(t, s, db, "other", "private", 1024, true, 1)
	for _, selection := range []SkillSelection{
		{SkillID: "project", SkillVersionID: "missing", Enabled: true},
		{SkillID: "project", SkillVersionID: "global-v1", Enabled: true},
		{SkillID: "private", SkillVersionID: "private-v1", Enabled: true},
	} {
		if _, err := s.ReplaceWorkspaceSkills("user", "workspace-canvas", 0, []SkillSelection{selection}); err == nil {
			t.Fatalf("invalid selection accepted: %+v", selection)
		}
	}
	if _, err := s.ReplaceWorkspaceSkills("user", "workspace-canvas", 0, []SkillSelection{{SkillID: "project", SkillVersionID: "project-v1", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.FreezeWorkspaceSnapshot("user", "workspace-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Skills) != 1 || snapshot.Skills[0].Source != "workspace" || snapshot.Skills[0].Hash == "" {
		t.Fatalf("invalid skill freeze: %+v", snapshot)
	}
	merged, err := s.resolveRunSkillsWithLayers("user", "conv", nil, true, snapshot.Skills, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) != 2 || merged[0].Source != "global" || merged[1].Source != "workspace" {
		t.Fatalf("layers not merged: %+v", merged)
	}
}

func TestAgentWorkspaceRunInheritsDocumentAndNativeSkills(t *testing.T) {
	s, db := agentRunFixture(t)
	selectionDataDir = s.dataDir
	t.Cleanup(func() { selectionDataDir = "" })
	seedSelectionSkill(t, s, db, "project-run", 1024, false, 1)
	if _, err := s.UpdateAgentWorkspace("user", "agent-canvas", 0, "FROZEN_PROJECT_MARKER"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceWorkspaceSkills("user", "agent-canvas", 1, []SkillSelection{{SkillID: "project-run", SkillVersionID: "project-run-v1", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
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
	if state.Workspace == nil || state.Workspace.Revision != 2 || !strings.Contains(state.Canonical.SystemPrompt, "FROZEN_PROJECT_MARKER") {
		t.Fatal("workspace not frozen in real run")
	}
	if len(state.Skills) != 1 || state.Skills[0].Source != "workspace" {
		t.Fatalf("workspace skill missing: %+v", state.Skills)
	}
	if _, err := s.UpdateAgentWorkspace("user", "agent-canvas", 2, "CHANGED_PROJECT_MARKER"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ClaimPiAgent("worker-workspace")
	if err != nil || snapshot == nil {
		t.Fatalf("Pi claim: %v", err)
	}
	if !strings.Contains(snapshot.Canonical.SystemPrompt, "FROZEN_PROJECT_MARKER") || strings.Contains(snapshot.Canonical.SystemPrompt, "CHANGED_PROJECT_MARKER") {
		t.Fatal("workspace prompt drifted")
	}
	page, err := s.PiSkillFile("user", run.ID, "worker-workspace", PiSkillFileRequest{NativeName: snapshot.Skills[0].NativeName, Path: "SKILL.md"})
	if err != nil || !strings.Contains(page.Content, "project-run") {
		t.Fatalf("native workspace read: %v", err)
	}
}

func TestAgentWorkspaceRejectsModelContextOverflowBeforeRun(t *testing.T) {
	s, db := agentRunFixture(t)
	if _, err := s.UpdateAgentWorkspace("user", "agent-canvas", 0, strings.Repeat("规则", 9000)); err != nil {
		t.Fatal(err)
	}
	config := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), "text-test")
	config.Text.ContextWindowTokens = 8192
	if err := db.Model(&model.ChannelModel{}).Where("id = ?", "cm").Update("capability_config_json", mustEncodeModelCapabilityConfig(t, config)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCloudAgentRun("user", agentTestRequest(), ""); err == nil {
		t.Fatal("model context overflow accepted")
	}
	var count int64
	if err := db.Model(&model.CloudAgentExecution{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected workspace context left a Run")
	}
}
