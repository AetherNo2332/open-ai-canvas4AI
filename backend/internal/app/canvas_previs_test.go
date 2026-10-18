package app

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/canvas/tasks"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"testing"
	"time"
)

func TestCanvasPrevisCompletionQueuesWritebackWithoutAgentOrLegacySave(t *testing.T) {
	s, db := newMediaRecoveryTestService(t)
	s.CanvasCommandServices()
	if err := database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.User{ID: "previs-owner", Status: model.UserStatusActive})
	p := identity.Principal{ID: "previs-owner", OwnerID: "previs-owner", Role: "owner", Scopes: identity.OwnerScopes()}
	cs := commands.NewService(s.repo, commands.ServiceOptions{})
	ts := tasks.NewService(s.repo, cs, s.CanvasTaskSubmitter(), s.CanvasTaskOptions())
	snap, err := cs.Create(context.Background(), p, commands.CreateCanvasRequest{ClientID: "client", OperationID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cs.Execute(context.Background(), p, commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "scene", BaseRevision: snap.Revision, Operations: []commands.Operation{{Type: "create_previs_scene", ID: "scene", TemplateID: "empty"}}})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	scene := commands.PrevisScene(snap.Document, "scene")
	shots := creationMaps(scene["shots"])
	h, err := ts.SubmitPrevis(context.Background(), p, tasks.PrevisSubmitRequest{Command: commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "render", BaseRevision: snap.Revision}, SceneID: "scene", ShotID: stringValue(shots[0]["id"])})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.repo.ClaimNextTask("fixture-worker", time.Minute)
	if err != nil || task == nil || task.ID != h.TaskID || task.Type != model.TaskTypePrevisRender {
		t.Fatalf("previs not claimable: %v %+v", err, task)
	}
	var input previsRenderInput
	if err = json.Unmarshal([]byte(task.InputJSON), &input); err != nil {
		t.Fatal(err)
	}
	for _, r := range []model.Resource{{ID: "canvas-previs-video", UserID: p.OwnerID, Status: model.ResourceStatusReady, MimeType: "video/mp4", Size: 200, Width: input.Width, Height: input.Height}, {ID: "canvas-previs-preview", UserID: p.OwnerID, Status: model.ResourceStatusReady, MimeType: "image/png", Size: 200, Width: input.Width, Height: input.Height}} {
		if err = db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	before, _ := cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	result := previsRenderResult{ResourceID: "canvas-previs-video", PreviewResourceID: "canvas-previs-preview", SceneID: input.SceneID, ShotID: input.ShotID, SourceHash: input.SourceHash, Width: input.Width, Height: input.Height, OutputReady: true}
	if err = s.commitPrevisOutput(task, input, result); err != nil {
		t.Fatal(err)
	}
	after, _ := cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	if before.Revision != after.Revision {
		t.Fatal("terminal worker wrote legacy canvas")
	}
	if _, err = s.repo.CanvasMaterialization(task.ID); err != nil {
		t.Fatal("terminal result lost materialization job")
	}
	if err = ts.Materialize(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	after, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	if len(creationMaps(after.Document["nodes"])) != 2 || after.Revision != before.Revision+1 {
		t.Fatal("command result missing")
	}
	var count int64
	db.Model(&model.CloudAgentExecution{}).Count(&count)
	if count != 0 || task.AgentRunID != "" {
		t.Fatal("Agent created")
	}
}
