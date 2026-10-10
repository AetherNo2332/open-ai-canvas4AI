package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
)

func backgroundPrevisFixture(t *testing.T, permission string) (*Service, *model.CloudAgentExecution, *cloudAgentRuntime) {
	t.Helper()
	s, _, run := piAgentTestFixture(t)
	s.activeCancels = make(map[string]context.CancelFunc)
	if _, err := s.repo.User(run.UserID); err != nil {
		if err := s.repo.Create(&model.User{ID: run.UserID, Username: "previs-user", Status: model.UserStatusActive, Role: model.UserRoleUser}); err != nil {
			t.Fatal(err)
		}
	}
	scene, err := cloudAgentPrevisSceneCreateTemplate(cloudAgentPrevisSceneCreateArgs{SceneID: "scene", Title: "后台预演", TemplateID: "dialogue"})
	if err != nil {
		t.Fatal(err)
	}
	// 场景创建即绑定工作站（预演台修复计划 G1）：回写前置检查要求工作站真实存在，
	// fixture 走生产代码放置工作站，而不是手写一个「裸」场景。
	doc := map[string]any{"nodes": []any{}, "connections": []any{}, "previsScenes": []any{scene}}
	if _, _, _, err := cloudAgentPrevisEnsureWorkstationNode(doc, scene, "", ""); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	canvas, err := s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	canvas.PayloadJSON = string(raw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Request.PermissionMode = permission
	shot := creationMaps(scene["shots"])[0]
	callID := "render-call"
	if os.Getenv("PREVIS_REAL_BACKEND") != "" {
		callID = fmt.Sprintf("real-call-%d", time.Now().UnixNano())
	}
	call := previsMutationCall(t, callID, "previs_preview", map[string]any{"sceneId": "scene", "shotId": shot["id"], "duration": 0.5, "fps": 8})
	state.Calls = []cloudAgentCall{call}
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": state.Calls})
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, &state)
	}); err != nil {
		t.Fatal(err)
	}
	run, latest := reloadPiRun(t, s, run.ID)
	return s, run, latest
}

func TestBackgroundPrevisSubmissionWaitsForDurableTask(t *testing.T) {
	s, run, state := backgroundPrevisFixture(t, "auto")
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if state.MediaTaskID == "" {
		t.Fatal("previs_preview returned without creating a durable render task")
	}
	task, err := s.repo.TaskForUser(run.UserID, state.MediaTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Type != "previs_render" || task.Status != model.TaskStatusQueued || task.AgentRunID != run.ID || task.BillingOrderID != "" {
		t.Fatalf("unexpected task: %+v", task)
	}
	if state.CallIndex != 0 {
		t.Fatal("tool advanced before video and canvas output were saved")
	}
	for _, event := range state.Events {
		if event.Type == "tool_completed" && event.Payload["callId"] == "render-call" {
			t.Fatal("queued render falsely reported tool_completed")
		}
	}
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	_, recovered := reloadPiRun(t, s, run.ID)
	if recovered.MediaTaskID != task.ID || len(recovered.TaskIDs) != len(state.TaskIDs) {
		t.Fatal("replay duplicated render task")
	}
}

func TestBackgroundPrevisUsesFrozenSceneAfterSubmission(t *testing.T) {
	s, run, state := backgroundPrevisFixture(t, "auto")
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	_, state = reloadPiRun(t, s, run.ID)
	if state.MediaTaskID == "" {
		t.Fatal("missing durable previs task")
	}
	task, _ := s.repo.TaskForUser(run.UserID, state.MediaTaskID)
	var input map[string]any
	if err := json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	if input["scene"] == nil || input["shotId"] == nil || input["callId"] != "render-call" || input["duration"] != 0.5 {
		t.Fatalf("render input is not a frozen scene: %s", task.InputJSON)
	}
}

func TestBackgroundPrevisRejectsInvalidInheritedTiming(t *testing.T) {
	s, run, state := backgroundPrevisFixture(t, "auto")
	canvas, _ := s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	creationMaps(creationMaps(doc["previsScenes"])[0]["shots"])[0]["duration"] = -5
	raw, _ := json.Marshal(doc)
	canvas.PayloadJSON = string(raw)
	if err := s.repo.UpsertCanvasProject(canvas); err != nil {
		t.Fatal(err)
	}
	state.Calls[0].Function.Arguments = `{"sceneId":"scene","shotId":"scene-shot-1"}`
	if _, err := cloudAgentPrevisPreview(s.repo, run.UserID, run.CanvasID, state.Calls[0]); err == nil {
		t.Fatal("invalid inherited duration admitted")
	}
}

func TestBackgroundPrevisApprovalWaitRejectAndStaleSource(t *testing.T) {
	for _, decision := range []string{"reject", "stale"} {
		t.Run(decision, func(t *testing.T) {
			s, run, state := backgroundPrevisFixture(t, "request_approval")
			if err := s.executeCloudAgentToolCall(run, state); err != nil {
				t.Fatal(err)
			}
			run, state = reloadPiRun(t, s, run.ID)
			if state.Approval == nil || state.MediaTaskID != "" {
				t.Fatal("approval wait submitted work")
			}
			choice := "reject"
			if decision == "stale" {
				choice = "approve"
			}
			if err := s.DecideCloudAgentApproval(run.UserID, run.ID, state.Approval.ID, choice, ""); err != nil {
				t.Fatal(err)
			}
			if decision == "stale" {
				canvas, _ := s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
				doc := mustCreationDocument(t, canvas.PayloadJSON)
				creationMaps(creationMaps(doc["previsScenes"])[0]["cameras"])[0]["fov"] = 65
				raw, _ := json.Marshal(doc)
				canvas.PayloadJSON = string(raw)
				s.repo.UpsertCanvasProject(canvas)
			}
			run, state = reloadPiRun(t, s, run.ID)
			if err := s.executeCloudAgentToolCall(run, state); err != nil {
				t.Fatal(err)
			}
			_, state = reloadPiRun(t, s, run.ID)
			if state.MediaTaskID != "" {
				t.Fatal("rejected or stale approval submitted")
			}
			pending, err := s.repo.PendingPrevisForRun(run.UserID, run.ID)
			if err != nil || len(pending) != 0 {
				t.Fatal("unexpected render tasks")
			}
		})
	}
}

func TestBackgroundPrevisRejectsForeignAndUncontrolledAssets(t *testing.T) {
	for _, storageKey := range []string{"resource:foreign-model", ""} {
		t.Run(storageKey, func(t *testing.T) {
			s, run, state := backgroundPrevisFixture(t, "auto")
			canvas, _ := s.repo.CanvasProjectForUser(run.UserID, run.CanvasID)
			doc := mustCreationDocument(t, canvas.PayloadJSON)
			object := creationMaps(creationMaps(doc["previsScenes"])[0]["objects"])[0]
			object["kind"], object["url"], object["storageKey"] = "model", "http://169.254.169.254/private", storageKey
			raw, _ := json.Marshal(doc)
			canvas.PayloadJSON = string(raw)
			s.repo.UpsertCanvasProject(canvas)
			if _, err := preparePrevisRender(s.repo, run.UserID, run.CanvasID, state.Calls[0]); err == nil {
				t.Fatal("unowned URL admitted")
			}
		})
	}
}
