package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func categoryTestRequest() CloudAgentRequest {
	req := CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, SkillIDs: []string{"skill"}, VisionEnabled: true}
	req.Budget.MaxGenerationTasks = 1
	return req
}

func categoryCall(name string) cloudAgentCall {
	var call cloudAgentCall
	call.ID = name
	call.Function.Name = name
	call.Function.Arguments = "{}"
	return call
}

func TestCloudAgentRegistersEligibleConcreteToolsFromStart(t *testing.T) {
	all := cloudAgentTools(categoryTestRequest())
	visible := cloudAgentVisibleTools(all, "", nil, nil)
	names := cloudAgentToolNames(visible)
	for _, parent := range []string{"agent_tools_control", "agent_tools_memory", "agent_tools_skills", "agent_tools_canvas_read", "agent_tools_image", "agent_tools_canvas_edit", "agent_tools_generation"} {
		if containsToolName(names, parent) {
			t.Fatalf("category entry %q must not be model-callable: %v", parent, names)
		}
	}
	for _, concrete := range []string{"canvas_get_state", "canvas_apply_ops", "plan_update", "generate_media", "skill_search"} {
		if !containsToolName(names, concrete) {
			t.Fatalf("eligible concrete tool %q missing at run start: %v", concrete, names)
		}
	}
	for _, name := range names {
		if cloudAgentToolCategory(name) == "" {
			t.Fatalf("registered concrete tool has no classification category: %s", name)
		}
	}
}

func TestCloudAgentPreflightUsesFlatAdvertisedConcreteCatalog(t *testing.T) {
	req := categoryTestRequest()
	all := cloudAgentTools(req)
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: all}, DisclosureVersion: cloudAgentToolDisclosureVersion}
	state.AdvertisedToolNames = cloudAgentToolNames(cloudAgentVisibleTools(all, "", nil, nil))
	if !cloudAgentPreflightBatch(state, []cloudAgentCall{categoryCall("canvas_get_state")})[0].Allowed {
		t.Fatal("eligible concrete read tool rejected at run start")
	}
	write := categoryCall("canvas_apply_ops")
	write.Function.Arguments = `{"snapshotHash":"hash","ops":[]}`
	if !cloudAgentPreflightBatch(state, []cloudAgentCall{write})[0].Allowed {
		t.Fatal("eligible concrete write tool rejected at run start")
	}
	if cloudAgentPreflightBatch(state, []cloudAgentCall{categoryCall("agent_tools_canvas_read")})[0].Allowed {
		t.Fatal("removed category selector admitted")
	}
	state.AdvertisedToolNames = []string{"canvas_get_state"}
	if cloudAgentPreflightBatch(state, []cloudAgentCall{categoryCall("canvas_apply_ops")})[0].Allowed {
		t.Fatal("unadvertised write tool admitted")
	}
	// Tool categories no longer consume a model step or block calls from another
	// category; ordinary permission and single-write rules still apply.
	state.AdvertisedToolNames = cloudAgentToolNames(cloudAgentVisibleTools(all, "", nil, nil))
	plan := categoryCall("plan_update")
	plan.Function.Arguments = `{"items":[]}`
	batch := cloudAgentPreflightBatch(state, []cloudAgentCall{plan, categoryCall("canvas_get_state")})
	if !batch[0].Allowed || !batch[1].Allowed {
		t.Fatalf("cross-category non-write batch rejected: %+v", batch)
	}
}

func TestCloudAgentPreviousStepCallsDoNotChangeToolDefinitions(t *testing.T) {
	all := cloudAgentTools(categoryTestRequest())
	private := categoryCall("canvas_get_state")
	private.Function.Arguments = `{"private":"do-not-copy"}`
	baseline := cloudAgentVisibleTools(all, "", nil, nil)
	if len(baseline) == 0 {
		t.Fatal("no eligible tools")
	}
	want, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	for _, previous := range [][]cloudAgentCall{{private, categoryCall("plan_update")}, {categoryCall("plan_update")}, nil} {
		got, err := json.Marshal(cloudAgentVisibleTools(all, "", previous, nil))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatal("previous calls changed model-facing tool definitions")
		}
	}
}

func TestCloudAgentToolCategoriesRespectCapabilityAndPermission(t *testing.T) {
	readOnly := CloudAgentRequest{PermissionMode: "read_only", ContextScope: []string{"canvas"}}
	readTools := cloudAgentToolNames(cloudAgentVisibleTools(cloudAgentTools(readOnly), "", nil, nil))
	if containsToolName(readTools, "canvas_apply_ops") || containsToolName(readTools, "generate_media") {
		t.Fatalf("read-only run exposed a write tool: %v", readTools)
	}
	noVision := categoryTestRequest()
	noVision.VisionEnabled = false
	noVisionTools := cloudAgentToolNames(cloudAgentVisibleTools(cloudAgentTools(noVision), "", nil, nil))
	if containsToolName(noVisionTools, "canvas_inspect_image") {
		t.Fatalf("vision-dependent tool exposed without vision capability: %v", noVisionTools)
	}
}

func TestCloudAgentToolDescriptionsFromMarkdown(t *testing.T) {
	for _, tool := range cloudAgentTools(categoryTestRequest()) {
		function := tool["function"].(map[string]any)
		name := function["name"].(string)
		if function["description"] == "" || cloudAgentToolText(name) == "" {
			t.Fatalf("missing Markdown description for %s", name)
		}
		if _, err := json.Marshal(function["parameters"]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloudAgentFlatDisclosureSurvivesLegacyCheckpointAndRepair(t *testing.T) {
	all := cloudAgentTools(categoryTestRequest())
	state := cloudAgentRuntime{DisclosureVersion: cloudAgentToolDisclosureVersion,
		ActivatedToolCategories: []string{"agent_tools_canvas_read", "agent_tools_control"},
		AdvertisedToolNames:     cloudAgentToolNames(cloudAgentVisibleToolsForCategories(all, nil, nil, nil))}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored cloudAgentRuntime
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if len(cloudAgentActivatedCategories(&restored)) != 2 || !containsToolName(restored.AdvertisedToolNames, "canvas_get_state") {
		t.Fatal("legacy categories or flat advertised tools were lost in checkpoint")
	}
	legacy := cloudAgentRuntime{SelectedToolCategory: "agent_tools_canvas_read"}
	if !containsToolName(cloudAgentActivatedCategories(&legacy), "agent_tools_canvas_read") {
		t.Fatal("older checkpoint lost selected category")
	}
	repair := cloudAgentToolNames(cloudAgentVisibleTools(all, "", nil, []string{"canvas_get_state", "ask_user"}))
	if len(repair) != 2 || !containsToolName(repair, "canvas_get_state") || !containsToolName(repair, "ask_user") {
		t.Fatalf("repair scope did not narrow concrete tools: %v", repair)
	}
}

func TestCloudAgentFirstWireRequestContainsConcreteFunctions(t *testing.T) {
	req := categoryTestRequest()
	canonical := cloudAgentCanonicalFor("policy", nil, "goal", req, true)
	canonical.Tools = cloudAgentVisibleTools(canonical.Tools, "", nil, nil)
	for protocol, body := range map[string]map[string]interface{}{
		"chat":      canonicalAgentChatBody(&canonical, false),
		"responses": canonicalAgentResponsesBody(&canonical),
		"claude":    claudeAgentBody(canonicalAgentChatBody(&canonical, true)),
		"gemini":    canonicalAgentGeminiBody(&canonical),
	} {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"name":"canvas_get_state"`) || strings.Contains(string(raw), `"name":"agent_tools_canvas_read"`) {
			t.Fatalf("%s first request did not expose the flat concrete tool set: %s", protocol, raw)
		}
	}
}

func containsToolName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
