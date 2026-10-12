package tasks

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestPrevisTaskWithoutAgentUsesCommandWriteback(t *testing.T) {
	s, db, repo, cs, p, req, stub := taskFixture(t)
	command := req.Command
	command.OperationID = "scene"
	command.Operations = []commands.Operation{{Type: "create_previs_scene", ID: "scene", TemplateID: "empty"}}
	if _, err := cs.Execute(context.Background(), p, command); err != nil {
		t.Fatal(err)
	}
	snap, _ := cs.ReadSnapshot(context.Background(), p, command.CanvasID)
	scene := commands.PrevisScene(snap.Document, "scene")
	shot := objectList(scene["shots"])[0]
	command.OperationID = "render"
	command.Operations = nil
	command.BaseRevision = snap.Revision
	request := PrevisSubmitRequest{Command: command, SceneID: "scene", ShotID: str(shot["id"])}
	h, err := s.SubmitPrevis(context.Background(), p, request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.SubmitPrevis(context.Background(), p, request)
	if err != nil || h.TaskID != again.TaskID || stub.calls != 1 || stub.wakes != 1 {
		t.Fatal("duplicate previs task")
	}
	binding, _ := repo.CanvasTaskBinding(h.TaskID)
	var input commands.PrevisRenderInput
	if err = json.Unmarshal([]byte(binding.ResultContextJSON), &input); err != nil {
		t.Fatal(err)
	}
	for _, r := range []model.Resource{{ID: "pv-video", UserID: p.OwnerID, Status: model.ResourceStatusReady, MimeType: "video/mp4", Size: 200, Width: input.Width, Height: input.Height}, {ID: "pv-preview", UserID: p.OwnerID, Status: model.ResourceStatusReady, MimeType: "image/png", Size: 200, Width: input.Width, Height: input.Height}} {
		db.Create(&r)
	}
	result, _ := json.Marshal(commands.PrevisOutput{ResourceID: "pv-video", PreviewResourceID: "pv-preview", Width: input.Width, Height: input.Height})
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": string(result)})
	repo.EnqueueCanvasMaterialization(h.TaskID)
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, command.CanvasID)
	shot = objectList(commands.PrevisScene(snap.Document, "scene")["shots"])[0]
	if str(shot["clayVideoNodeId"]) == "" || str(shot["previewNodeId"]) == "" {
		t.Fatal("previs result not applied")
	}
	outputs, _ := repo.CanvasTaskOutputs(h.TaskID)
	if len(outputs) != 2 {
		t.Fatal("previs artifacts not retained")
	}
	task, _ := repo.Task(h.TaskID)
	if task.AgentRunID != "" {
		t.Fatal("fake Agent run")
	}
}
