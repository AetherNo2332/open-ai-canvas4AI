package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func workflowTool(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, tool := range cloudAgentTools(tierAutoRequest()) {
		fn := tool["function"].(map[string]any)
		if fn["name"] == name {
			return fn["parameters"].(map[string]any)
		}
	}
	t.Fatalf("missing tool %s", name)
	return nil
}

func TestStoryboardWorkflowCompactContract(t *testing.T) {
	row := cloudAgentStoryboardRowSchema()["properties"].(map[string]any)
	if row["assets"] == nil {
		t.Error("creative row needs business asset references")
	}
	for _, field := range []string{"assetBindings", "characters"} {
		if row[field] != nil {
			t.Errorf("mechanical or duplicate field %s remains in compact contract", field)
		}
	}
	create := workflowTool(t, "canvas_create_storyboard")
	if containsToolName(create["required"].([]string), "nodeId") {
		t.Error("creation should allocate its own ID")
	}
	generate := workflowTool(t, "generate_media")
	for _, field := range []string{"nodeId", "title"} {
		if containsToolName(generate["required"].([]string), field) {
			t.Errorf("storyboard generation should derive %s", field)
		}
	}
}

func TestStoryboardWorkflowBaseReadsAreAlwaysAdvertised(t *testing.T) {
	names := cloudAgentAdvertisedToolNames(tierAutoRequest(), cloudAgentCanvasInventory{}, "检查剧本")
	for _, name := range []string{"canvas_read_content", "canvas_search_nodes", "canvas_list_node_types"} {
		if !containsToolName(names, name) {
			t.Errorf("basic read %s missing", name)
		}
	}
}

func workflowDocument() map[string]any {
	return map[string]any{"referenceFieldsVersion": float64(1), "connections": []any{}, "nodes": []any{
		map[string]any{"id": "hero", "title": "陈林", "type": "text", "metadata": map[string]any{"workflowKind": "character", "characterName": "陈林"}},
		map[string]any{"id": "scene", "title": "教室", "type": "image"},
		map[string]any{"id": "old", "title": "教室", "type": "image", "metadata": map[string]any{"usageStatus": "archived"}},
	}}
}

func TestStoryboardWorkflowBusinessReferences(t *testing.T) {
	doc := workflowDocument()
	ids, err := cloudAgentResolveBusinessReferences(doc, []string{"陈林", "hero", "教室"})
	if err != nil || len(ids) != 2 || ids[0] != "hero" || ids[1] != "scene" {
		t.Fatalf("resolve: %v %v", ids, err)
	}
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "teacher-v2", "title": "陈林", "type": "image"})
	if _, err := cloudAgentResolveBusinessReferences(doc, []string{"陈林"}); err == nil || !strings.Contains(err.Error(), "teacher-v2") {
		t.Fatalf("ambiguous name silently picked: %v", err)
	}
	for _, ref := range []string{"old", "不存在", ""} {
		if _, err := cloudAgentResolveBusinessReferences(doc, []string{ref}); err == nil {
			t.Fatalf("accepted invalid ref %q", ref)
		}
	}
}

func TestStoryboardWorkflowBatchPlanning(t *testing.T) {
	doc := workflowDocument()
	rows := []map[string]any{
		{"id": "r1", "shotNumber": float64(1), "durationSeconds": float64(5), "plotDescription": "old", "status": "idle", "characters": []any{map[string]any{"characterName": "旧角色"}}},
		{"id": "r2", "shotNumber": float64(2), "durationSeconds": float64(6), "plotDescription": "old", "imageNodeId": "existing", "status": "idle"},
	}
	before, _ := json.Marshal(rows)
	next, _, err := cloudAgentPlanStoryboardRows(doc, rows, cloudAgentStoryboardEditArgs{Action: "set_assets", RowIDs: []string{"r1", "r2"}, Assets: []cloudAgentStoryboardAsset{{Ref: "陈林", Role: "character"}, {Ref: "教室", Role: "environment"}}}, "user", "call")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range next {
		if len(creationMaps(row["assetBindings"])) != 2 || len(creationMaps(row["characters"])) != 0 {
			t.Fatalf("assets not replaced: %v", row)
		}
	}
	if next[1]["imageNodeId"] != "existing" {
		t.Fatal("unrelated output was changed")
	}
	after, _ := json.Marshal(rows)
	if string(before) != string(after) {
		t.Fatal("planning mutated input")
	}
	for _, args := range []cloudAgentStoryboardEditArgs{
		{Action: "update", RowIDs: []string{"r1", "missing"}, Patch: map[string]any{"dialogue": "new"}},
		{Action: "update", RowIDs: []string{"r1", "r1"}, Patch: map[string]any{"dialogue": "new"}},
		{Action: "set_assets", RowIDs: []string{"r1", "r2"}, Assets: []cloudAgentStoryboardAsset{{Ref: "陈林", Role: "audio"}}},
		{Action: "remove", RowIDs: []string{"r1"}, Assets: []cloudAgentStoryboardAsset{}},
	} {
		if _, _, err := cloudAgentPlanStoryboardRows(doc, rows, args, "user", "call"); err == nil {
			t.Fatalf("accepted invalid batch: %+v", args)
		}
	}
	after, _ = json.Marshal(rows)
	if string(before) != string(after) {
		t.Fatal("failed batch mutated input")
	}
	cleared, _, err := cloudAgentPlanStoryboardRows(doc, rows, cloudAgentStoryboardEditArgs{Action: "set_assets", RowID: "r1", Assets: []cloudAgentStoryboardAsset{}}, "user", "clear")
	if err != nil || len(creationMaps(cleared[0]["assetBindings"])) != 0 || len(creationMaps(cleared[0]["characters"])) != 0 {
		t.Fatalf("explicit clear: %v %v", cleared, err)
	}
	removed, _, err := cloudAgentPlanStoryboardRows(doc, rows, cloudAgentStoryboardEditArgs{Action: "remove", RowID: "r1"}, "user", "remove")
	if err != nil || len(removed) != 1 || removed[0]["shotNumber"] != float64(1) {
		t.Fatalf("remove/renumber: %v %v", removed, err)
	}
}

func TestStoryboardWorkflowOutputTarget(t *testing.T) {
	doc := workflowDocument()
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{"id": "r1", "shotNumber": float64(1)}}}}})
	a := cloudAgentMediaArgs{Mode: "image", StoryboardNodeID: "board", StoryboardRowID: "r1"}
	first, err := cloudAgentStoryboardOutputTarget(doc, a, "user", "same-call")
	if err != nil || first.NodeID == "" || first.Title != "镜头 1 · 关键帧" {
		t.Fatalf("auto target: %+v %v", first, err)
	}
	again, _ := cloudAgentStoryboardOutputTarget(doc, a, "user", "same-call")
	if again.NodeID != first.NodeID {
		t.Fatal("call replay chose another output")
	}
	_, _, rows, _ := storyboardNodeFromDocument(doc, "board")
	rows[0]["imageNodeId"] = "occupied"
	reused, _ := cloudAgentStoryboardOutputTarget(doc, a, "user", "different-call")
	if reused.NodeID != "occupied" {
		t.Fatal("occupied output bypassed with fresh target")
	}
	for _, meta := range []map[string]any{{"status": "loading", "taskId": "task"}, {"status": "success", "storageKey": "resource:ready"}} {
		if reason, _ := cloudAgentMediaTargetIssue(map[string]any{"id": reused.NodeID, "type": "image", "metadata": meta}, "image"); reason == "" {
			t.Fatal("occupied output admitted")
		}
	}
}

func TestStoryboardWorkflowCreativeContinuation(t *testing.T) {
	row := map[string]any{"id": "row", "plotDescription": strings.Repeat("镜🙂", 5000), "dialogue": "你好"}
	item := cloudAgentStoryboardCreativeRow("board", row, map[string]any{"state": "resolved"})
	if len([]rune(item["plotDescription"].(string))) != 2000 || item["plotDescriptionTruncated"] != true {
		t.Fatal("long creative field must be bounded and flagged")
	}
	doc := map[string]any{"nodes": []any{map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{row}}}}}}
	text, field, err := cloudAgentTextSource(doc, cloudAgentTextReadArgs{NodeID: "board", RowID: "row", Field: "plotDescription"})
	if err != nil || field != "plotDescription" || text != row["plotDescription"] {
		t.Fatalf("cannot continue: %v", err)
	}
	if _, _, err := cloudAgentTextSource(doc, cloudAgentTextReadArgs{NodeID: "board", RowID: "row", Field: "imageNodeId"}); err == nil {
		t.Fatal("non-text field readable as content")
	}
}

func TestStoryboardWorkflowModelGrouping(t *testing.T) {
	calls := 0
	result, err := cloudAgentGroupStoryboardModels([]string{"one", "two", "three", "bad"}, func(id string) (*ModelRequestIntent, error) {
		if id == "bad" {
			return nil, BadAuthRequest("分镜行不存在")
		}
		count := 1
		if id == "three" {
			count = 2
		}
		return &ModelRequestIntent{Capability: "image", Inputs: map[string]int{"image": count}}, nil
	}, func(intent *ModelRequestIntent) (any, error) { calls++; return map[string]any{"intent": intent}, nil })
	if err != nil || calls != 2 || len(result["groups"].([]map[string]any)) != 2 {
		t.Fatalf("catalog not grouped: %v %v calls=%d", result, err, calls)
	}
	rows := result["rows"].([]any)
	if rows[0].(map[string]any)["group"] != rows[1].(map[string]any)["group"] || rows[3].(map[string]any)["issue"] == nil {
		t.Fatal("lost per-row result")
	}
	if _, err := cloudAgentGroupStoryboardModels([]string{"one", "one"}, nil, nil); err == nil {
		t.Fatal("duplicate target admitted")
	}
}

func TestStoryboardWorkflowDisclosureOnlyExpandsAtBoundary(t *testing.T) {
	req := tierAutoRequest()
	state := cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}}
	state.AdvertisedToolNames = cloudAgentAdvertisedToolNames(req, cloudAgentCanvasInventory{}, "节点数量")
	boardInventory := cloudAgentCanvasInventory{NodeTypes: []string{"script"}}
	for _, guard := range []string{"model", "batch"} {
		state.ActiveTaskID = ""
		state.Calls = nil
		if guard == "model" {
			state.ActiveTaskID = "inflight"
		} else {
			state.Calls = []cloudAgentCall{{ID: "pending"}}
		}
		if containsToolName(cloudAgentNextAdvertisedToolNames(&state, boardInventory), "canvas_edit_storyboard") {
			t.Fatalf("expanded during %s", guard)
		}
	}
	state.ActiveTaskID = ""
	state.Calls = nil
	next := cloudAgentNextAdvertisedToolNames(&state, boardInventory)
	for _, want := range []string{"canvas_read_storyboard", "canvas_edit_storyboard", "canvas_create_character", "canvas_list_assets", "canvas_bind_asset"} {
		if !containsToolName(next, want) {
			t.Fatalf("incomplete workflow: %s", want)
		}
	}
	state.PendingInterjections = []cloudAgentInterjection{{Text: "分镜"}}
	state.Canonical.Messages = []map[string]any{{"role": "tool", "content": "分镜"}, {"role": "user", "content": "分镜", cloudAgentContextSourceKey: "subagent_message"}}
	if containsToolName(cloudAgentNextAdvertisedToolNames(&state, cloudAgentCanvasInventory{}), "canvas_edit_storyboard") {
		t.Fatal("data/pending text unlocked tools")
	}
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "user", "content": "写分镜", cloudAgentContextSourceKey: "user_interjection"})
	if !containsToolName(cloudAgentNextAdvertisedToolNames(&state, cloudAgentCanvasInventory{}), "canvas_edit_storyboard") {
		t.Fatal("delivered user instruction did not unlock tools")
	}
	state.AdvertisedToolNames = next
	if !containsToolName(cloudAgentNextAdvertisedToolNames(&state, cloudAgentCanvasInventory{}), "canvas_create_character") {
		t.Fatal("loadout shrank")
	}
	state.Request.PermissionMode = "read_only"
	if containsToolName(cloudAgentNextAdvertisedToolNames(&state, boardInventory), "canvas_edit_storyboard") {
		t.Fatal("expansion granted write permission")
	}
}

func TestStoryboardWorkflowConditionalArguments(t *testing.T) {
	for _, sample := range []struct {
		name, arguments string
		valid           bool
	}{
		{"canvas_create_storyboard", `{"snapshotHash":"h","title":"分镜","rows":[{"durationSeconds":5,"plotDescription":"进入教室","assets":[{"ref":"陈林","role":"character"}]}]}`, true},
		{"canvas_create_storyboard", `{"snapshotHash":"h","title":"分镜","rows":[{"durationSeconds":5,"plotDescription":"进入教室","camera":"pan"}]}`, true},
		{"canvas_create_storyboard", `{"snapshotHash":"h","title":"分镜","references":[],"referenceNodeIds":[],"rows":[{"durationSeconds":5,"plotDescription":"进入教室"}]}`, false},
		{"canvas_edit_storyboard", `{"snapshotHash":"h","nodeId":"board","action":"set_assets","rowIds":["one","two"],"assets":[]}`, true},
		{"canvas_edit_storyboard", `{"snapshotHash":"h","nodeId":"board","action":"update","rowIds":["one","two"],"patch":{"dialogue":"你好"}}`, true},
		{"canvas_edit_storyboard", `{"snapshotHash":"h","nodeId":"board","action":"update","rowId":"one","rowIds":["one"],"patch":{"dialogue":"你好"}}`, false},
		{"canvas_edit_storyboard", `{"snapshotHash":"h","nodeId":"board","action":"set_references","references":["剧本"]}`, true},
		{"generate_media", `{"mode":"image","storyboardNodeId":"board","storyboardRowId":"one"}`, true},
		{"generate_media", `{"mode":"image","prompt":"镜头","referenceNodeIds":[],"size":"16:9"}`, false},
		{"model_list", `{"mode":"video","storyboardNodeId":"board","storyboardRowIds":["one","two"]}`, true},
		{"model_list", `{"mode":"video","storyboardNodeId":"board","storyboardRowIds":["one"],"referenceNodeIds":[]}`, false},
	} {
		err := validateCloudAgentToolArguments(workflowTool(t, sample.name), sample.arguments)
		if (err == nil) != sample.valid {
			t.Errorf("%s valid=%v: %s: %v", sample.name, sample.valid, sample.arguments, err)
		}
	}
}

func TestStoryboardWorkflowModelIntentKeepsRowDuration(t *testing.T) {
	doc := workflowDocument()
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{
		map[string]any{"id": "short", "durationSeconds": float64(5), "plotDescription": "short"}, map[string]any{"id": "long", "durationSeconds": float64(20), "plotDescription": "long"},
	}}}})
	s := &Service{}
	raw, _ := json.Marshal(doc)
	doc, _ = creationDocument(string(raw))
	resolve := func(id string) (*ModelRequestIntent, error) {
		return s.cloudAgentModelIntentFromDocument("user", "", `{"mode":"video","storyboardNodeId":"board","storyboardRowId":"`+id+`"}`, doc)
	}
	intent, err := resolve("short")
	if err != nil || intent.Options["videoSeconds"] != "5" {
		t.Fatalf("lost row duration: %+v %v", intent, err)
	}
	result, err := cloudAgentGroupStoryboardModels([]string{"short", "long"}, resolve, func(intent *ModelRequestIntent) (any, error) { return intent, nil })
	if err != nil || len(result["groups"].([]map[string]any)) != 2 {
		t.Fatalf("different durations merged: %v %v", result, err)
	}
}

func TestStoryboardWorkflowPiDeliveryKeepsToolIntent(t *testing.T) {
	req := tierAutoRequest()
	state := cloudAgentRuntime{Request: req, Canonical: canonicalAgentRequest{Tools: cloudAgentTools(req)}, PendingInterjections: []cloudAgentInterjection{{ID: "user-message", Text: "写分镜"}}}
	state.AdvertisedToolNames = cloudAgentAdvertisedToolNames(req, cloudAgentCanvasInventory{}, "节点数量")
	if err := cloudAgentCommitPiInterjections("run", &state, []string{"user-message"}, "【用户插话】写分镜"); err != nil {
		t.Fatal(err)
	}
	state.Canonical.Messages = nil
	if !containsToolName(cloudAgentNextAdvertisedToolNames(&state, cloudAgentCanvasInventory{}), "canvas_edit_storyboard") {
		t.Fatal("delivered Pi instruction lost after checkpoint/compaction")
	}
}

func TestStoryboardWorkflowCreationIDsAreStepScoped(t *testing.T) {
	state := cloudAgentRuntime{Request: tierAutoRequest(), Step: 1, LastStepTaskID: "task-1"}
	state.Request.CanvasID = "canvas"
	for _, name := range []string{"canvas_create_storyboard", "canvas_create_character"} {
		call := cloudAgentCall{ID: "call-1"}
		call.Function.Name = name
		call.Function.Arguments = `{}`
		one, err := cloudAgentResolveWorkflowIDs("user", "run", &state, call)
		if err != nil {
			t.Fatal(err)
		}
		replay, _ := cloudAgentResolveWorkflowIDs("user", "run", &state, call)
		if replay.Function.Arguments != one.Function.Arguments {
			t.Fatal("same step replay changed ID")
		}
		state.LastStepTaskID = "task-2"
		two, _ := cloudAgentResolveWorkflowIDs("user", "run", &state, call)
		if two.Function.Arguments == one.Function.Arguments {
			t.Fatal("repeated provider ID collided across steps")
		}
		other, _ := cloudAgentResolveWorkflowIDs("user", "other-run", &state, call)
		if other.Function.Arguments == two.Function.Arguments {
			t.Fatal("repeated provider ID collided across runs")
		}
		kept, _ := cloudAgentResolveWorkflowIDs("user", "run", &state, one)
		if kept.Function.Arguments != one.Function.Arguments {
			t.Fatal("approval/apply changed resolved ID")
		}
		state.LastStepTaskID = "task-1"
	}
	bad := cloudAgentCall{}
	bad.Function.Name = "canvas_create_storyboard"
	bad.Function.Arguments = "null"
	if _, err := cloudAgentResolveWorkflowIDs("user", "run", &state, bad); err == nil {
		t.Fatal("null call accepted")
	}
}
