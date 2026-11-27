package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func TestAgentSDKCanvasScopeCompatibility(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	legacy := agentTestRequest()
	first, err := s.CreateCloudAgentRun("user", legacy, "")
	if err != nil {
		t.Fatal(err)
	}
	// Equivalent old and SDK payloads must share the idempotency identity.
	scoped := legacy
	scoped.Scope = &AgentBusinessScope{Type: "canvas", ID: legacy.CanvasID}
	replayed, err := s.CreateCloudAgentRun("user", scoped, "")
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("SDK retry created another run: %+v %v", replayed, err)
	}
	payload, _ := json.Marshal(replayed)
	var view struct {
		ContractVersion string             `json:"contractVersion"`
		Scope           AgentBusinessScope `json:"scope"`
	}
	if err := json.Unmarshal(payload, &view); err != nil || view.Scope != *scoped.Scope || view.ContractVersion != AgentSDKContractVersion {
		t.Fatalf("missing SDK receipt identity: %s %v", payload, err)
	}
	for _, scope := range []AgentBusinessScope{{Type: "unknown", ID: "agent-canvas"}, {Type: "canvas", ID: "other"}} {
		scoped.Scope = &scope
		scoped.IdempotencyKey = "agent-sdk-invalid-" + scope.Type
		if _, err := s.CreateCloudAgentRun("user", scoped, ""); err == nil {
			t.Fatalf("accepted invalid scope: %+v", scope)
		}
	}
	if _, err := s.CreateCloudAgentRun("other", legacy, ""); err == nil {
		t.Fatal("accepted cross-user canvas")
	}
	changed := legacy
	changed.Prompt = "different"
	_, err = s.CreateCloudAgentRun("user", changed, "")
	appErr, ok := err.(*kernel.AppError)
	if !ok || string(appErr.Reason) != "agent_idempotency_conflict" {
		t.Fatalf("unstable conflict reason: %#v", err)
	}
	var count int64
	db.Model(&model.CloudAgentExecution{}).Count(&count)
	if count != 1 {
		t.Fatalf("rejected requests produced runs: %d", count)
	}
}

func TestAgentSDKSharedContractFixtures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "agent", "contracts", "agent-sdk-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ContractVersion string `json:"contractVersion"`
		Cases           []struct {
			Name     string             `json:"name"`
			Request  CloudAgentRequest  `json:"request"`
			Accepted bool               `json:"accepted"`
			Scope    AgentBusinessScope `json:"scope"`
			Reason   string             `json:"reason"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ContractVersion != AgentSDKContractVersion {
		t.Fatal("Go/TS contract version drift")
	}
	for _, item := range fixture.Cases {
		t.Run(item.Name, func(t *testing.T) {
			err := validateCloudAgentRequest(&item.Request)
			if item.Accepted {
				if err != nil || agentBusinessScope(item.Request) != item.Scope {
					t.Fatalf("contract rejected: %v", err)
				}
			} else if appErr, ok := err.(*kernel.AppError); !ok || string(appErr.Reason) != item.Reason {
				t.Fatalf("wrong machine reason: %#v", err)
			}
		})
	}
}

func TestAgentSDKDramaUsesRealProjectWithoutCanvas(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	for _, row := range []any{
		&model.Project{ID: "drama-project", UserID: "user", Name: "SDK drama", Status: model.ProjectStatusActive},
		&model.DramaProduction{ProjectID: "drama-project", UserID: "user", Status: "paused", Stage: "script"},
		&model.DramaEpisode{ID: "episode-1", ProjectID: "drama-project", Title: "第一集", Synopsis: "SDK_EPISODE_TEXT"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := agentTestRequest()
	req.Scope = &AgentBusinessScope{Type: "drama", ID: "drama-project"}
	req.CanvasID, req.ContextScope = "", nil
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	if run.CanvasID != "" || run.Scope != *req.Scope {
		t.Fatalf("drama faked a canvas: %+v", run)
	}
	if _, err := s.ChangeCloudAgentPermission("user", run.ID, "auto", "drama-no-escalation"); err == nil {
		t.Fatal("drama advisor escalated its business permissions")
	}
	var task model.Task
	if err := db.First(&task, "id = ?", run.ID).Error; err != nil || task.ProjectID != req.Scope.ID {
		t.Fatalf("task lost business ownership: %+v %v", task, err)
	}
	stored, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cloudAgentReadTool(s.repo, "user", &state, piAgentTestCall("read-own-task", "task_get", `{"taskId":"`+run.ID+`"}`)); err != nil {
		t.Fatalf("drama could not read its existing platform task: %v", err)
	}
	for _, name := range cloudAgentToolNames(state.Canonical.Tools) {
		if strings.HasPrefix(name, "canvas_") || name == "generate_media" || name == "director" {
			t.Fatalf("canvas tool leaked into drama: %s", name)
		}
	}
	if !containsString(cloudAgentToolNames(state.Canonical.Tools), "drama_read_episode") {
		t.Fatalf("drama catalog lost its named read tool: %v", cloudAgentToolNames(state.Canonical.Tools))
	}
	call := piAgentTestCall("read-episode", "drama_read_episode", `{"episodeId":"episode-1"}`)
	result, err := cloudAgentReadTool(s.repo, "user", &state, call, s)
	raw, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(raw), "SDK_EPISODE_TEXT") || !strings.Contains(string(raw), `"adopted":false`) {
		t.Fatalf("missing real proposal input: %s %v", raw, err)
	}
	if _, err := s.CreateCloudAgentRun("other", req, ""); err == nil {
		t.Fatal("cross-user drama admitted")
	}
	var canvases int64
	db.Model(&model.CanvasProject{}).Count(&canvases)
	if canvases != 0 {
		t.Fatalf("drama created fake canvas rows: %d", canvases)
	}
	t.Setenv("CANVAS_AGENT_SDK_BUSINESS_DISABLED", "true")
	if replay, err := s.CreateCloudAgentRun("user", req, ""); err != nil || replay.ID != run.ID {
		t.Fatalf("paused admission broke an already accepted request replay: %+v %v", replay, err)
	}
	req.IdempotencyKey = "drama-disabled-admission"
	if _, err := s.CreateCloudAgentRun("user", req, ""); err == nil {
		t.Fatal("paused new business admission still accepted")
	}
	if _, err := s.CloudAgentRun("user", run.ID); err != nil {
		t.Fatalf("admission rollback hid existing records: %v", err)
	}
}

func TestAgentSDKCorruptLegacyAccountRuleDoesNotBlockIsolatedRun(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateUserAgentSystemPrompt("user", AgentSystemPromptRequest{SystemPrompt: "valid rule"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AgentProfile{}).Where("user_id = ?", "user").Update("content", strings.Repeat("x", agentSystemPromptMaxRunes+1)).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCloudAgentRun("user", agentTestRequest(), "")
	if err != nil {
		t.Fatal(err)
	}
	_, state := reloadPiRun(t, s, run.ID)
	if strings.Contains(state.Canonical.SystemPrompt, strings.Repeat("x", 100)) {
		t.Fatal("corrupt legacy source reached the isolated model prompt")
	}
}

func TestAgentSDKFreezesRuleRevisionAndIsolatesConversations(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`})
	rules, err := s.UpdateAgentWorkspace("user", "agent-canvas", 0, "FIRST_RULE")
	if err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	first, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAgentWorkspace("user", "agent-canvas", rules.Revision, "SECOND_RULE"); err != nil {
		t.Fatal(err)
	}
	req.IdempotencyKey = "agent-sdk-rule-next"
	second, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.CloudAgentRun("user", first.ID)
	if err != nil || len(old.Rules) != 1 || old.Rules[0].Source != "canvas" || old.Rules[0].Text != "FIRST_RULE" || old.Rules[0].Revision != 1 {
		t.Fatalf("running rules drifted: %+v %v", old, err)
	}
	if len(second.Rules) != 1 || second.Rules[0].Text != "SECOND_RULE" || second.Rules[0].Revision != 2 || second.ConversationID == old.ConversationID {
		t.Fatalf("sessions or rule versions were mixed: %+v", second)
	}
}
