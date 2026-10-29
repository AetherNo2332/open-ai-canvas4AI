package app

import (
	"encoding/json"
	"strings"
	"testing"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/model"
)

func TestCloudAgentNewMediaVersionKeepsCurrentUntilApprovedAndFinished(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "manual-selection-wins"}[manual], func(t *testing.T) {
			s, db, args := agentMediaFixture(t)
			// Reuse a real ready reference as the output target; its old media must remain
			// usable both during approval and after task admission.
			capability := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceGrokImage), "grok-image")
			for _, row := range []any{
				&model.ChannelModel{ID: "image-cm", ChannelID: "channel", ModelKey: "grok-image", Capability: "image", Protocol: model.ChannelInterfaceGrokImage, CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, capability), BillingMode: "fixed_request", PriceConfigured: true, Enabled: true},
				&model.ChannelModelPriceTier{ID: "image-tier", ChannelModelID: "image-cm", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 1, PriceConfigured: true, Enabled: true},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			args.Mode = "image"
			args.ChannelModelKey = "grok-image"
			args.Duration = 0
			args.VideoGenerateAudio = nil
			args.NodeID = "hero"
			args.ReferenceNodeIDs = []string{"hero"}
			args.Intent = "new_version"
			run, staged := agentMediaPiRun(t, s, db, args, "request_approval")
			if _, _, err := s.prepareCloudAgentMedia(run, &staged, agentMediaCall(args)); err != nil {
				t.Fatalf("prepare version: %v", err)
			}
			if err := agentPiExecuteCallForTest(t, s, run.ID, "media-call"); err != nil {
				t.Fatal(err)
			}
			view, err := s.CloudAgentRun("user", run.ID)
			if err != nil || view.Approval == nil {
				t.Fatalf("missing version approval: %+v %v", view, err)
			}
			preview, _ := json.Marshal(view.Approval.Preview)
			if !strings.Contains(string(preview), "新版本") || !strings.Contains(string(preview), "旧版本") || strings.Contains(string(preview), "草稿节点和引用连线已创建") {
				t.Fatalf("misleading version preview: %s", preview)
			}
			if versions := agentNodeVersions(t, s, args.NodeID); len(versions) != 0 {
				t.Fatal("approval created a version")
			}
			assertAgentCurrentResource(t, s, args.NodeID, "resource:ref-two")
			approveAgentMediaDraft(t, s, run.ID)
			_, state := agentInterjectionState(t, s, run.ID)
			submitted := agentCanvasPatchForOperation(t, state, "generate_media_submit")["canvasPatch"].(map[string]any)
			changed := creationMaps(submitted["nodes"])
			if len(changed) != 1 || changed[0]["after"].(map[string]any)["id"] != args.NodeID {
				t.Fatalf("version submit lacks the actual pending node: %+v", submitted)
			}
			projection := changed[0]["after"].(map[string]any)["metadata"].(map[string]any)
			// 提交把节点当前媒体登记为版本 1（等价浏览器 recordCommittedMediaVersion），
			// 在途生成单独走 generating，不是版本条目；版本 1 的媒体与来源必须仍是旧媒体。
			submittedVersions := mediaversions.List(changed[0]["after"].(map[string]any))
			if projection["taskId"] == nil || projection["storageKey"] != "resource:ref-two" || len(submittedVersions) != 1 {
				t.Fatalf("version submit must keep old media and register only the current media as version 1: %+v", projection)
			}
			first := submittedVersions[0]
			firstMedia, _ := first["media"].(map[string]any)
			if first["source"] != "upload" || firstMedia["storageKey"] != "resource:ref-two" || projection["currentMediaVersionId"] != first["id"] {
				t.Fatalf("submitted version 1 must snapshot the pre-task current media: %+v", first)
			}
			if state.MediaTaskID == "" {
				t.Fatalf("approved version has no task: %+v", state.Events[len(state.Events)-1])
			}
			assertAgentCurrentResource(t, s, args.NodeID, "resource:ref-two")
			// The frozen request resolves self-reference to the old resource, without a
			// canvas self-edge. Its output later adopts through the shared materializer.
			task, err := s.repo.Task(state.MediaTaskID)
			if err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			_ = json.Unmarshal([]byte(task.InputJSON), &input)
			refs := creationMaps(input["referenceImages"])
			if len(refs) != 1 || refs[0]["storageKey"] != "resource:ref-two" {
				t.Fatalf("self-reference not frozen: %+v", refs)
			}
			canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
			doc, _ := creationDocument(canvas.PayloadJSON)
			for _, edge := range creationMaps(doc["connections"]) {
				if edge["fromNodeId"] == args.NodeID && edge["toNodeId"] == args.NodeID {
					t.Fatal("self-reference created self-edge")
				}
			}
			if manual {
				read := cloudAgentCall{}
				read.Function.Name = "canvas_get_state"
				read.Function.Arguments = `{"view":"media_versions","nodeIds":["hero"]}`
				out, err := cloudAgentReadTool(s.repo, "user", &state, read, s)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(out)
				var history struct {
					SnapshotHash string `json:"snapshotHash"`
					Versions     struct {
						Generating struct {
							TaskID string `json:"taskId"`
						} `json:"generating"`
						Items []struct {
							ID string `json:"id"`
						} `json:"items"`
					} `json:"mediaVersions"`
				}
				_ = json.Unmarshal(raw, &history)
				if history.Versions.Generating.TaskID != task.ID || len(history.Versions.Items) != 1 || strings.Contains(string(raw), "beforeVersion") {
					t.Fatalf("read must report generation apart from versions: %s", raw)
				}
				// The user adopts an older version in the browser while the task runs.
				canvas, _ := s.repo.CanvasProjectForUser("user", "agent-canvas")
				doc, _ := creationDocument(canvas.PayloadJSON)
				nodes, _ := creationObjects(doc["nodes"])
				meta := cloudAgentNodeMetadata(nodes["hero"])
				meta["mediaVersions"] = []any{map[string]any{"id": "manual-v1", "number": 1, "createdAt": "2026-10-01T00:00:00.000Z", "source": "upload", "media": map[string]any{"storageKey": "resource:ref-two", "content": "/api/resources/ref-two/file"}}}
				meta["currentMediaVersionId"] = "manual-v1"
				delete(meta, "taskId")
				encoded, _ := json.Marshal(doc)
				canvas.PayloadJSON = string(encoded)
				if err := s.repo.UpsertCanvasProject(canvas); err != nil {
					t.Fatal(err)
				}
				history.SnapshotHash = cloudAgentCanvasHash(doc)
				selectCall := cloudAgentCall{ID: "select-during-generation"}
				selectCall.Function.Name = "canvas_apply_ops"
				body, _ := json.Marshal(map[string]any{"snapshotHash": history.SnapshotHash, "ops": []any{map[string]any{"type": "use_media_version", "id": "hero", "versionId": "manual-v1"}}})
				selectCall.Function.Arguments = string(body)
				policy, err := s.RuntimePolicy()
				if err != nil {
					t.Fatal(err)
				}
				if _, err = applyCloudAgentCanvas(s.repo, "user", "agent-canvas", selectCall, policy); err != nil {
					t.Fatal(err)
				}
			}
			if err = db.Create(&model.Resource{ID: "version-output", UserID: "user", Kind: "image", MimeType: "image/png", Status: model.ResourceStatusReady}).Error; err != nil {
				t.Fatal(err)
			}
			if err = db.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"images":[{"resourceId":"version-output"}]}`}).Error; err != nil {
				t.Fatal(err)
			}
			if err = s.materializeCloudAgentStoryboardTask(func() *model.Task { r, _ := s.repo.Task(task.ID); return r }()); err != nil {
				t.Fatal(err)
			}
			expectedResource, expectedVersions := "resource:version-output", 2
			if manual {
				expectedResource = "resource:ref-two"
			}
			assertAgentCurrentResource(t, s, args.NodeID, expectedResource)
			versions := agentNodeVersions(t, s, args.NodeID)
			if len(versions) != expectedVersions {
				t.Fatalf("node history after writeback: %+v", versions)
			}
			newest := versions[len(versions)-1]
			if generation, _ := newest["generation"].(map[string]any); generation["taskId"] != task.ID || newest["source"] != "generate" {
				t.Fatalf("agent version not traceable by generation.taskId: %+v", newest)
			}
			if err = advancePiAgentForTest(t, s, run.ID); err != nil {
				t.Fatal(err)
			}
			_, completed := agentInterjectionState(t, s, run.ID)
			found := false
			for _, event := range completed.Events {
				result, ok := event.Payload["result"].(map[string]any)
				if !ok || result["taskId"] != task.ID || result["phase"] != "completion" {
					continue
				}
				expected := "生成结果已回写画布节点"
				if manual {
					expected = "新版本已加入节点版本历史，保留手动选择的当前版本"
				}
				found = event.Type == "tool_completed" && result["summary"] == expected && result["adopted"] == !manual && result["versionId"] == newest["id"]
			}
			if !found {
				t.Fatal("completion did not report the persisted adoption decision")
			}
		})
	}
}

func assertAgentCurrentResource(t *testing.T, s *Service, nodeID, key string) {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := creationObjects(doc["nodes"])
	if cloudAgentNodeMetadata(nodes[nodeID])["storageKey"] != key {
		t.Fatalf("current media changed: %+v", nodes[nodeID])
	}
}

func agentNodeVersions(t *testing.T, s *Service, nodeID string) []map[string]any {
	t.Helper()
	canvas, err := s.repo.CanvasProjectForUser("user", "agent-canvas")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := creationDocument(canvas.PayloadJSON)
	if err != nil {
		t.Fatal(err)
	}
	nodes, _ := creationObjects(doc["nodes"])
	return mediaversions.List(nodes[nodeID])
}
