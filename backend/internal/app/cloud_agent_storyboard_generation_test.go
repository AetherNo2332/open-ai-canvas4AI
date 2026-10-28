package app

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"strings"
	"testing"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/canvas/tasks"
	"yingce/backend/internal/database"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
)

func storyboardMediaFixture(t *testing.T) (*Service, *gorm.DB, map[string]any, map[string]any) {
	t.Helper()
	s, db, a := agentMediaFixture(t)
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
	doc, _ := creationDocument(canvas.PayloadJSON)
	doc["referenceFieldsVersion"] = 1
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"referenceNodeIds": []any{"cat"}, "rows": []any{map[string]any{"id": "row", "shotNumber": 1, "durationSeconds": 5, "plotDescription": "scene", "imageGenerationPrompt": "still frame", "videoMotionPrompt": "camera moves", "assetBindings": []any{map[string]any{"nodeId": "hero", "role": "character", "priority": 100}}}}}}})
	raw, _ := json.Marshal(doc)
	if err := db.Model(canvas).Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"mode": "video", "storyboardNodeId": "board", "storyboardRowId": "row", "nodeId": a.NodeID, "title": "镜头视频", "channelId": a.ChannelID, "channelModelKey": a.ChannelModelKey, "size": "9:16", "snapshotHash": cloudAgentCanvasHash(doc)}
	doc, _ = creationDocument(string(raw))
	return s, db, doc, args
}
func storyboardMediaCall(t *testing.T, args map[string]any) cloudAgentCall {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: "storyboard-media"}
	call.Function.Name = "generate_media"
	call.Function.Arguments = string(raw)
	return call
}
func storyboardMediaPrepare(t *testing.T, s *Service, args map[string]any) (CreateTaskRequest, *cloudAgentMediaPlan, error) {
	t.Helper()
	state := cloudAgentRuntime{Request: agentTestRequest()}
	return s.prepareCloudAgentMedia(&model.CloudAgentExecution{ID: "storyboard-run", UserID: "user"}, &state, storyboardMediaCall(t, args))
}

func TestCloudAgentStoryboardApprovalSubmitsItsOwnBindingOnce(t *testing.T) {
	s, db, _, arguments := storyboardMediaFixture(t)
	var args cloudAgentMediaArgs
	raw, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	req := agentTestRequest()
	req.PermissionMode = "request_approval"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 2, 24
	view, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	call := storyboardMediaCall(t, arguments)
	call.ID = "media-call"
	run, _ := agentStagePiOutputForTest(t, s, db, view.ID, "", []cloudAgentCall{call})
	if err = agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
		t.Fatal(err)
	}
	approveAgentMediaDraft(t, s, run.ID)
	_, state := agentInterjectionState(t, s, run.ID)
	if state.MediaTaskID == "" {
		t.Fatal("approved storyboard task was not submitted")
	}
	binding, err := s.repo.CanvasTaskBinding(state.MediaTaskID)
	if err != nil || binding == nil || binding.TargetNodeID != args.StoryboardNodeID {
		t.Fatalf("storyboard submission lost its binding: %+v %v", binding, err)
	}
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := creationDocument(canvas.PayloadJSON)
	nodes, _ := creationObjects(doc["nodes"])
	row := creationMaps(cloudAgentNodeMetadata(nodes["board"])["storyboard"].(map[string]any)["rows"])[0]
	if row["videoNodeId"] != args.NodeID || row["taskId"] != state.MediaTaskID {
		t.Fatalf("approved row was not bound to its submitted output: %+v", row)
	}
	agentCanvasPatchForOperation(t, state, "generate_media_submit")
	if err = advancePiAgentForTest(t, s, run.ID); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err = db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("approved storyboard created multiple tasks: %d %v", count, err)
	}
}

func TestStoryboardGenerationResolvesDeclaredInputs(t *testing.T) {
	s, _, _, args := storyboardMediaFixture(t)
	req, plan, err := storyboardMediaPrepare(t, s, args)
	if err != nil {
		t.Fatal(err)
	}
	refs := creationMaps(req.Input["referenceImages"])
	if len(refs) != 2 || refs[0]["id"] != "hero" || refs[1]["id"] != "cat" || !strings.Contains(req.Prompt, "camera moves") || plan.Args.Duration != 5 {
		t.Fatalf("wrong row input: %+v %+v", req, plan.Args)
	}
}
func TestStoryboardModelListUsesSameInputs(t *testing.T) {
	s, _, _, _ := storyboardMediaFixture(t)
	intent, err := s.cloudAgentModelIntent("user", "agent-canvas", `{"mode":"video","storyboardNodeId":"board","storyboardRowId":"row"}`)
	if err != nil {
		t.Fatal(err)
	}
	if intent.Inputs["image"] != 2 {
		t.Fatalf("model query ignored row: %+v", intent)
	}
}

func TestStoryboardGenerationNativeUsesDeclaredInputs(t *testing.T) {
	s, _, doc, _ := storyboardMediaFixture(t)
	request := tasks.SubmitRequest{Command: commands.Command{CanvasID: "agent-canvas"}, TargetNodeID: "board", Consumer: tasks.Consumer{Kind: "storyboard", RowID: "row", OutputNodeID: "output", Field: "videoNodeId"}, Task: tasks.TaskRequest{Mode: "video", Prompt: "stale client prompt", Input: map[string]any{"config": map[string]any{"videoSeconds": "99"}, "referenceImages": []any{map[string]any{"id": "stale"}}}}}
	resolved, err := prepareCanvasStoryboardInput(context.Background(), s.repo, identity.Principal{ID: "user", OwnerID: "user"}, doc, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resolved.Prompt, "camera moves") || resolved.Input["config"].(map[string]any)["videoSeconds"] != "5" || creationMaps(resolved.Input["referenceImages"])[0]["id"] != "hero" {
		t.Fatalf("client data overrode row: %+v", resolved)
	}
}
func TestStoryboardGenerationRejectsConflictingInputs(t *testing.T) {
	for _, field := range []string{"prompt", "referenceNodeIds", "sourceNodeId", "durationSeconds", "referenceTransientIds"} {
		t.Run(field, func(t *testing.T) {
			s, db, _, args := storyboardMediaFixture(t)
			args[field] = "conflict"
			if field == "durationSeconds" {
				args[field] = 8
			}
			if strings.HasPrefix(field, "reference") {
				args[field] = []any{}
			}
			_, _, err := storyboardMediaPrepare(t, s, args)
			if err == nil || !strings.Contains(err.Error(), "分镜输入") {
				t.Fatalf("expected input conflict: %v", err)
			}
			var tasks, orders int64
			db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&tasks)
			db.Model(&model.BillingOrder{}).Count(&orders)
			if tasks != 0 || orders != 0 {
				t.Fatal("rejected input billed")
			}
		})
	}
}
func TestStoryboardPreparedRowChanged(t *testing.T) {
	doc := map[string]any{"nodes": []any{map[string]any{"id": "board", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{"id": "row", "videoMotionPrompt": "old motion"}}}}}}, "connections": []any{}}
	raw := []byte(`{"mode":"video","storyboardNodeId":"board","storyboardRowId":"row","nodeId":"output"}`)
	var a cloudAgentMediaArgs
	_ = json.Unmarshal(raw, &a)
	before, err := cloudAgentMediaDependencyHash(doc, a)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := creationObjects(doc["nodes"])
	board := cloudAgentNodeMetadata(nodes["board"])["storyboard"].(map[string]any)
	creationMaps(board["rows"])[0]["videoMotionPrompt"] = "new motion"
	after, err := cloudAgentMediaDependencyHash(doc, a)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("approved row change was ignored")
	}
}
func TestStoryboardReferenceLimitBeforeBilling(t *testing.T) {
	s, db, doc, args := storyboardMediaFixture(t)
	nodes, _ := creationObjects(doc["nodes"])
	row := creationMaps(cloudAgentNodeMetadata(nodes["board"])["storyboard"].(map[string]any)["rows"])[0]
	bindings := []any{}
	for i := 0; i < 17; i++ {
		id := strings.Repeat("a", i+1)
		doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": id, "type": "image", "metadata": map[string]any{"storageKey": "resource:ref-one"}})
		bindings = append(bindings, map[string]any{"nodeId": id, "role": "style", "priority": 50})
	}
	row["assetBindings"] = bindings
	raw, _ := json.Marshal(doc)
	db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", string(raw))
	args["snapshotHash"] = cloudAgentCanvasHash(doc)
	if _, _, err := storyboardMediaPrepare(t, s, args); err == nil || !strings.Contains(err.Error(), "16") {
		t.Fatalf("expected capacity error: %v", err)
	}
	var count int64
	db.Model(&model.BillingOrder{}).Count(&count)
	if count != 0 {
		t.Fatal("reference overflow billed")
	}
}
