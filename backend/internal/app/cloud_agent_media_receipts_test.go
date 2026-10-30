package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
)

func TestCloudAgentMediaCompletionReplaysCommittedResultAfterBackgroundAndCheckpointFailure(t *testing.T) {
	for _, later := range []string{"unrelated-edit", "delete-node", "delete-canvas", "changed-owner"} {
		t.Run(later, func(t *testing.T) {
			s, db, args := agentMediaFixture(t)
			run, _ := agentMediaPiRun(t, s, db, args, "request_approval")
			if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
				t.Fatal(err)
			}
			approveAgentMediaDraft(t, s, run.ID)
			run, state := agentInterjectionState(t, s, run.ID)
			taskID := state.MediaTaskID
			if taskID == "" {
				t.Fatal("approved task missing")
			}
			if later == "changed-owner" {
				canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
				if err != nil {
					t.Fatal(err)
				}
				doc, _ := creationDocument(canvas.PayloadJSON)
				doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "later-owner", "type": "script", "metadata": map[string]any{"storyboard": map[string]any{"rows": []any{map[string]any{"id": "new-row", "videoNodeId": args.NodeID}}}}})
				raw, _ := json.Marshal(doc)
				canvas.PayloadJSON = string(raw)
				if err = s.repo.UpsertCanvasProject(canvas); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Create(&model.Resource{ID: "receipt-output", UserID: "user", Kind: "video", Status: "ready", MimeType: "video/mp4"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.Task{}).Where("id = ?", taskID).Updates(map[string]any{"status": "succeeded", "result_json": `{"mode":"video","video":{"storageKey":"resource:receipt-output"}}`}).Error; err != nil {
				t.Fatal(err)
			}
			task, err := s.repo.Task(taskID)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.materializeCloudAgentStoryboardTask(task); err != nil {
				t.Fatal(err)
			}
			cs, _ := s.CanvasCommandServices()
			p := identity.Principal{ID: "user", OwnerID: "user", ClientID: "later-client", Role: "owner", Scopes: identity.OwnerScopes()}
			snap, err := cs.ReadSnapshot(context.Background(), p, "agent-canvas")
			if err != nil {
				t.Fatal(err)
			}
			resultRevision := snap.Revision
			op := commands.Operation{Type: "add_node", ID: "later-node", NodeType: "text"}
			if later == "delete-node" {
				op = commands.Operation{Type: "delete_node", ID: args.NodeID}
			} else if later == "delete-canvas" {
				op = commands.Operation{Type: "delete_canvas", ID: snap.CanvasID}
			}
			if _, err = cs.Execute(context.Background(), p, commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: p.ClientID, BaseRevision: snap.Revision, OperationID: "later-edit", Operations: []commands.Operation{op}}); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("injected Agent checkpoint interruption")
			injected := false
			if err = db.Callback().Update().Before("gorm:update").Register("test:media-checkpoint", func(tx *gorm.DB) {
				if tx.Statement.Table == "cloud_agent_executions" && !injected {
					injected = true
					tx.AddError(failure)
				}
			}); err != nil {
				t.Fatal(err)
			}
			// 异步生成由回写巡检完成：第一次回写在 Agent 检查点上失败并整体回滚，再次回写只落一次结果。
			if _, err = s.SettlePendingCloudAgentGenerations(); !errors.Is(err, failure) {
				t.Fatalf("checkpoint failure did not roll back: %v", err)
			}
			if err = db.Callback().Update().Remove("test:media-checkpoint"); err != nil {
				t.Fatal(err)
			}
			if settled, err := s.SettlePendingCloudAgentGenerations(); err != nil || settled != 1 {
				t.Fatalf("settle after rollback = %d, %v", settled, err)
			}
			_, state = agentInterjectionState(t, s, run.ID)
			event := agentCanvasPatchForOperation(t, state, "generate_media_complete")
			patch := event["canvasPatch"].(map[string]any)
			if patch["revision"] != float64(resultRevision) || patch["baseRevision"] != float64(resultRevision-1) {
				t.Fatalf("completion used a later canvas: %+v", patch)
			}
			nodes := creationMaps(patch["nodes"])
			if len(nodes) != 1 || nodes[0]["after"].(map[string]any)["id"] != args.NodeID {
				t.Fatalf("completion lost actual result or included a later edit: %+v", nodes)
			}
			after := nodes[0]["after"].(map[string]any)["metadata"].(map[string]any)
			if later != "changed-owner" && after["storageKey"] != "resource:receipt-output" {
				t.Fatalf("completion lost result: %+v", after)
			}
			if later == "changed-owner" {
				if after["storageKey"] == "resource:receipt-output" {
					t.Fatal("changed ownership adopted the result")
				}
				found := false
				for _, event := range state.Events {
					if event.Type == "generation_settled" && event.Payload["taskId"] == taskID && strings.Contains(stringValue(event.Payload["summary"]), "画布回写未完成") {
						found = true
					}
				}
				if !found {
					t.Fatal("committed pending cleanup lost its detached-result diagnostic")
				}
			}
			if err = advancePiAgentForTest(t, s, run.ID); err != nil {
				t.Fatal(err)
			}
			_, state = agentInterjectionState(t, s, run.ID)
			agentCanvasPatchForOperation(t, state, "generate_media_complete") // exactly one
			current, err := cs.ReadSnapshot(context.Background(), p, "agent-canvas")
			if later == "delete-canvas" {
				if err == nil {
					t.Fatal("completion restored a deleted canvas")
				}
			} else if err != nil || current.Revision != resultRevision+1 {
				t.Fatalf("replay wrote another revision: %+v %v", current, err)
			}
			if later == "delete-node" {
				for _, node := range creationMaps(current.Document["nodes"]) {
					if node["id"] == args.NodeID {
						t.Fatal("completion restored a deleted output")
					}
				}
			}
			var count int64
			if err = db.Model(&model.Task{}).Where("type = ?", "canvas_video").Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("replay created another generation: %d %v", count, err)
			}
			if err = db.Model(&model.CanvasTaskBinding{}).Where("task_id = ?", taskID).Update("resolution_version", 1).Error; err != nil {
				t.Fatal(err)
			}
			probe := cloudAgentRuntime{Request: state.Request}
			if err = emitCloudAgentMediaCompletion(s.repo, run, &probe, taskID, "{}"); err != nil {
				t.Fatal(err)
			}
			last := probe.Events[len(probe.Events)-1]
			if last.Payload["requiresRefresh"] != true || last.Payload["canvasPatch"] != nil || last.Payload["resultOperationId"] != "task-result:"+taskID+":resolution:1" {
				t.Fatalf("missing current recovery receipt fell back to an old result: %+v", last)
			}
		})
	}
}
