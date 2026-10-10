package app

import (
	"context"
	"encoding/json"
	"fmt"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrevisRendererClientRetriesOnlyTransientFailure(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(status)
					fmt.Fprint(w, `{"code":"renderer_unavailable"}`)
					return
				}
				fmt.Fprint(w, `{"status":"output_ready"}`)
			}))
			defer server.Close()
			client := &previsRendererClient{base: server.URL, client: server.Client()}
			view, err := client.json(context.Background(), http.MethodPost, "/jobs/same-task/start", nil)
			if status == http.StatusServiceUnavailable && (err != nil || view.Status != "output_ready" || calls != 2) {
				t.Fatalf("transient: calls=%d err=%v", calls, err)
			}
			if status == http.StatusForbidden && (err == nil || calls != 1) {
				t.Fatal("deterministic rejection was retried")
			}
		})
	}
}

func submittedPrevis(t *testing.T) (*Service, *model.CloudAgentExecution, *model.Task, previsRenderInput) {
	t.Helper()
	s, run, state := backgroundPrevisFixture(t, "auto")
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	_, state = reloadPiRun(t, s, run.ID)
	task, err := s.repo.TaskForUser(run.UserID, state.MediaTaskID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.repo.ClaimNextTask("previs-test-worker", time.Minute)
	if err != nil || task == nil {
		t.Fatal(err)
	}
	var input previsRenderInput
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	return s, run, task, input
}

func TestBackgroundPrevisWaitsForRendererLeaseOnTakeover(t *testing.T) {
	s, _, task, _ := submittedPrevis(t)
	submits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/jobs" {
			submits++
			if submits == 1 {
				w.WriteHeader(http.StatusConflict)
				fmt.Fprint(w, `{"code":"lease_lost"}`)
				return
			}
			fmt.Fprint(w, `{"status":"preparing"}`)
			return
		}
		fmt.Fprint(w, `{"status":"failed","code":"asset_load_failed"}`)
	}))
	defer server.Close()
	t.Setenv("CANVAS_PREVIS_RENDERER_URL", server.URL)
	t.Setenv("CANVAS_PREVIS_RENDERER_TOKEN", strings.Repeat("t", 40))
	worker := &taskWorkerCoordinator{service: s}
	if err := worker.processPrevisRender(task, context.Background()); err != nil {
		t.Fatal(err)
	}
	latest, err := s.repo.Task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if submits != 2 || latest.Error != "asset_load_failed" {
		t.Fatalf("backend takeover did not wait for renderer lease expiry: submits=%d status=%s code=%s", submits, latest.Status, latest.Error)
	}
}

// Runs the real backend lease worker and renderer with no user webpage or Pi process.
func TestBackgroundPrevisRealThreeShots(t *testing.T) {
	if os.Getenv("PREVIS_REAL_BACKEND") == "" {
		t.Skip("requires isolated renderer and ffprobe")
	}
	s, run, state := backgroundPrevisFixture(t, "auto")
	canvas, _ := s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	scene := creationMaps(doc["previsScenes"])[0]
	shots := creationMaps(scene["shots"])
	threeShots := []any{}
	for index, move := range []string{"static", "push_in", "pan_right"} {
		raw, _ := json.Marshal(shots[0])
		var shot map[string]any
		json.Unmarshal(raw, &shot)
		shot["id"] = fmt.Sprintf("scene-shot-%d", index+1)
		shot["cameraMove"] = move
		threeShots = append(threeShots, shot)
	}
	scene["shots"] = threeShots
	scene["activeShotId"] = "scene-shot-1"
	canvasRaw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(canvasRaw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	shots = creationMaps(scene["shots"])
	calls := make([]cloudAgentCall, 3)
	for index := 0; index < 3; index++ {
		calls[index] = previsMutationCall(t, fmt.Sprintf("e2e-call-%d-%d", time.Now().UnixNano(), index), "previs_preview", map[string]any{"sceneId": "scene", "shotId": shots[index]["id"], "duration": 0.5, "fps": 8})
	}
	state.Calls = calls
	state.Canonical.Messages[len(state.Canonical.Messages)-1]["tool_calls"] = calls
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatal(err)
	}
	outputDir := os.Getenv("PREVIS_E2E_OUTPUT")
	if outputDir != "" {
		os.MkdirAll(outputDir, 0750)
	}
	for index := 0; index < 3; index++ {
		run, state = reloadPiRun(t, s, run.ID)
		if err := s.executeCloudAgentToolCall(run, state); err != nil {
			t.Fatal(err)
		}
		run, state = reloadPiRun(t, s, run.ID)
		task, err := s.repo.ClaimNextTask("e2e-worker", 45*time.Second)
		if err != nil || task == nil || task.ID != state.MediaTaskID {
			t.Fatal("task not durably queued")
		}
		if index == 2 {
			if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
				current.Status = "failed"
				current.CleanupPending = true
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			run, _ = s.repo.CloudAgent(run.UserID, run.ID)
			if err := s.finishCloudAgentCleanup(context.Background(), run); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.taskWorker().processClaimedTask(task, nil); err != nil {
			t.Fatal(err)
		}
		completed, _ := s.repo.Task(task.ID)
		if completed.Status != model.TaskStatusSucceeded {
			t.Fatalf("%d: %s %s", index, completed.Status, completed.Error)
		}
		var result previsRenderResult
		if json.Unmarshal([]byte(completed.ResultJSON), &result) != nil || !result.OutputReady || !result.Linked || result.FrameCount != 4 {
			t.Fatal(completed.ResultJSON)
		}
		for _, item := range []struct{ id, extension string }{{result.ResourceID, "mp4"}, {result.PreviewResourceID, "png"}} {
			resource, reader, err := s.OpenResource(run.UserID, item.id)
			if err != nil || resource.Size < 100 {
				t.Fatal("unavailable result")
			}
			if outputDir != "" {
				file, err := os.Create(filepath.Join(outputDir, fmt.Sprintf("shot-%d.%s", index+1, item.extension)))
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(file, reader)
				file.Close()
			}
			reader.Close()
		}
		t.Logf("shot=%d task=%s video=%s preview=%s nodes=%s,%s frames=%d durationMs=%d", index+1, task.ID, result.ResourceID, result.PreviewResourceID, result.VideoNodeID, result.PreviewNodeID, result.FrameCount, result.DurationMs)
		if index < 2 {
			run, state = reloadPiRun(t, s, run.ID)
			if err := s.executeCloudAgentToolCall(run, state); err != nil {
				t.Fatal(err)
			}
		}
	}
	canvas, _ = s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
	doc = mustCreationDocument(t, canvas.PayloadJSON)
	if len(creationMaps(doc["nodes"])) != 7 {
		t.Fatal("missing delivered nodes")
	}
	if outputDir != "" {
		os.WriteFile(filepath.Join(outputDir, "canvas.json"), []byte(canvas.PayloadJSON), 0640)
	}
	final, _ := s.repo.CloudAgent(run.UserID, run.ID)
	if final.Status != "failed" {
		t.Fatal("background completion revived Pi run")
	}
}

func TestBackgroundPrevisRealWritebackRecovery(t *testing.T) {
	if os.Getenv("PREVIS_REAL_BACKEND") == "" {
		t.Skip("requires isolated renderer and ffprobe")
	}
	s, run, task, input := submittedPrevis(t)
	canvas, _ := s.repo.CanvasProjectForUser(run.UserID, input.CanvasID)
	originalPayload := canvas.PayloadJSON
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	creationMaps(creationMaps(doc["previsScenes"])[0]["cameras"])[0]["fov"] = 75
	raw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(raw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	if err := s.taskWorker().processClaimedTask(task, nil); err != nil {
		t.Fatal(err)
	}
	source, _ := s.repo.Task(task.ID)
	var original previsRenderResult
	if source.Status != model.TaskStatusFailed || source.Error != "target_changed" || json.Unmarshal([]byte(source.ResultJSON), &original) != nil || !original.OutputReady {
		t.Fatalf("failed output facts missing: %s %s", source.Status, source.Error)
	}
	canvas, _ = s.repo.CanvasProjectForUser(run.UserID, input.CanvasID)
	if len(creationMaps(mustCreationDocument(t, canvas.PayloadJSON)["nodes"])) != 1 {
		t.Fatal("conflict wrote successful nodes")
	}
	canvas.PayloadJSON = originalPayload
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	repair, err := s.RecoverPrevisWriteback(task.UserID, task.ID, "link")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.repo.ClaimNextTask("e2e-repair-worker", 45*time.Second)
	if err != nil || claimed == nil || claimed.ID != repair.ID {
		t.Fatal("repair not queued")
	}
	if err := s.taskWorker().processClaimedTask(claimed, nil); err != nil {
		t.Fatal(err)
	}
	completed, _ := s.repo.Task(repair.ID)
	var result previsRenderResult
	if completed.Status != model.TaskStatusSucceeded || json.Unmarshal([]byte(completed.ResultJSON), &result) != nil || result.ResourceID != original.ResourceID || result.PreviewResourceID != original.PreviewResourceID || result.RenderAttempts != original.RenderAttempts {
		t.Fatal("save-only recovery rerendered or duplicated resources")
	}
	source, _ = s.repo.Task(task.ID)
	if source.Status != model.TaskStatusFailed {
		t.Fatal("repair overwrote failed history")
	}
	canvas, _ = s.repo.CanvasProjectForUser(run.UserID, input.CanvasID)
	if len(creationMaps(mustCreationDocument(t, canvas.PayloadJSON)["nodes"])) != 3 {
		t.Fatal("recovery delivery missing")
	}
	if dir := os.Getenv("PREVIS_E2E_OUTPUT"); dir != "" {
		os.WriteFile(filepath.Join(dir, "recovery-canvas.json"), []byte(canvas.PayloadJSON), 0640)
		for _, item := range []struct{ id, ext string }{{result.ResourceID, "mp4"}, {result.PreviewResourceID, "png"}} {
			_, reader, err := s.OpenResource(task.UserID, item.id)
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.Create(filepath.Join(dir, "recovery."+item.ext))
			if err != nil {
				t.Fatal(err)
			}
			_, copyErr := io.Copy(file, reader)
			file.Close()
			reader.Close()
			if copyErr != nil {
				t.Fatal(copyErr)
			}
		}
	}
	t.Logf("failedTask=%s repairTask=%s sameVideo=%s samePreview=%s renderAttempts=%d", source.ID, repair.ID, result.ResourceID, result.PreviewResourceID, result.RenderAttempts)
}

func TestBackgroundPrevisPiFailureKeepsAuthorizedTaskButUserCancelStopsIt(t *testing.T) {
	s, run, task, _ := submittedPrevis(t)
	run, _ = s.repo.CloudAgent(run.UserID, run.ID)
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		current.Status = "failed"
		current.CleanupPending = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	run, _ = s.repo.CloudAgent(run.UserID, run.ID)
	if err := s.finishCloudAgentCleanup(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	latest, _ := s.repo.Task(task.ID)
	if latest.Status != model.TaskStatusRunning {
		t.Fatal("Pi failure cancelled authorized previs")
	}
	if err := s.CancelCloudAgent(context.Background(), run.UserID, run.ID); err != nil {
		t.Fatal(err)
	}
	latest, _ = s.repo.Task(task.ID)
	if latest.Status != model.TaskStatusCancelled {
		t.Fatal("explicit stop failed after Pi terminal")
	}
}

func storedPrevis(t *testing.T, s *Service, task *model.Task, input previsRenderInput) previsRenderResult {
	t.Helper()
	video, _, err := s.storeResource(task.UserID, "media", "previs.mp4", "video/mp4", 1000, input.Width, input.Height, 500, strings.NewReader(strings.Repeat("v", 1000)), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	preview, _, err := s.storeResource(task.UserID, "image", "previs.png", "image/png", 1000, input.Width, input.Height, 0, strings.NewReader(strings.Repeat("p", 1000)), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	result := previsRenderResult{ResourceID: video.ID, PreviewResourceID: preview.ID, OutputReady: true, Width: input.Width, Height: input.Height, DurationMs: 500, SceneID: input.SceneID, ShotID: input.ShotID, SourceHash: input.SourceHash}
	if err := s.checkpointPrevisOutput(task, result); err != nil {
		t.Fatal(err)
	}
	return result
}

// D6（修复计划 G2）：浏览器 preview-requested 监听与服务器渲染任务并存时以服务器回写为准。
// 前端已无派发方（agent 预演只走后台任务），这里守住服务器侧语义：同一镜头先后两次服务端
// 渲染，镜头指针必须重指最新任务的产物，旧产物节点保留为历史，不出现重复或覆盖损坏。
func TestBackgroundPrevisReRenderRepointsShotToLatestServerWrite(t *testing.T) {
	s, run, state := backgroundPrevisFixture(t, "auto")
	canvas, _ := s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
	scene := creationMaps(mustCreationDocument(t, canvas.PayloadJSON)["previsScenes"])[0]
	shotID := stringValue(creationMaps(scene["shots"])[0]["id"])
	// 同镜头再渲染一次：第二个 previs_preview 调用排在同一轮的调用清单里。
	state.Calls = append(state.Calls, previsMutationCall(t, "render-call-2", "previs_preview", map[string]any{"sceneId": "scene", "shotId": shotID, "duration": 0.5, "fps": 8}))
	state.Canonical.Messages[len(state.Canonical.Messages)-1]["tool_calls"] = state.Calls
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatal(err)
	}

	commit := func() previsRenderResult {
		t.Helper()
		// 第一次提交直接执行当前调用；上一次提交后要先消化回执，下一个调用才会创建任务。
		for attempt := 0; attempt < 3; attempt++ {
			run, state = reloadPiRun(t, s, run.ID)
			if err := s.executeCloudAgentToolCall(run, state); err != nil {
				t.Fatal(err)
			}
			run, state = reloadPiRun(t, s, run.ID)
			if state.MediaTaskID != "" {
				break
			}
		}
		if state.MediaTaskID == "" {
			t.Fatal("re-render did not create a durable render task")
		}
		task, err := s.repo.TaskForUser(run.UserID, state.MediaTaskID)
		if err != nil {
			t.Fatal(err)
		}
		task, err = s.repo.ClaimNextTask("previs-test-worker", time.Minute)
		if err != nil || task == nil {
			t.Fatal(err)
		}
		var input previsRenderInput
		if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
			t.Fatal(err)
		}
		result := storedPrevis(t, s, task, input)
		if err := s.commitPrevisOutput(task, input, result); err != nil {
			t.Fatal(err)
		}
		// commitPrevisOutput 按值接收 result：节点 ID 以任务终态 ResultJSON 里的权威值为准。
		completed, err := s.repo.Task(task.ID)
		if err != nil {
			t.Fatal(err)
		}
		var stored previsRenderResult
		if err := json.Unmarshal([]byte(completed.ResultJSON), &stored); err != nil {
			t.Fatal(err)
		}
		return stored
	}
	first := commit()
	second := commit()

	canvas, _ = s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	nodes := creationMaps(doc["nodes"])
	shot, _ := findPrevisShot(creationMaps(doc["previsScenes"])[0], shotID)
	if stringValue(shot["previewNodeId"]) != second.PreviewNodeID || stringValue(shot["clayVideoNodeId"]) != second.VideoNodeID {
		t.Fatalf("re-render did not repoint the shot to the latest server write: %#v", shot)
	}
	byID := map[string]bool{}
	for _, node := range nodes {
		if byID[stringValue(node["id"])] {
			t.Fatalf("duplicate node after re-render: %s", canvas.PayloadJSON)
		}
		byID[stringValue(node["id"])] = true
	}
	for _, stale := range []string{first.VideoNodeID, first.PreviewNodeID} {
		if !byID[stale] {
			t.Fatalf("first render product was clobbered: %s", canvas.PayloadJSON)
		}
	}
	if len(nodes) != 5 {
		t.Fatalf("unexpected node count after re-render: %d %s", len(nodes), canvas.PayloadJSON)
	}
}

func TestBackgroundPrevisWritebackMergesUnrelatedEditAndReplaysReceipt(t *testing.T) {
	s, run, task, input := submittedPrevis(t)
	result := storedPrevis(t, s, task, input)
	canvas, _ := s.repo.CanvasProjectForUser(task.UserID, input.CanvasID)
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "manual", "type": "text", "content": "keep me"})
	creationMaps(doc["previsScenes"])[0]["activeShotId"] = "other"
	raw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(raw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	if err := s.commitPrevisOutput(task, input, result); err != nil {
		t.Fatal(err)
	}
	finished, _ := s.repo.Task(task.ID)
	if finished.Status != model.TaskStatusSucceeded {
		t.Fatal(finished.Status)
	}
	canvas, _ = s.repo.CanvasProjectForUser(task.UserID, input.CanvasID)
	doc = mustCreationDocument(t, canvas.PayloadJSON)
	if len(creationMaps(doc["nodes"])) != 4 {
		t.Fatal(canvas.PayloadJSON)
	}
	run, state := reloadPiRun(t, s, run.ID)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	_, state = reloadPiRun(t, s, run.ID)
	if state.MediaTaskID != "" || state.CallIndex != 1 {
		t.Fatal("missing durable receipt")
	}
}

func TestBackgroundPrevisWritebackFailsLoudWithoutWorkstation(t *testing.T) {
	s, _, task, input := submittedPrevis(t)
	result := storedPrevis(t, s, task, input)
	canvas, _ := s.repo.CanvasProjectForUser(task.UserID, input.CanvasID)
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	// 工作站被删掉（存量脏数据且组内没有可提升的 video）：写回必须显式失败，不得静默造裸节点。
	doc["nodes"] = []any{}
	raw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(raw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	err := s.commitPrevisOutput(task, input, result)
	if err == nil || !strings.Contains(err.Error(), "previs_workstation_missing") {
		t.Fatalf("missing workstation must fail loud, got %v", err)
	}
	stored, _ := s.repo.Task(task.ID)
	if stored.Status == model.TaskStatusSucceeded {
		t.Fatal("fail-loud writeback reported success")
	}
	persisted, _ := s.repo.CanvasProjectForUser(task.UserID, input.CanvasID)
	if len(creationMaps(mustCreationDocument(t, persisted.PayloadJSON)["nodes"])) != 0 {
		t.Fatal("fail-loud writeback still wrote bare nodes")
	}
}

func TestBackgroundPrevisWritebackRepairsLegacyDirtyNodes(t *testing.T) {
	s, _, task, input := submittedPrevis(t)
	result := storedPrevis(t, s, task, input)
	canvas, _ := s.repo.CanvasProjectForUser(task.UserID, input.CanvasID)
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	// 旧版回写形态：只有 previsSceneId、没有 workflowKind 的 video/image 产物节点，且场景无工作站。
	// 画布保存会校验素材归属，所以脏节点引用真实资源，与生产回写的节点数据一致。
	legacyVideoResource, _, err := s.storeResource(task.UserID, "media", "legacy.mp4", "video/mp4", 1000, 640, 360, 500, strings.NewReader(strings.Repeat("v", 1000)), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	legacyImageResource, _, err := s.storeResource(task.UserID, "image", "legacy.png", "image/png", 1000, 640, 360, 0, strings.NewReader(strings.Repeat("p", 1000)), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	doc["nodes"] = []any{
		map[string]any{"id": "legacy-video", "type": "video", "title": "白模预演", "metadata": map[string]any{"content": resourceFileURL(legacyVideoResource.ID), "storageKey": "resource:" + legacyVideoResource.ID, "status": "success", "previsSceneId": input.SceneID, "previsShotId": input.ShotID, "previsRepairTaskId": "old-task"}},
		map[string]any{"id": "legacy-image", "type": "image", "title": "预演构图帧", "metadata": map[string]any{"content": resourceFileURL(legacyImageResource.ID), "storageKey": "resource:" + legacyImageResource.ID, "status": "success", "previsSceneId": input.SceneID, "previsShotId": input.ShotID, "previsRepairTaskId": "old-task"}},
	}
	raw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(raw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	if err := s.commitPrevisOutput(task, input, result); err != nil {
		t.Fatal(err)
	}
	canvas, _ = s.repo.CanvasProjectForUser(task.UserID, input.CanvasID)
	doc = mustCreationDocument(t, canvas.PayloadJSON)
	nodes := creationMaps(doc["nodes"])
	if len(nodes) != 4 {
		t.Fatalf("unexpected node count after repair: %d %s", len(nodes), canvas.PayloadJSON)
	}
	byID := map[string]map[string]any{}
	workstations, products, bare := 0, 0, 0
	for _, node := range nodes {
		metadata, _ := node["metadata"].(map[string]any)
		byID[stringValue(node["id"])] = node
		switch stringValue(metadata["workflowKind"]) {
		case "shot":
			workstations++
		case "reference_video", "reference_set":
			products++
		default:
			if stringValue(metadata["previsSceneId"]) != "" {
				bare++
			}
		}
	}
	if workstations != 1 || products != 3 || bare != 0 {
		t.Fatalf("repair left nodes inconsistent: workstations=%d products=%d bare=%d", workstations, products, bare)
	}
	videoMetadata, _ := byID["legacy-video"]["metadata"].(map[string]any)
	if stringValue(videoMetadata["workflowKind"]) != "shot" || stringValue(videoMetadata["storageKey"]) != "resource:"+legacyVideoResource.ID || stringValue(videoMetadata["previsPreviewNodeId"]) != "legacy-image" {
		t.Fatalf("legacy video was not promoted and relinked in place: %#v", videoMetadata)
	}
	completed, _ := s.repo.Task(task.ID)
	var stored previsRenderResult
	if err := json.Unmarshal([]byte(completed.ResultJSON), &stored); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{stored.VideoNodeID: "reference_video", stored.PreviewNodeID: "reference_set"} {
		metadata, _ := byID[id]["metadata"].(map[string]any)
		if stringValue(metadata["workflowKind"]) != want {
			t.Fatalf("fresh writeback product %s lost its product semantics: %#v", id, metadata)
		}
	}
	shot, ok := findPrevisShot(creationMaps(doc["previsScenes"])[0], input.ShotID)
	if !ok || stringValue(shot["previewNodeId"]) != stored.PreviewNodeID || stringValue(shot["clayVideoNodeId"]) != stored.VideoNodeID {
		t.Fatalf("shot link was not relinked to the delivered products: %#v", shot)
	}
}
func TestBackgroundPrevisConflictRetainsOutputAndRepairKeepsHistory(t *testing.T) {
	s, _, task, input := submittedPrevis(t)
	result := storedPrevis(t, s, task, input)
	canvas, _ := s.repo.CanvasProjectForUser(task.UserID, input.CanvasID)
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	creationMaps(creationMaps(doc["previsScenes"])[0]["cameras"])[0]["fov"] = 75
	raw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(raw)
	s.repo.UpsertCanvasProject(canvas)
	if err := s.commitPrevisOutput(task, input, result); err == nil {
		t.Fatal("overwrote changed target")
	}
	persisted, _ := s.repo.Task(task.ID)
	if !strings.Contains(persisted.ResultJSON, result.ResourceID) {
		t.Fatal("lost artifact checkpoint")
	}
	if err := s.failPrevis(task, "target_changed", "writeback"); err != nil {
		t.Fatal(err)
	}
	repair, err := s.RecoverPrevisWriteback(task.UserID, task.ID, "independent")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.RecoverPrevisWriteback(task.UserID, task.ID, "independent")
	if err != nil || duplicate.ID != repair.ID {
		t.Fatal("repair duplicated")
	}
	repair, err = s.repo.ClaimNextTask("repair-test-worker", time.Minute)
	if err != nil || repair == nil {
		t.Fatal("repair not claimed")
	}
	if err := s.taskWorker().processPrevisRender(repair, context.Background()); err != nil {
		t.Fatal(err)
	}
	original, _ := s.repo.Task(task.ID)
	if original.Status != model.TaskStatusFailed {
		t.Fatal("rewrote failed history")
	}
}

func TestBackgroundPrevisCancelledOrOldLeaseCannotWrite(t *testing.T) {
	for _, mode := range []string{"cancelled", "old_lease"} {
		t.Run(mode, func(t *testing.T) {
			s, _, task, input := submittedPrevis(t)
			result := storedPrevis(t, s, task, input)
			if mode == "cancelled" {
				s.repo.CancelTaskIfStatus(task.UserID, task.ID, model.TaskStatusRunning, time.Now())
			} else {
				s.repo.ReleaseTaskLease(task.ID, task.LeaseOwner)
				s.repo.ClaimNextTask("new-owner", time.Minute)
			}
			if err := s.commitPrevisOutput(task, input, result); err == nil {
				t.Fatal("stale executor wrote output")
			}
			canvas, _ := s.repo.CanvasProjectForUser(task.UserID, input.CanvasID)
			if len(creationMaps(mustCreationDocument(t, canvas.PayloadJSON)["nodes"])) != 1 {
				t.Fatal("late canvas nodes")
			}
		})
	}
}

func TestBackgroundPrevisInvalidTargetsKeepSavedArtifacts(t *testing.T) {
	for _, failure := range []string{"deleted_shot", "bad_canvas", "disabled_user", "missing_resource", "payload_limit"} {
		t.Run(failure, func(t *testing.T) {
			s, run, task, input := submittedPrevis(t)
			result := storedPrevis(t, s, task, input)
			canvas, _ := s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
			doc := mustCreationDocument(t, canvas.PayloadJSON)
			want := ""
			switch failure {
			case "deleted_shot":
				creationMaps(doc["previsScenes"])[0]["shots"] = []any{}
				want = "target_changed"
			case "bad_canvas":
				canvas.PayloadJSON = "{"
				want = "invalid_canvas"
			case "disabled_user":
				user, _ := s.repo.User(run.UserID)
				user.Status = "disabled"
				if err := s.repo.Save(user); err != nil {
					t.Fatal(err)
				}
				want = "permission_denied"
			case "missing_resource":
				if err := s.repo.DeleteResource(run.UserID, result.ResourceID); err != nil {
					t.Fatal(err)
				}
				want = "missing_resource"
			case "payload_limit":
				doc["nodes"] = append(creationMaps(doc["nodes"]), map[string]any{"id": "large", "type": "text", "metadata": map[string]any{"content": strings.Repeat("x", 4<<20)}})
				want = "payload_too_large"
			}
			if failure != "bad_canvas" {
				raw, _ := json.Marshal(doc)
				canvas.PayloadJSON = string(raw)
			}
			if err := s.repo.UpsertCanvasProject(canvas); err != nil {
				t.Fatal(err)
			}
			if err := s.commitPrevisOutput(task, input, result); err == nil || previsErrorCode(err) != want {
				t.Fatalf("want %s got %v", want, err)
			}
			latest, _ := s.repo.Task(task.ID)
			if latest.Status == model.TaskStatusSucceeded || !strings.Contains(latest.ResultJSON, result.PreviewResourceID) {
				t.Fatal("failed writeback reported success or discarded output checkpoint")
			}
		})
	}
}

func TestBackgroundPrevisFailedRepairCanBeRequestedAgain(t *testing.T) {
	s, _, task, input := submittedPrevis(t)
	storedPrevis(t, s, task, input)
	if err := s.failPrevis(task, "storage_unavailable", "writeback"); err != nil {
		t.Fatal(err)
	}
	repair, err := s.RecoverPrevisWriteback(task.UserID, task.ID, "link")
	if err != nil {
		t.Fatal(err)
	}
	repair, err = s.repo.ClaimNextTask("repair-worker", time.Minute)
	if err != nil || repair == nil {
		t.Fatal("not claimed")
	}
	if err := s.failPrevis(repair, "storage_unavailable", "writeback"); err != nil {
		t.Fatal(err)
	}
	again, err := s.RecoverPrevisWriteback(task.UserID, task.ID, "link")
	if err != nil || again.ID == repair.ID || again.Status != model.TaskStatusQueued {
		t.Fatalf("failed repair permanently blocks retry: %v %v", again, err)
	}
}

func TestBackgroundPrevisRetryResourceRespectsTotalStorageLimit(t *testing.T) {
	s, _, task, input := submittedPrevis(t)
	policy, err := s.RuntimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	key := "previs-failed-resource"
	failed := model.Resource{ID: "failed-previs", UserID: task.UserID, Status: model.ResourceStatusFailed, Provider: "local", ObjectKey: "failed-previs.mp4", UploadKey: &key, MimeType: "video/mp4", Size: 1000, Width: input.Width, Height: input.Height}
	filled := model.Resource{ID: "full-storage", UserID: task.UserID, Status: model.ResourceStatusReady, Provider: "local", ObjectKey: "full.bin", MimeType: "application/octet-stream", Size: gigabytes(policy.Resource.StoredFileGB) - 500}
	if err := s.repo.CreateResource(&failed); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.CreateResource(&filled); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(t.TempDir(), "previs-output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	file.WriteString(strings.Repeat("v", 1000))
	if _, err = s.storePrevisArtifact(context.Background(), task, "media", "previs.mp4", "video/mp4", 1000, input.Width, input.Height, 500, file, key); err == nil || previsErrorCode(err) != "quota_exceeded" {
		t.Fatal("failed object recovery bypassed total storage quota")
	}
}

func TestPrevisRenderHashNormalizesLegacyColorsWithoutMutatingCanvas(t *testing.T) {
	scene, err := cloudAgentPrevisSceneCreateTemplate(cloudAgentPrevisSceneCreateArgs{SceneID: "legacy", Title: "legacy", TemplateID: "dialogue"})
	if err != nil {
		t.Fatal(err)
	}
	scene["background"] = "rgb(17, 24, 39)"
	creationMaps(scene["objects"])[0]["color"] = "rgb(135, 149, 165)"
	shotID := stringValue(creationMaps(scene["shots"])[0]["id"])
	raw, _ := json.Marshal(scene)
	var normalized map[string]any
	json.Unmarshal(raw, &normalized)
	if err := cloudAgentPrevisValidateScene(normalized); err != nil {
		t.Fatal(err)
	}
	if previsRenderSourceHash(scene, shotID) != previsRenderSourceHash(normalized, shotID) {
		t.Fatal("unchanged legacy scene produces a writeback conflict")
	}
	after, _ := json.Marshal(scene)
	if string(after) != string(raw) {
		t.Fatal("hash mutated authoritative canvas")
	}
}

func TestBackgroundPrevisArtifactsSurviveLeaseExpiryAndOrphanCleanup(t *testing.T) {
	s, db, _ := newResourceDeletionTestService(t)
	resources := []model.Resource{{ID: "saved-video", UserID: "user-1", Provider: "unsupported-test-provider", ObjectKey: "saved.mp4", Status: model.ResourceStatusReady}, {ID: "saved-preview", UserID: "user-1", Provider: "unsupported-test-provider", ObjectKey: "saved.png", Status: model.ResourceStatusReady}}
	for _, resource := range resources {
		if err := db.Create(&resource).Error; err != nil {
			t.Fatal(err)
		}
	}
	task := model.Task{ID: "failed-previs", UserID: "user-1", Type: model.TaskTypePrevisRender, Status: model.TaskStatusFailed, InputJSON: `{}`, ResultJSON: `{"resourceId":"saved-video","previewResourceId":"saved-preview","outputReady":true}`}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	ids := []string{"saved-video", "saved-preview"}
	if err := s.repo.UpsertCloudAgentResourceLeases("user-1", "run", "previs-task:failed-previs", ids, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupDetachedUserResources("user-1", resources); err != nil {
		t.Fatal(err)
	}
	retained, err := s.repo.ResourcesForUserIDs("user-1", ids)
	if err != nil || len(retained) != 2 {
		t.Fatalf("orphan cleanup deleted recoverable output after lease expiry: %v %v", retained, err)
	}
}
