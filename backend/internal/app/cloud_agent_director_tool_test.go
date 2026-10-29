package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func directorTestCall(id string, args map[string]any) cloudAgentCall {
	raw, _ := json.Marshal(args)
	call := cloudAgentCall{ID: id}
	call.Function.Name, call.Function.Arguments = "director", string(raw)
	return call
}

// directorFixture seeds a leased-free Pi run whose next call is the given
// director call, under the requested permission mode.
func directorFixture(t *testing.T, permission string, payload string, call cloudAgentCall) (*Service, *model.CloudAgentExecution, *cloudAgentRuntime) {
	t.Helper()
	s, db, run := piAgentTestFixture(t)
	if payload != "" {
		if err := db.Model(&model.CanvasProject{}).Where("id = ?", run.CanvasID).Update("payload_json", payload).Error; err != nil {
			t.Fatal(err)
		}
	}
	state, err := cloudAgentDecode(run)
	if err != nil {
		t.Fatal(err)
	}
	state.Request.PermissionMode = permission
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

func directorSceneDocument(t *testing.T) string {
	t.Helper()
	scene, err := cloudAgentPrevisSceneCreateTemplate(cloudAgentPrevisSceneCreateArgs{SceneID: "scene", Title: "客厅", TemplateID: "dialogue"})
	if err != nil {
		t.Fatal(err)
	}
	node := map[string]any{"id": "shot-node", "type": "video", "title": "镜头 1", "position": map[string]any{"x": 0, "y": 0}, "width": 520, "height": 340,
		"metadata": map[string]any{"workflowKind": "shot", "previsSceneId": "scene", "previsShotId": "scene-shot-1", "status": "idle"}}
	raw, _ := json.Marshal(map[string]any{"nodes": []any{node}, "connections": []any{}, "previsScenes": []any{scene}})
	return string(raw)
}

func directorEvents(state *cloudAgentRuntime, kind string) []CloudAgentEvent {
	out := []CloudAgentEvent{}
	for _, event := range state.Events {
		if event.Type == kind {
			out = append(out, event)
		}
	}
	return out
}

func directorToolMessage(t *testing.T, state *cloudAgentRuntime, callID string) map[string]any {
	t.Helper()
	for _, message := range state.Canonical.Messages {
		if stringField(message, "role") == "tool" && stringField(message, "tool_call_id") == callID {
			out := map[string]any{}
			if err := json.Unmarshal([]byte(stringField(message, "content")), &out); err != nil {
				t.Fatal(err)
			}
			return out
		}
	}
	t.Fatalf("missing tool result for %s", callID)
	return nil
}

func TestDirectorToolSchemaSingleEntry(t *testing.T) {
	catalogs := map[string][]string{
		"supported": CloudAgentSupportedToolNames(),
		"artifact":  cloudAgentToolNames(CloudAgentToolSchemaArtifact().Tools),
	}
	for label, names := range catalogs {
		found := 0
		for _, name := range names {
			if name == "director" {
				found++
				continue
			}
			if strings.HasPrefix(name, "previs_") || strings.HasPrefix(name, "director_") {
				t.Fatalf("%s catalog still exposes %s", label, name)
			}
		}
		if found != 1 {
			t.Fatalf("%s catalog must expose exactly one director tool, got %d: %v", label, found, names)
		}
	}
	for _, name := range []string{"previs_scene_read", "previs_scene_create", "previs_apply_patch", "previs_preview", "director_block_scene", "director_preview_shot", "director_check_continuity", "director_handoff_shot"} {
		if cloudAgentToolAllowed(tierAutoRequest(), name) || cloudAgentWrite(name) {
			t.Fatalf("retired tool %s remains callable", name)
		}
	}
	if !cloudAgentWrite("director") {
		t.Fatal("director must be a write tool by name")
	}
	if cloudAgentToolCategory("director") == "" {
		t.Fatal("director needs a tool category")
	}
}

func TestDirectorReadAllowedReadOnly(t *testing.T) {
	req := tierAutoRequest()
	req.PermissionMode = "read_only"
	if !cloudAgentToolAllowed(req, "director") {
		t.Fatal("read_only must keep director for action=read")
	}
	for _, tool := range cloudAgentTools(req) {
		function := tool["function"].(map[string]any)
		if function["name"] != "director" {
			continue
		}
		action := function["parameters"].(map[string]any)["properties"].(map[string]any)["action"].(map[string]any)
		if enum, _ := action["enum"].([]string); len(enum) != 1 || enum[0] != "read" {
			t.Fatalf("read_only director must only advertise read: %v", action["enum"])
		}
	}
	read := directorTestCall("director-read", map[string]any{"action": "read", "sceneId": "scene"})
	if cloudAgentWriteCall(read) {
		t.Fatal("director read is not a write")
	}
	if !cloudAgentWriteCall(directorTestCall("x", map[string]any{"action": "create_scene"})) || !cloudAgentWriteCall(directorTestCall("x", map[string]any{"action": "preview"})) {
		t.Fatal("director write actions must be writes")
	}
	bad := cloudAgentCall{ID: "bad"}
	bad.Function.Name, bad.Function.Arguments = "director", "{"
	if !cloudAgentWriteCall(bad) {
		t.Fatal("unparseable director call must fail closed as a write")
	}

	s, run, state := directorFixture(t, "read_only", directorSceneDocument(t), read)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if state.Approval != nil || run.Status == "waiting_approval" || state.CallIndex != 1 {
		t.Fatalf("read must execute without approval: status=%s index=%d", run.Status, state.CallIndex)
	}
	result := directorToolMessage(t, state, "director-read")
	if result["snapshotHash"] == "" || result["nodeId"] != "shot-node" {
		t.Fatalf("read result missing scene hash or node: %v", result)
	}
	if len(directorEvents(state, "director_open")) != 0 {
		t.Fatal("read must not open the director desk")
	}

	write := directorTestCall("director-write", map[string]any{"action": "create_scene", "template": "empty"})
	s, run, state = directorFixture(t, "read_only", "", write)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	_, state = reloadPiRun(t, s, run.ID)
	if state.CallIndex != 1 || len(directorEvents(state, "tool_failed")) != 1 {
		t.Fatal("read_only director write must be rejected as a tool failure")
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", run.CanvasID)
	if strings.Contains(canvas.PayloadJSON, "previsScenes\":[{") {
		t.Fatal("read_only director wrote the canvas")
	}
}

func TestDirectorWriteNeedsApproval(t *testing.T) {
	call := directorTestCall("director-create", map[string]any{"action": "create_scene", "template": "dialogue", "sceneId": "new-scene"})
	s, run, state := directorFixture(t, "request_approval", "", call)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if run.Status != "waiting_approval" || state.Approval == nil {
		t.Fatalf("director write must wait for approval: %s", run.Status)
	}
	if len(directorEvents(state, "director_open")) != 0 {
		t.Fatal("director_open emitted before approval")
	}
	canvas, _ := s.repo.CanvasProjectForUser("user", run.CanvasID)
	if strings.Contains(canvas.PayloadJSON, "new-scene") {
		t.Fatal("canvas written before approval")
	}
	if err := s.DecideCloudAgentApproval("user", run.ID, state.Approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	_, state = reloadPiRun(t, s, run.ID)
	opens := directorEvents(state, "director_open")
	if len(opens) != 1 || opens[0].Payload["sceneId"] != "new-scene" || opens[0].Payload["action"] != "create_scene" || opens[0].Payload["nodeId"] == "" {
		t.Fatalf("approved write must emit one director_open: %+v", opens)
	}
	canvas, _ = s.repo.CanvasProjectForUser("user", run.CanvasID)
	doc := mustCreationDocument(t, canvas.PayloadJSON)
	if _, ok := findPrevisScene(creationMaps(doc["previsScenes"]), "new-scene"); !ok {
		t.Fatal("approved scene not persisted")
	}
}

func TestDirectorAutoEmitsOpenEvent(t *testing.T) {
	call := directorTestCall("director-auto", map[string]any{"action": "create_scene", "template": "empty", "sceneId": "auto-scene"})
	s, run, state := directorFixture(t, "auto", "", call)
	before, _ := s.repo.CanvasProjectForUser("user", run.CanvasID)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if run.Status == "waiting_approval" || state.Approval != nil {
		t.Fatal("auto mode must not request approval")
	}
	opens := directorEvents(state, "director_open")
	if len(opens) != 1 {
		t.Fatalf("auto write must emit director_open: %+v", state.Events)
	}
	payload := opens[0].Payload
	if payload["canvasId"] != run.CanvasID || payload["sceneId"] != "auto-scene" || payload["action"] != "create_scene" || payload["nodeId"] == "" {
		t.Fatalf("director_open payload = %v", payload)
	}
	completed := directorEvents(state, "tool_completed")
	if len(completed) != 1 || completed[0].Seq < opens[0].Seq {
		t.Fatal("director_open must precede the tool result")
	}
	after, _ := s.repo.CanvasProjectForUser("user", run.CanvasID)
	if after.Revision <= before.Revision {
		t.Fatal("director write did not go through the shared canvas revision")
	}
	doc := mustCreationDocument(t, after.PayloadJSON)
	nodeID := stringValue(payload["nodeId"])
	found := false
	for _, node := range creationMaps(doc["nodes"]) {
		metadata, _ := node["metadata"].(map[string]any)
		if stringValue(node["id"]) == nodeID && stringValue(metadata["previsSceneId"]) == "auto-scene" && stringValue(metadata["workflowKind"]) == "shot" {
			found = true
		}
	}
	if !found {
		t.Fatal("create_scene must bind a workstation node the browser can open")
	}
}

func TestDirectorPreviewTimesOutWithoutViewport(t *testing.T) {
	call := directorTestCall("director-preview", map[string]any{"action": "preview", "sceneId": "scene"})
	s, run, state := directorFixture(t, "auto", directorSceneDocument(t), call)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if state.DirectorPreview == nil || state.CallIndex != 0 {
		t.Fatal("preview must wait for the browser viewport")
	}
	opens := directorEvents(state, "director_open")
	if len(opens) != 1 || opens[0].Payload["requestId"] != state.DirectorPreview.RequestID || opens[0].Payload["action"] != "preview" || opens[0].Payload["shotId"] != "scene-shot-1" {
		t.Fatalf("preview director_open = %+v", opens)
	}
	if time.Until(state.DirectorPreview.Deadline) < 100*time.Second {
		t.Fatalf("default timeout must be 120s: %v", state.DirectorPreview.Deadline)
	}
	revision := run.Revision
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if state.CallIndex != 0 || run.Revision != revision {
		t.Fatal("polling an unexpired preview must not record or rewrite anything")
	}
	state.DirectorPreview.Deadline = time.Now().Add(-time.Second)
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	if state.CallIndex != 1 || state.DirectorPreview != nil {
		t.Fatal("expired preview must settle the call")
	}
	result := directorToolMessage(t, state, "director-preview")
	if result["reason"] != "director_viewport_unavailable" || result["retryable"] != true {
		t.Fatalf("timeout receipt = %v", result)
	}
	if err := s.SubmitCloudAgentDirectorPreview("user", run.ID, opens[0].Payload["requestId"].(string), "video", ""); !directorNotFound(err) {
		t.Fatalf("expired request accepted: %v", err)
	}
}

func directorNotFound(err error) bool {
	var appErr *kernel.AppError
	return errors.As(err, &appErr) && appErr.Status == 404 && appErr.Reason == "director_preview_not_found"
}

func TestDirectorPreviewResultResumesCall(t *testing.T) {
	call := directorTestCall("director-preview", map[string]any{"action": "preview", "sceneId": "scene", "shotId": "scene-shot-1"})
	s, run, state := directorFixture(t, "auto", directorSceneDocument(t), call)
	if err := s.executeCloudAgentToolCall(run, state); err != nil {
		t.Fatal(err)
	}
	run, state = reloadPiRun(t, s, run.ID)
	requestID := state.DirectorPreview.RequestID
	if err := s.repo.Create(&model.Resource{ID: "preview-video", UserID: "user", Status: model.ResourceStatusReady, MimeType: "video/mp4", Size: 1024}); err != nil {
		t.Fatal(err)
	}
	if err := s.repo.Create(&model.Resource{ID: "foreign-video", UserID: "other", Status: model.ResourceStatusReady, MimeType: "video/mp4", Size: 1024}); err != nil {
		t.Fatal(err)
	}
	if err := s.SubmitCloudAgentDirectorPreview("user", run.ID, "wrong", "preview-video", ""); !directorNotFound(err) {
		t.Fatalf("unknown request accepted: %v", err)
	}
	if err := s.SubmitCloudAgentDirectorPreview("other", run.ID, requestID, "preview-video", ""); !directorNotFound(err) {
		t.Fatalf("foreign user accepted: %v", err)
	}
	if err := s.SubmitCloudAgentDirectorPreview("user", run.ID, requestID, "foreign-video", ""); err == nil {
		t.Fatal("foreign resource accepted")
	}
	if err := s.SubmitCloudAgentDirectorPreview("user", run.ID, requestID, "preview-video", "both"); err == nil {
		t.Fatal("both result and error accepted")
	}
	if err := s.SubmitCloudAgentDirectorPreview("user", run.ID, requestID, "preview-video", ""); err != nil {
		t.Fatal(err)
	}
	_, state = reloadPiRun(t, s, run.ID)
	if state.CallIndex != 1 || state.DirectorPreview != nil {
		t.Fatal("viewport result did not resume the call")
	}
	result := directorToolMessage(t, state, "director-preview")
	if result["videoResourceId"] != "preview-video" || result["requestId"] != requestID || result["status"] != "completed" {
		t.Fatalf("preview receipt = %v", result)
	}
	if err := s.SubmitCloudAgentDirectorPreview("user", run.ID, requestID, "preview-video", ""); !directorNotFound(err) {
		t.Fatal("settled request accepted twice")
	}
}

// The Pi worker drives tools through PiToolAdvance: a waiting preview must stay
// pending (no receipt, no duplicate open event) until the browser result lands.
func TestDirectorPreviewThroughPiToolAdvance(t *testing.T) {
	s, db, run := piAgentTestLeasedFixture(t)
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", run.CanvasID).Update("payload_json", directorSceneDocument(t)).Error; err != nil {
		t.Fatal(err)
	}
	_, state := reloadPiRun(t, s, run.ID)
	call := directorTestCall("pi-preview", map[string]any{"action": "preview", "nodeId": "shot-node"})
	state.Calls = []cloudAgentCall{call}
	state.PiToolBatchTaskID, state.LastStepTaskID = "batch-task", "batch-task"
	state.Canonical.Messages = append(state.Canonical.Messages, map[string]any{"role": "assistant", "content": "", "tool_calls": state.Calls})
	if err := s.repo.MutateCloudAgent(run.UserID, run.ID, run.Revision, func(current *model.CloudAgentExecution, _ *repository.Repository) error {
		return cloudAgentSave(current, state)
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		receipt, err := s.PiToolAdvance("user", run.ID, "worker-a", "batch-task", call.ID)
		if err != nil || receipt == nil || !receipt.Pending || receipt.Result != nil {
			t.Fatalf("waiting preview must stay pending: %+v %v", receipt, err)
		}
	}
	latest, state := reloadPiRun(t, s, run.ID)
	if latest.WaitKind != cloudAgentDirectorPreviewWaitKind || len(directorEvents(state, "director_open")) != 1 {
		t.Fatalf("wait kind=%s opens=%d", latest.WaitKind, len(directorEvents(state, "director_open")))
	}
	if err := s.SubmitCloudAgentDirectorPreview("user", run.ID, state.DirectorPreview.RequestID, "", "用户关闭了导演台"); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.PiToolAdvance("user", run.ID, "worker-a", "batch-task", call.ID)
	if err != nil || receipt == nil || receipt.Pending || !receipt.IsError {
		t.Fatalf("browser error must settle as a failed receipt: %+v %v", receipt, err)
	}
}
