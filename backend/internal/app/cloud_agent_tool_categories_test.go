package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func categoryTestRequest() CloudAgentRequest {
	return CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, SkillIDs: []string{"skill"}, VisionEnabled: true}
}

func categoryCall(name string) cloudAgentCall {
	var call cloudAgentCall
	call.ID = name
	call.Function.Name = name
	call.Function.Arguments = "{}"
	return call
}

func TestCloudAgentCategoryDisclosureAndRetraction(t *testing.T) {
	all := cloudAgentTools(categoryTestRequest())
	for _, name := range cloudAgentToolNames(all) {
		if !cloudAgentIsToolCategory(name) && cloudAgentToolCategory(name) == "" {
			t.Fatalf("registered tool has no disclosure category: %s", name)
		}
	}
	root := cloudAgentVisibleTools(all, "", nil, nil)
	if len(root) != 7 {
		t.Fatalf("root parent count = %d", len(root))
	}
	for _, name := range cloudAgentToolNames(root) {
		if !cloudAgentIsToolCategory(name) {
			t.Fatalf("child leaked into root: %s", name)
		}
	}
	selected := cloudAgentVisibleTools(all, "agent_tools_canvas_read", []cloudAgentCall{categoryCall("agent_tools_canvas_read")}, nil)
	names := cloudAgentToolNames(selected)
	if !containsToolName(names, "canvas_get_state") || containsToolName(names, "canvas_apply_ops") {
		t.Fatalf("wrong category selection: %v", names)
	}
	for _, tool := range selected {
		function := tool["function"].(map[string]any)
		if function["name"] == "agent_tools_canvas_read" && !strings.Contains(function["description"].(string), "agent_tools_canvas_read") {
			t.Fatal("previous call missing from parent schema")
		}
	}
	retracted := cloudAgentVisibleTools(all, "", []cloudAgentCall{categoryCall("canvas_get_state")}, nil)
	if containsToolName(cloudAgentToolNames(retracted), "canvas_get_state") {
		t.Fatal("child remained advertised after its step")
	}
	callWithPrivateArguments := categoryCall("canvas_get_state")
	callWithPrivateArguments.Function.Arguments = `{"private":"do-not-copy"}`
	record := cloudAgentCategoryCallRecord([]cloudAgentCall{callWithPrivateArguments}, "agent_tools_canvas_read")
	if !strings.Contains(record, "canvas_get_state") || strings.Contains(record, "do-not-copy") {
		t.Fatalf("unsafe previous-call record: %q", record)
	}
}

func TestCloudAgentCategoryPreflightUsesWireCatalog(t *testing.T) {
	req := categoryTestRequest()
	all := cloudAgentTools(req)
	state := &cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: all}, DisclosureVersion: cloudAgentToolDisclosureVersion}
	state.AdvertisedToolNames = cloudAgentToolNames(cloudAgentVisibleTools(all, "", nil, nil))
	if cloudAgentPreflightBatch(state, []cloudAgentCall{categoryCall("canvas_get_state")})[0].Allowed {
		t.Fatal("unadvertised child admitted")
	}
	if !cloudAgentPreflightBatch(state, []cloudAgentCall{categoryCall("agent_tools_canvas_read")})[0].Allowed {
		t.Fatal("parent rejected")
	}
	if cloudAgentPreflightBatch(state, []cloudAgentCall{categoryCall("agent_tools_canvas_read"), categoryCall("agent_tools_generation")})[1].Allowed {
		t.Fatal("multiple categories admitted")
	}
	state.AdvertisedToolNames = cloudAgentToolNames(cloudAgentVisibleTools(all, "agent_tools_canvas_read", nil, nil))
	if !cloudAgentPreflightBatch(state, []cloudAgentCall{categoryCall("canvas_get_state")})[0].Allowed {
		t.Fatal("selected child rejected")
	}
}

func TestCloudAgentCategoriesRespectCapabilityAndPermission(t *testing.T) {
	req := CloudAgentRequest{PermissionMode: "read_only"}
	root := cloudAgentVisibleTools(cloudAgentTools(req), "", nil, nil)
	names := cloudAgentToolNames(root)
	for _, absent := range []string{"agent_tools_skills", "agent_tools_canvas_read", "agent_tools_image", "agent_tools_canvas_edit", "agent_tools_generation"} {
		if containsToolName(names, absent) {
			t.Fatalf("ineligible parent %s", absent)
		}
	}
	req.ContextScope = []string{"canvas"}
	imageTools := cloudAgentToolNames(cloudAgentVisibleTools(cloudAgentTools(req), "agent_tools_image", nil, nil))
	if containsToolName(imageTools, "canvas_inspect_image") {
		t.Fatal("vision tool exposed without vision capability")
	}
	if containsToolName(imageTools, "image_layer_split") {
		t.Fatal("generation tool exposed in read-only mode")
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

func TestCloudAgentDisclosureSurvivesCheckpointAndRepair(t *testing.T) {
	all := cloudAgentTools(categoryTestRequest())
	state := cloudAgentRuntime{DisclosureVersion: cloudAgentToolDisclosureVersion, SelectedToolCategory: "agent_tools_canvas_read", AdvertisedToolNames: cloudAgentToolNames(cloudAgentVisibleTools(all, "agent_tools_canvas_read", nil, nil))}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored cloudAgentRuntime
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.SelectedToolCategory != state.SelectedToolCategory || !containsToolName(restored.AdvertisedToolNames, "canvas_get_state") {
		t.Fatal("disclosure checkpoint lost")
	}
	repair := cloudAgentVisibleTools(all, "", nil, []string{"canvas_get_state", "ask_user"})
	for _, name := range cloudAgentToolNames(repair) {
		if !cloudAgentIsToolCategory(name) {
			t.Fatalf("repair leaked child without selecting parent: %s", name)
		}
	}
	repairOpened := cloudAgentToolNames(cloudAgentVisibleTools(all, "agent_tools_canvas_read", nil, []string{"canvas_get_state", "ask_user"}))
	if !containsToolName(repairOpened, "canvas_get_state") || containsToolName(repairOpened, "canvas_read_storyboard") {
		t.Fatalf("repair scope did not limit children: %v", repairOpened)
	}
}

func TestCloudAgentFirstWireRequestContainsOnlyCategoryFunctions(t *testing.T) {
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
		if !strings.Contains(string(raw), "agent_tools_canvas_read") || strings.Contains(string(raw), `"name":"canvas_get_state"`) {
			t.Fatalf("%s leaked child function in first request: %s", protocol, raw)
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
