package app

import (
	"testing"

	"infinite-canvas/backend/internal/model"
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
	if containsToolName(state.AdvertisedToolNames, "canvas_edit_storyboard") {
		t.Fatalf("empty canvas run advertised storyboard edit: %v", state.AdvertisedToolNames)
	}
	if !containsToolName(cloudAgentToolNames(state.Canonical.Tools), "canvas_edit_storyboard") {
		t.Fatal("eligible catalog must still contain L1 storyboard edit")
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
	if !containsToolName(state.AdvertisedToolNames, "canvas_read_storyboard") || !containsToolName(state.AdvertisedToolNames, "canvas_edit_storyboard") {
		t.Fatalf("script node run missing storyboard tools: %v", state.AdvertisedToolNames)
	}
}

func TestCloudAgentPromptCacheKeyFollowsAdvertisedTools(t *testing.T) {
	req := tierAutoRequest()
	req.CanvasID = "agent-canvas"
	eligible := cloudAgentTools(req)
	empty := cloudAgentToolsNamed(eligible, cloudAgentAdvertisedToolNames(req, cloudAgentScanCanvasInventory(tierPayload()), "现在画布有几个节点"))
	script := cloudAgentToolsNamed(eligible, cloudAgentAdvertisedToolNames(req, cloudAgentScanCanvasInventory(tierPayload(tierNode("board", "script", nil))), "现在画布有几个节点"))
	identity := "cache-identity"
	emptyKey := cloudAgentPromptCacheKeyForRequest(req.CanvasID, identity, "system", empty)
	scriptKey := cloudAgentPromptCacheKeyForRequest(req.CanvasID, identity, "system", script)
	if emptyKey == scriptKey {
		t.Fatal("prompt cache key must change when advertised L1 tools change")
	}
	again := cloudAgentPromptCacheKeyForRequest(req.CanvasID, identity, "system", empty)
	if again != emptyKey {
		t.Fatal("same advertised tools must keep the same prompt cache key")
	}
}

func TestCloudAgentPreflightRejectsEligibleButUnadvertisedStoryboardEdit(t *testing.T) {
	req := tierAutoRequest()
	all := cloudAgentTools(req)
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: all}, DisclosureVersion: cloudAgentToolDisclosureVersion}
	state.AdvertisedToolNames = cloudAgentAdvertisedToolNames(req, cloudAgentScanCanvasInventory(tierPayload()), "现在画布有几个节点")
	call := categoryCall("canvas_edit_storyboard")
	call.Function.Arguments = `{"snapshotHash":"h","nodeId":"board","action":"append"}`
	admission := cloudAgentPreflightBatch(state, []cloudAgentCall{call})[0]
	if admission.Allowed {
		t.Fatal("unadvertised storyboard edit admitted")
	}
}

func TestCloudAgentEmptyAdvertisedNamesFallBackToEligibleCatalog(t *testing.T) {
	req := tierAutoRequest()
	all := cloudAgentTools(req)
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: all}, DisclosureVersion: cloudAgentToolDisclosureVersion}
	if _, ok := cloudAgentAdvertisedTool(state, "canvas_edit_storyboard"); !ok {
		t.Fatal("empty advertised names must fall back to eligible catalog")
	}
	state.AdvertisedToolNames = []string{"canvas_get_state"}
	if _, ok := cloudAgentAdvertisedTool(state, "canvas_edit_storyboard"); ok {
		t.Fatal("frozen L0 advertised set admitted L1 write")
	}
}
