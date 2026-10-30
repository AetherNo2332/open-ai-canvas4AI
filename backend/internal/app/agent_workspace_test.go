package app

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
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

func TestAgentWorkspaceIgnoresRetiredSkillConfiguration(t *testing.T) {
	s, db := workspaceService(t)
	seedSelectionSkill(t, s, db, "global", 1024, false, 1)
	seedDefault(t, db, "global", 0)
	view, err := s.GetAgentWorkspace("user", "workspace-canvas")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.AgentWorkspaceSkill{WorkspaceID: view.WorkspaceID, SkillID: "retired-invalid", SkillVersionID: "missing", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.FreezeWorkspaceSnapshot("user", "workspace-canvas"); err != nil {
		t.Fatalf("retired skill configuration blocked the canvas document: %v", err)
	}
	selected, err := s.resolveRunSkills("user", "new-conversation", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Source != "global" || selected[0].ID != "global" {
		t.Fatalf("unexpected skills: %+v", selected)
	}
	baseline := []model.AgentConversationSkill{
		{ConversationID: "existing-conversation", SkillID: "global", SkillVersionID: selected[0].VersionID, ContentHash: selected[0].Hash, Source: "global", Position: 0},
		{ConversationID: "existing-conversation", SkillID: "retired-invalid", SkillVersionID: "missing", Source: "workspace", Position: 1},
	}
	if err := db.Create(&baseline).Error; err != nil {
		t.Fatal(err)
	}
	continued, err := s.resolveRunSkills("user", "existing-conversation", nil, false)
	if err != nil || len(continued) != 1 || continued[0].Source != "global" {
		t.Fatalf("retired conversation baseline was restored: skills=%+v err=%v", continued, err)
	}
}

func TestAgentWorkspaceHistoricalSkillRemainsReadableWithoutInstallation(t *testing.T) {
	s, db := workspaceService(t)
	seedSelectionSkill(t, s, db, "historical-public", 1024, false, 1)
	version, err := s.repo.SkillVersion("historical-public-v1")
	if err != nil {
		t.Fatal(err)
	}
	frozen := cloudAgentSkill{ID: "historical-public", VersionID: version.ID, Hash: version.ContentHash, Source: "workspace"}
	entry, err := s.cloudAgentFrozenSkillFile("user", frozen, "SKILL.md")
	if err != nil || entry == nil || !strings.Contains(entry.Content, "historical-public") {
		t.Fatalf("historical frozen public skill must not require user installation: %v", err)
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
	seedDefault(t, db, "project-run", 0)
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
	if state.Workspace == nil || state.Workspace.Revision != 1 || !strings.Contains(state.Canonical.SystemPrompt, "FROZEN_PROJECT_MARKER") {
		t.Fatal("workspace not frozen in real run")
	}
	if len(state.Skills) != 1 || state.Skills[0].Source != "global" {
		t.Fatalf("global default skill missing: %+v", state.Skills)
	}
	if _, err := s.UpdateAgentWorkspace("user", "agent-canvas", 1, "CHANGED_PROJECT_MARKER"); err != nil {
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
