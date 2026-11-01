package app

import (
	"testing"

	"yingce/backend/internal/model"
)

func tierRunRequest(prompt, key string) CloudAgentRequest {
	req := tierAutoRequest()
	req.SkillIDs = nil
	req.CanvasID = "agent-canvas"
	req.Prompt = prompt
	req.Model = "text-test"
	req.ChannelID = "channel"
	req.ChannelModelKey = "text-test"
	req.IdempotencyKey = key
	req.Budget.MaxCredits = 1
	return req
}

func TestCloudAgentRunFreezesAdvertisedL0OnEmptyCanvas(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCloudAgentRun("user", tierRunRequest("现在画布有几个节点", "tier-empty"), "")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	if !containsToolName(state.AdvertisedToolNames, "canvas_get_state") || !containsToolName(state.AdvertisedToolNames, "canvas_apply_ops") {
		t.Fatalf("empty canvas run missing L0 advertised tools: %v", state.AdvertisedToolNames)
	}
	if !containsToolName(state.AdvertisedToolNames, "storyboard") {
		t.Fatalf("canvas run missing unified storyboard tool: %v", state.AdvertisedToolNames)
	}
	if !containsToolName(cloudAgentToolNames(state.Canonical.Tools), "storyboard") {
		t.Fatal("eligible catalog must still contain storyboard")
	}
}

func TestCloudAgentRunAdvertisesStoryboardWhenScriptNodeExists(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	payload := `{"nodes":[{"id":"board","type":"script","title":"分镜","metadata":{"workflowKind":"script"}}],"connections":[]}`
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: payload}).Error; err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateCloudAgentRun("user", tierRunRequest("继续当前画布工作", "tier-script"), "")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := s.repo.CloudAgent("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(execution)
	if err != nil {
		t.Fatal(err)
	}
	if !containsToolName(state.AdvertisedToolNames, "storyboard") {
		t.Fatalf("script node run missing storyboard tools: %v", state.AdvertisedToolNames)
	}
}

func TestCloudAgentPromptCacheKeyFollowsAdvertisedTools(t *testing.T) {
	req := tierAutoRequest()
	req.CanvasID = "agent-canvas"
	eligible := cloudAgentTools(req)
	full := cloudAgentToolsNamed(eligible, cloudAgentAdvertisedToolNames(req))
	readOnly := req
	readOnly.PermissionMode = "read_only"
	narrowed := cloudAgentToolsNamed(eligible, cloudAgentAdvertisedToolNames(readOnly))
	identity := "cache-identity"
	fullKey := cloudAgentPromptCacheKeyForRequest(req.CanvasID, identity, "system", full)
	if fullKey == cloudAgentPromptCacheKeyForRequest(req.CanvasID, identity, "system", narrowed) {
		t.Fatal("prompt cache key must change when the advertised tools change")
	}
	if cloudAgentPromptCacheKeyForRequest(req.CanvasID, identity, "system", full) != fullKey {
		t.Fatal("same advertised tools must keep the same prompt cache key")
	}
}

func TestCloudAgentPreflightRejectsEligibleButUnadvertisedStoryboardEdit(t *testing.T) {
	req := tierAutoRequest()
	all := cloudAgentTools(req)
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: all}, DisclosureVersion: cloudAgentToolDisclosureVersion}
	state.AdvertisedToolNames = []string{"canvas_get_state"}
	call := categoryCall("storyboard")
	call.Function.Arguments = `{"action":"update","snapshotHash":"h","nodeId":"board","rowIds":["row"],"patch":{"dialogue":"x"}}`
	admission := cloudAgentPreflightBatch(state, []cloudAgentCall{call})[0]
	if admission.Allowed {
		t.Fatal("unadvertised storyboard edit admitted")
	}
}

func TestCloudAgentEmptyAdvertisedNamesFallBackToEligibleCatalog(t *testing.T) {
	req := tierAutoRequest()
	all := cloudAgentTools(req)
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: all}, DisclosureVersion: cloudAgentToolDisclosureVersion}
	if _, ok := cloudAgentAdvertisedTool(state, "storyboard"); !ok {
		t.Fatal("empty advertised names must fall back to eligible catalog")
	}
	state.AdvertisedToolNames = []string{"canvas_get_state"}
	if _, ok := cloudAgentAdvertisedTool(state, "storyboard"); ok {
		t.Fatal("frozen L0 advertised set admitted L1 write")
	}
}

// 画布内容变化不再影响工具清单：刷新前后都是同一份冻结清单。
func TestAgentLoadoutIgnoresCanvasInventoryChanges(t *testing.T) {
	s, db, _, _ := creationTestService(t)
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: tierPayload()}).Error; err != nil {
		t.Fatal(err)
	}
	view, err := s.CreateCloudAgentRun("user", tierRunRequest("节点数量", "workflow-loadout"), "")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.repo.CloudAgent("user", view.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{tierPayload(tierNode("table", "batch-table", nil)), tierPayload()} {
		if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", payload).Error; err != nil {
			t.Fatal(err)
		}
		if run, err = s.cloudAgentRefreshToolLoadout(run); err != nil {
			t.Fatal(err)
		}
	}
	after, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.AdvertisedToolNames) != len(before.AdvertisedToolNames) || !containsToolName(after.AdvertisedToolNames, "canvas_edit_batch_table") {
		t.Fatalf("canvas changes must not change the frozen loadout: %v -> %v", before.AdvertisedToolNames, after.AdvertisedToolNames)
	}
}
