package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func canvasRunVideoSpec(prompt string) map[string]any {
	return map[string]any{
		"version": 1, "mode": "video", "prompt": prompt,
		"modelSelection":    map[string]any{"kind": "channel", "channelId": "channel", "modelKey": "seedance-test"},
		"options":           map[string]any{"size": "9:16", "resolution": "720", "durationSeconds": 5, "generateAudio": true},
		"referenceBindings": []any{}, "textInputMode": "prompt-only",
	}
}

func canvasRunFixtureDoc() map[string]any {
	return map[string]any{
		"id": "agent-canvas", "referenceFieldsVersion": 1,
		"nodes": []map[string]any{
			{"id": "submap-1", "type": "submap", "title": "原镜头", "position": map[string]any{"x": 80, "y": 80}, "metadata": map[string]any{"status": "idle"}},
			{"id": "submap-1__text-1", "type": "text", "title": "原文本", "scopeId": "submap-1", "position": map[string]any{"x": 120, "y": 120}, "metadata": map[string]any{"content": "原镜头文本"}},
			{"id": "submap-1__image-1", "type": "image", "title": "原首帧", "scopeId": "submap-1", "position": map[string]any{"x": 360, "y": 120}, "metadata": map[string]any{"status": "success", "storageKey": "resource:ref-one", "content": "https://must-not-expose.invalid/one.png"}},
			{"id": "submap-1__video-1", "type": "video", "title": "原视频", "scopeId": "submap-1", "position": map[string]any{"x": 600, "y": 120}, "width": 400, "height": 225,
				"metadata": map[string]any{"status": "idle", "referenceTextNodeIds": []any{"submap-1__text-1"}, "referenceNodeIds": []any{"submap-1__image-1"}, "videoStartFrameNodeId": "submap-1__image-1", "generationSpec": canvasRunVideoSpec("@文本1\n@图片1")}},
			{"id": "submap-2", "type": "submap", "title": "副本镜头", "position": map[string]any{"x": 80, "y": 420}, "metadata": map[string]any{"status": "idle"}},
			{"id": "submap-2__text-1", "type": "text", "title": "副本文本", "scopeId": "submap-2", "position": map[string]any{"x": 120, "y": 460}, "metadata": map[string]any{"content": "副本镜头文本"}},
			{"id": "submap-2__image-1", "type": "image", "title": "副本首帧", "scopeId": "submap-2", "position": map[string]any{"x": 360, "y": 460}, "metadata": map[string]any{"status": "success", "storageKey": "resource:ref-two"}},
			{"id": "submap-2__video-1", "type": "video", "title": "副本视频", "scopeId": "submap-2", "position": map[string]any{"x": 600, "y": 460}, "width": 400, "height": 225,
				"metadata": map[string]any{"status": "idle", "referenceTextNodeIds": []any{"submap-2__text-1"}, "referenceNodeIds": []any{"submap-2__image-1"}, "videoStartFrameNodeId": "submap-2__image-1", "generationSpec": canvasRunVideoSpec("@文本1\n@图片1")}},
		},
		"connections": []any{},
	}
}

func canvasRunFixture(t *testing.T) (*Service, *gorm.DB, string) {
	t.Helper()
	s, db, _ := agentMediaFixture(t)
	s.mediaBatchMu.Lock()
	s.mediaBatchClosed = true
	s.mediaBatchMu.Unlock()
	doc := canvasRunFixtureDoc()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("payload_json", string(raw)).Error; err != nil {
		t.Fatal(err)
	}
	return s, db, cloudAgentCanvasHash(doc)
}

func canvasRunCall(t *testing.T, command, snapshotHash string) cloudAgentCall {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"command": command, "snapshotHash": snapshotHash})
	if err != nil {
		t.Fatal(err)
	}
	call := cloudAgentCall{ID: "run-call"}
	call.Function.Name, call.Function.Arguments = "canvas", string(raw)
	return call
}

func canvasRunStart(t *testing.T, s *Service, call cloudAgentCall, maxGenerations int) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	req := agentTestRequest()
	req.PermissionMode = "auto"
	req.Budget.MaxGenerationTasks = maxGenerations
	req.Budget.MaxVideoSeconds = 60
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, decoded := startPiAgentFirstStep(t, s, root.ID)
	state := *decoded
	state.ActiveTaskID = ""
	state.Step = 1
	state.Calls = []cloudAgentCall{call}
	state.PiToolBatchTaskID = state.LastStepTaskID
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": state.Calls})
	if err = s.repo.MutateCloudAgent("user", run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent("user", run.ID)
	state, _ = cloudAgentDecode(run)
	return run, state
}

func TestCanvasRunComposesFieldReferencesAndTextOrder(t *testing.T) {
	raw, err := json.Marshal(canvasRunFixtureDoc())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := creationObjects(doc["nodes"])
	if err != nil {
		t.Fatal(err)
	}
	node := nodes["submap-1__video-1"]
	meta := cloudAgentNodeMetadata(node)
	meta["referenceTextNodeIds"] = []any{"submap-1__text-1"}
	args := cloudAgentMediaBatchArgs{Mode: "video", Items: []struct {
		NodeID   string `json:"nodeId"`
		Prompt   string `json:"prompt"`
		Duration int    `json:"durationSeconds"`
	}{{NodeID: "submap-1__video-1"}}}
	item, err := cloudAgentMediaBatchItemArgs(doc, args, 0)
	if err != nil {
		t.Fatal(err)
	}
	if item.SourceNodeID != "submap-1__text-1" || len(item.ReferenceNodeIDs) != 1 || item.ReferenceNodeIDs[0] != "submap-1__image-1" {
		t.Fatalf("field references not resolved: %+v", item)
	}
	if item.VideoStartFrameNodeID != "submap-1__image-1" || item.VideoEndFrameNodeID != "" {
		t.Fatalf("frame roles wrong: start=%q end=%q", item.VideoStartFrameNodeID, item.VideoEndFrameNodeID)
	}
	for _, want := range []string{"【文本1】", "@图片1", "【文本1】\n原镜头文本"} {
		if !strings.Contains(item.Prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, item.Prompt)
		}
	}
	if strings.Contains(item.Prompt, "@文本1") {
		t.Fatalf("text mention was not compiled: %s", item.Prompt)
	}
}

func TestCanvasRunExpandsToBatchWithNodeSpec(t *testing.T) {
	s, _, hash := canvasRunFixture(t)
	call := canvasRunCall(t, "run ./submap-1/video-1/ ./submap-2/video-1/", hash)
	args, ok, err := cloudAgentCanvasRunBatchArgs(s.repo, "user", "agent-canvas", call)
	if err != nil || !ok {
		t.Fatalf("run should expand: ok=%v err=%v", ok, err)
	}
	if args.Mode != "video" || args.ChannelID != "channel" || args.ChannelModelKey != "seedance-test" || args.Size != "9:16" || args.Quality != "720" {
		t.Fatalf("node generation spec not carried: %+v", args)
	}
	if args.VideoGenerateAudio == nil || !*args.VideoGenerateAudio || len(args.Items) != 2 {
		t.Fatalf("batch fields wrong: %+v", args)
	}
	if args.Items[0].NodeID != "submap-1__video-1" || args.Items[1].NodeID != "submap-2__video-1" || args.Items[0].Duration != 5 {
		t.Fatalf("targets/duration wrong: %+v", args.Items)
	}
}

func TestCanvasRunGlobAndBatchFlags(t *testing.T) {
	s, _, hash := canvasRunFixture(t)
	call := canvasRunCall(t, "run --glob './*/video-1/' --mode video --concurrency 2 --max-retries 2 --canary", hash)
	args, ok, err := cloudAgentCanvasRunBatchArgs(s.repo, "user", "agent-canvas", call)
	if err != nil || !ok {
		t.Fatalf("glob run should expand: ok=%v err=%v", ok, err)
	}
	if len(args.Items) != 2 || args.Items[0].NodeID != "submap-1__video-1" || args.Items[1].NodeID != "submap-2__video-1" {
		t.Fatalf("glob selected wrong targets: %+v", args.Items)
	}
	if args.Concurrency != 2 || args.MaxRetries == nil || *args.MaxRetries != 2 || !args.Canary {
		t.Fatalf("batch flags not carried: %+v", args)
	}
}

func TestCanvasRunRejectsMixedCommandsAndStaleSnapshot(t *testing.T) {
	s, _, hash := canvasRunFixture(t)
	mixed := canvasRunCall(t, "run ./submap-1/video-1/ && rm ./submap-1__text-1", hash)
	if _, _, err := cloudAgentCanvasRunBatchArgs(s.repo, "user", "agent-canvas", mixed); err == nil || !strings.Contains(err.Error(), "只能有一条 run") {
		t.Fatalf("mixed run must be rejected: %v", err)
	}
	stale := canvasRunCall(t, "run ./submap-1/video-1/", "stale-hash")
	if _, _, err := cloudAgentCanvasRunBatchArgs(s.repo, "user", "agent-canvas", stale); err == nil || !strings.Contains(err.Error(), "画布已变化") {
		t.Fatalf("stale run must be rejected: %v", err)
	}
}

func TestCanvasRunApprovalSubmitsAndWritesBackPerSubmap(t *testing.T) {
	s, db, hash := canvasRunFixture(t)
	call := canvasRunCall(t, "run ./submap-1/video-1/ ./submap-2/video-1/", hash)
	run, state := canvasRunStart(t, s, call, 4)
	if err := s.executeCloudAgentToolCall(run, &state); err != nil {
		t.Fatal(err)
	}
	var tasks int64
	db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&tasks)
	if tasks != 0 {
		t.Fatal("run submitted tasks before approval")
	}
	waiting, err := s.CloudAgentRun("user", run.ID)
	if err != nil || waiting.Status != "waiting_approval" || waiting.Approval == nil {
		t.Fatalf("run should wait for one approval: status=%v err=%v", waiting.Status, err)
	}
	if waiting.Approval.Call.Function.Name != "canvas" || waiting.Approval.Preview.Kind != "media_batch" || len(waiting.Approval.Preview.Items) != 2 {
		t.Fatalf("run approval identity wrong: %#v", waiting.Approval)
	}
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, approved := agentMediaBatchState(t, s, run.ID)
	if _, err := s.PiToolAdvance("user", run.ID, "worker-a", approved.PiToolBatchTaskID, approved.Calls[0].ID); err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 2; step++ {
		if _, err := s.stepCloudAgentMediaBatch(ctx, "user", run.ID); err != nil {
			t.Fatal(err)
		}
	}
	tasks, orders := canvasVideoTaskCounts(t, db)
	if tasks != 2 || orders != 2 {
		t.Fatalf("expected one task/order per target: tasks=%d orders=%d", tasks, orders)
	}
	_, submitted := agentMediaBatchState(t, s, run.ID)
	for index, nodeID := range []string{"submap-1__video-1", "submap-2__video-1"} {
		meta, _ := agentCanvasNode(t, s, nodeID)["metadata"].(map[string]any)
		if stringValue(meta["taskId"]) != submitted.MediaBatch.Items[index].taskID() {
			t.Fatalf("%s not bound to its own task: %+v", nodeID, meta)
		}
		task, err := s.repo.TaskForUser("user", submitted.MediaBatch.Items[index].taskID())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(task.InputJSON, "【文本1】") {
			t.Fatalf("%s task input missing compiled text: %s", nodeID, task.InputJSON)
		}
		want := "原镜头文本"
		if index == 1 {
			want = "副本镜头文本"
		}
		if !strings.Contains(task.InputJSON, want) {
			t.Fatalf("%s task used the wrong submap text: %s", nodeID, task.InputJSON)
		}
	}
	for index, nodeID := range []string{"submap-1__video-1", "submap-2__video-1"} {
		resourceID := "run-output-" + []string{"one", "two"}[index]
		if err := db.Create(&model.Resource{ID: resourceID, UserID: "user", Kind: "video", Status: model.ResourceStatusReady, MimeType: "video/mp4", Width: 720, Height: 1280}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.Task{}).Where("id = ?", submitted.MediaBatch.Items[index].taskID()).Updates(map[string]any{
			"status": model.TaskStatusSucceeded, "result_json": `{"mode":"video","video":{"storageKey":"resource:` + resourceID + `"}}`,
		}).Error; err != nil {
			t.Fatal(err)
		}
		_ = nodeID
	}
	done := false
	for step := 0; step < 6 && !done; step++ {
		if done, err = s.stepCloudAgentMediaBatch(ctx, "user", run.ID); err != nil {
			t.Fatal(err)
		}
	}
	if !done {
		t.Fatal("run batch did not finish")
	}
	for index, nodeID := range []string{"submap-1__video-1", "submap-2__video-1"} {
		meta, _ := agentCanvasNode(t, s, nodeID)["metadata"].(map[string]any)
		want := "resource:run-output-" + []string{"one", "two"}[index]
		if meta["status"] != "success" || meta["storageKey"] != want {
			t.Fatalf("%s write-back wrong: %+v", nodeID, meta)
		}
	}
}
