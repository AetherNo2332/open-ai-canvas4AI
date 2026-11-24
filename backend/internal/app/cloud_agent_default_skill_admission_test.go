package app

import (
	"encoding/json"
	"slices"
	"testing"

	"yingce/backend/internal/model"
)

func TestAgentDefinitionDefaultSkillsAtAdmission(t *testing.T) {
	for _, business := range []string{"canvas", "drama"} {
		t.Run(business, func(t *testing.T) {
			service, db := agentRunFixture(t)
			selectionDataDir = service.dataDir
			t.Cleanup(func() { selectionDataDir = "" })
			for _, id := range []string{"shared-skill", "canvas-skill", "drama-skill", canvasToolsGuideSkillID, previsWorldGuideSkillID} {
				seedSelectionSkill(t, service, db, id, 1024, false, 1)
			}
			var defaults []AgentSkillDefaultItem
			if err := json.Unmarshal([]byte(`[
				{"skillId":"shared-skill","skillVersionId":"shared-skill-v1","position":0,"enabled":1},
				{"skillId":"canvas-skill","skillVersionId":"canvas-skill-v1","position":1,"enabled":1,"scope":"canvas"},
				{"skillId":"drama-skill","skillVersionId":"drama-skill-v1","position":2,"enabled":1,"scope":"drama"}]`), &defaults); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ReplaceAgentSkillDefaults(adminSkillDefaultsActor(), 0, defaults); err != nil {
				t.Fatal(err)
			}
			seedDefault(t, db, canvasToolsGuideSkillID, 3)
			seedDefault(t, db, previsWorldGuideSkillID, 3)

			request := agentTestRequest()
			expected := []string{"shared-skill", business + "-skill"}
			if business == "canvas" {
				expected = append(expected, canvasToolsGuideSkillID, previsWorldGuideSkillID)
			} else {
				for _, row := range []any{
					&model.Project{ID: "skill-test-drama", UserID: "user", Name: "Skill test", Status: model.ProjectStatusActive},
					&model.DramaProduction{ProjectID: "skill-test-drama", UserID: "user", Status: "paused", Stage: "script"},
				} {
					if err := db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
				request.Scope = &AgentBusinessScope{Type: "drama", ID: "skill-test-drama"}
				request.CanvasID, request.ContextScope = "", nil
			}
			run, err := service.CreateCloudAgentRun("user", request, "")
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(run.Skills))
			for _, skill := range run.Skills {
				ids = append(ids, skill.ID)
			}
			if !slices.Equal(ids, expected) {
				t.Fatalf("default skills = %v, want %v", ids, expected)
			}
			summary, err := service.AgentSkillDefaultsForBusiness("user", business, "")
			if err != nil || len(summary.Skills) != len(expected) {
				t.Fatalf("default skill preview = %+v, %v", summary, err)
			}
			snapshot, err := service.ClaimPiAgent("worker-default-skills")
			if err != nil || snapshot == nil || len(snapshot.Skills) != len(expected) {
				t.Fatalf("worker snapshot = %+v, %v", snapshot, err)
			}
			for index, skill := range snapshot.Skills {
				if skill.ID != expected[index] || summary.Skills[index].SkillID != skill.ID || summary.Skills[index].SkillName != skill.DisplayName || run.Skills[index].Instruction != "" {
					t.Fatalf("unexpected skill delivery for %q", skill.ID)
				}
			}
			if _, err := service.PiSkillFile("user", run.ID, "worker-default-skills", PiSkillFileRequest{
				NativeName: snapshot.Skills[0].NativeName, Path: "SKILL.md",
			}); err != nil {
				t.Fatalf("frozen skill cannot be read on demand: %v", err)
			}
			if business == "drama" {
				request.IdempotencyKey, request.SkillIDs = "drama-manual-skill", []string{"canvas-skill"}
				if _, err := service.CreateCloudAgentRun("user", request, ""); err == nil {
					t.Fatal("read-only drama configuration accepted explicit account skills")
				}
			}
		})
	}
}

func TestAgentMessageQueuesUnfrozenManualSkill(t *testing.T) {
	service, db, root := reliableAgentRoot(t)
	selectionDataDir = service.dataDir
	t.Cleanup(func() { selectionDataDir = "" })
	seedSelectionSkillFor(t, service, db, "user", "manual-skill", 1024, true, 1)
	input := queueTestInput(root.ID, "enable-manual-skill", "interject")
	input.Request.SkillIDs = []string{"manual-skill"}
	receipt, err := service.SendAgentConversationMessage("user", root.ID, input)
	if err != nil || receipt.Delivery != "queued" {
		t.Fatalf("unfrozen skill needs a fresh Run: %+v, %v", receipt, err)
	}
	message, err := service.repo.AgentQueuedMessage("user", root.ID, receipt.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateAgentQueuedMessage("user", root.ID, receipt.MessageID, AgentQueuedMessageUpdate{Revision: message.Revision, Action: "interject"}); err == nil {
		t.Fatal("queue promotion dropped the newly selected skill")
	}
	finishConversationTestRun(t, db, root.ID)
	dispatched, err := service.dispatchAgentConversation("user", root.ID)
	if err != nil || !dispatched {
		t.Fatalf("skill request was not dispatched: %v", err)
	}
	queued, err := service.repo.AgentQueuedMessage("user", root.ID, receipt.MessageID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := service.CloudAgentRun("user", queued.RunID)
	if err != nil || len(next.Skills) != 1 || next.Skills[0].ID != "manual-skill" {
		t.Fatalf("manual skill was not frozen for the queued turn: %+v, %v", next, err)
	}
}

func TestStoryboardMessageInheritsFrozenDefaultAndManualSkills(t *testing.T) {
	service, db := agentRunFixture(t)
	project, err := service.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	project.PayloadJSON = `{"nodes":[{"id":"storyboard","type":"script"}]}`
	if err := service.repo.UpsertCanvasProject(project); err != nil {
		t.Fatal(err)
	}
	selectionDataDir = service.dataDir
	t.Cleanup(func() { selectionDataDir = "" })
	seedSelectionSkill(t, service, db, "board-default", 1024, false, 1)
	seedDefault(t, db, "board-default", 0)
	seedSelectionSkillFor(t, service, db, "user", "board-manual", 1024, true, 1)
	request := agentTestRequest()
	request.SkillIDs = []string{"board-manual"}
	root, err := service.CreateCloudAgentRun("user", request, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AgentSkillDefault{}).Where("skill_id = ?", "board-default").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	seedSelectionSkill(t, service, db, "later-default", 1024, false, 1)
	seedDefault(t, db, "later-default", 1)
	input := AgentConversationMessageRequest{
		Mode: "queue", ExpectedRunID: root.ID, InheritRunConfiguration: true,
		Request: CloudAgentRequest{CanvasID: request.CanvasID, Prompt: "先规划 3 镜、每镜 10 秒，确认后写回 storyboard", FocusNodeIDs: []string{"storyboard"}, IdempotencyKey: "storyboard-skill-inheritance"},
	}
	finishConversationTestRun(t, db, root.ID)
	receipt, err := service.SendAgentConversationMessage("user", root.ID, input)
	if err != nil || receipt.RunID == "" {
		queue, _ := service.AgentMessageQueue("user", root.ID)
		t.Fatalf("storyboard send did not start a new turn of its conversation: %+v, %v, queue=%+v", receipt, err, queue)
	}
	next, err := service.CloudAgentRun("user", receipt.RunID)
	if err != nil || next.ConversationID != root.ConversationID || len(next.Skills) != 2 || len(root.Skills) != 2 {
		t.Fatalf("storyboard lost the frozen skill set: %+v, %v", next, err)
	}
	for index, skill := range next.Skills {
		if skill.ID != root.Skills[index].ID || skill.VersionID != root.Skills[index].VersionID || skill.Source != root.Skills[index].Source {
			t.Fatalf("storyboard changed frozen skill %q", skill.ID)
		}
	}
}
