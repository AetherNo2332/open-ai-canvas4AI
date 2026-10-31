package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

func queuePiMediaToolForTest(t *testing.T, s *Service, db *gorm.DB, args cloudAgentMediaArgs, permission string) (*model.CloudAgentExecution, cloudAgentRuntime) {
	t.Helper()
	req := agentTestRequest()
	req.PermissionMode = permission
	req.Budget.MaxGenerationTasks = 2
	req.Budget.MaxVideoSeconds = 24
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	_, state := agentStartPiModelStep(t, s, root.ID)
	// The SDK accepts an omitted optional array or [], never JSON null.
	if args.ReferenceTransientIDs == nil {
		args.ReferenceTransientIDs = []string{}
	}
	body, err := json.Marshal(map[string]any{"toolCalls": []cloudAgentCall{agentMediaCall(args)}, "stopReasonKind": cloudAgentStopKindStop})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.Task{}).Where("id = ?", state.ActiveTaskID).Updates(map[string]any{
		"status": model.TaskStatusSucceeded, "result_json": string(body),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := advancePiAgentForTest(t, s, root.ID); err != nil {
		t.Fatal(err)
	}
	run, queued := agentInterjectionState(t, s, root.ID)
	if queued.ActiveTaskID != "" || len(queued.Calls) != 1 || queued.CallIndex != 0 {
		t.Fatalf("Pi media batch was not queued: active=%s calls=%d index=%d", queued.ActiveTaskID, len(queued.Calls), queued.CallIndex)
	}
	return run, queued
}

func TestCloudAgentMediaCleanupUsesPersistedTaskTarget(t *testing.T) {
	for _, missingTarget := range []bool{false, true} {
		t.Run(map[bool]string{false: "known target", true: "unknown target"}[missingTarget], func(t *testing.T) {
			s, db, args := agentMediaFixture(t)
			run, _ := queuePiMediaToolForTest(t, s, db, args, "request_approval")
			if err := advancePiAgentForTest(t, s, run.ID); err != nil {
				t.Fatal(err)
			}
			approveAgentMediaDraft(t, s, run.ID)
			run, state := agentInterjectionState(t, s, run.ID)
			updates := map[string]any{"status": model.TaskStatusFailed, "error": "上游超时"}
			if missingTarget {
				updates["input_json"] = `{}`
			}
			if err := db.Model(&model.Task{}).Where("id = ?", state.MediaTaskID).Updates(updates).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CloudAgentExecution{}).Where("id = ?", run.ID).Updates(map[string]any{"status": "failed", "cleanup_pending": true, "state_json": `{`}).Error; err != nil {
				t.Fatal(err)
			}
			run, err := s.repo.CloudAgent("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.finishCloudAgentCleanup(context.Background(), run); err != nil {
				t.Fatal(err)
			}
			run, err = s.repo.CloudAgent("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if run.CleanupPending || run.MediaTaskID != "" {
				t.Fatal("cleanup checkpoint did not finish")
			}
			canvas, err := s.repo.CanvasProjectForUser("user", state.Request.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := creationDocument(canvas.PayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			nodes, _ := creationObjects(doc["nodes"])
			meta := nodes[args.NodeID]["metadata"].(map[string]any)
			if missingTarget {
				if meta["status"] != "error" {
					t.Fatal("durable version target did not recover missing request metadata")
				}
			} else if meta["status"] != "error" {
				t.Fatal("known target did not receive terminal status")
			}
		})
	}
}

func TestCloudAgentMediaCompletionKeepsGenerationAndWritebackFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status model.TaskStatus
		err    string
		change string
		reason string
	}{
		{"failed task and deleted node", model.TaskStatusFailed, "上游拒绝该生成规格", "delete", "target_node_missing"},
		{"failed task and replaced binding", model.TaskStatusFailed, "上游请求超时", "rebind", "task_binding_changed"},
		{"successful task and deleted node", model.TaskStatusSucceeded, "", "delete", "target_node_missing"},
		{"successful task without resource", model.TaskStatusSucceeded, "", "", "result_resource_unavailable"},
		{"long task error remains readable", model.TaskStatusFailed, strings.Repeat("上游拒绝该生成规格", 40), "delete", "target_node_missing"},
		{"private task error remains redacted", model.TaskStatusFailed, "upstream https://private.invalid/?token=private-value", "delete", "target_node_missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, db, args := agentMediaFixture(t)
			run, _ := queuePiMediaToolForTest(t, s, db, args, "request_approval")
			if err := advancePiAgentForTest(t, s, run.ID); err != nil {
				t.Fatal(err)
			}
			approveAgentMediaDraft(t, s, run.ID)
			run, state := agentInterjectionState(t, s, run.ID)
			taskID := state.MediaTaskID
			if taskID == "" {
				t.Fatal("media task missing")
			}
			if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": test.status, "error": test.err, "result_json": `{}`}).Error; err != nil {
				t.Fatal(err)
			}
			canvas, err := s.repo.CanvasProjectForUser("user", state.Request.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			doc, err := creationDocument(canvas.PayloadJSON)
			if err != nil {
				t.Fatal(err)
			}
			nodes := creationMaps(doc["nodes"])
			updated := make([]map[string]any, 0, len(nodes))
			for _, node := range nodes {
				if stringValue(node["id"]) == args.NodeID {
					if test.change == "delete" {
						continue
					}
					if test.change == "rebind" {
						node["metadata"].(map[string]any)["taskId"] = "replacement-task"
					}
				}
				updated = append(updated, node)
			}
			// A copied task ID on another node must not redirect the writeback.
			if test.change == "rebind" {
				updated = append(updated, map[string]any{"id": "different-node", "type": "video", "metadata": map[string]any{"taskId": taskID, "content": "keep-existing", "status": "success"}})
			}
			doc["nodes"] = updated
			before, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CanvasProject{}).Where("id = ?", canvas.ID).Update("payload_json", string(before)).Error; err != nil {
				t.Fatal(err)
			}
			if err := advancePiAgentForTest(t, s, run.ID); err != nil {
				t.Fatal(err)
			}
			run, state = agentInterjectionState(t, s, run.ID)
			// 异步生成：结果（含生成失败与回写失败）以 generation_settled 事件和一条系统通知送达，
			// 回写失败不再判死整轮——生成已计费，作品保留在资源库，由 Agent 如实告诉用户。
			var settled *CloudAgentEvent
			for index := range state.Events {
				if state.Events[index].Type == "generation_settled" && state.Events[index].Payload["taskId"] == taskID {
					settled = &state.Events[index]
				}
			}
			if settled == nil || settled.Payload["status"] != string(test.status) || settled.Payload["callId"] == "" {
				t.Fatalf("completion lost distinct outcome or target: %+v", settled)
			}
			if run.Status == "failed" || len(state.PendingGenerations) != 0 {
				t.Fatalf("writeback failure must not fail the run: status=%s pending=%d", run.Status, len(state.PendingGenerations))
			}
			if len(state.PendingInterjections) != 1 || state.PendingInterjections[0].Source != cloudAgentGenerationSource {
				t.Fatalf("outcome must be queued as one generation notice: %+v", state.PendingInterjections)
			}
			notice := state.PendingInterjections[0].Text
			if test.status == model.TaskStatusFailed {
				if !strings.Contains(notice, "生成未成功") || strings.Contains(notice, "生成成功") {
					t.Fatalf("writeback masked generation failure: %s", notice)
				}
			} else if !strings.Contains(notice, "生成成功，但画布回写未完成") {
				t.Fatalf("successful generation with failed writeback reported wrongly: %s", notice)
			}
			serialized, _ := json.Marshal(settled.Payload)
			if strings.Contains(string(serialized)+notice, "private-value") || strings.Contains(string(serialized)+notice, "private.invalid") {
				t.Fatal("provider credentials leaked into Agent events")
			}
			if diagnostic := taskExecutionDiagnosticForTest(t, s, taskID); diagnostic.WritebackReason != test.reason {
				t.Fatalf("writeback reason = %q, want %q", diagnostic.WritebackReason, test.reason)
			}
			if test.change != "" {
				after, err := s.repo.CanvasProjectForUser("user", canvas.ID)
				if err != nil {
					t.Fatal(err)
				}
				if after.PayloadJSON != string(before) {
					t.Fatal("missing or rebound target modified another node")
				}
			}
			if err := advancePiAgentForTest(t, s, run.ID); err != nil {
				t.Fatal(err)
			}
			var count int64
			if err := db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatal("completion failure resubmitted billable media")
			}
		})
	}
}

func TestCloudAgentCapabilityGuidePublishesConnectionContracts(t *testing.T) {
	guide := cloudAgentCapabilityGuide()
	for _, nodeType := range []string{"script", "markdown", "text", "image", "video"} {
		descriptor, ok := cloudAgentNodeCapabilityForType(nodeType)
		if !ok {
			t.Fatalf("missing capability %s", nodeType)
		}
		found := false
		for _, line := range strings.Split(guide, "\n") {
			if !strings.Contains(line, "（"+nodeType+"）：") {
				continue
			}
			_, raw, ok := strings.Cut(line, " 连线能力：")
			if !ok {
				t.Fatalf("connection contract missing for %s", nodeType)
			}
			var contract map[string]any
			if err := json.Unmarshal([]byte(raw), &contract); err != nil {
				t.Fatal(err)
			}
			if contract["canSource"] != descriptor.Connection.CanSource || contract["canTarget"] != descriptor.Connection.CanTarget || contract["inputKind"] != descriptor.InputKind {
				t.Fatalf("guide differs from execution registry for %s", nodeType)
			}
			found = true
		}
		if !found {
			t.Fatalf("node %s missing from guide", nodeType)
		}
	}
}

func taskExecutionDiagnosticForTest(t *testing.T, s *Service, taskID string) model.TaskExecutionDiagnostic {
	t.Helper()
	task, err := s.repo.TaskForUser("user", taskID)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := taskExecutionDiagnostic(task)
	if diagnostic == nil {
		t.Fatal("task has no execution diagnostic")
	}
	return *diagnostic
}
