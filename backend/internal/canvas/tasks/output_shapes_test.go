package tasks

import (
	"context"
	"encoding/json"
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestOutputShapesKeepDirectStoryboardAndAllAudio(t *testing.T) {
	rows := `{"title":"direct","rows":[{"plotDescription":"入场"}]}`
	values, err := resultValues(rows, "text")
	if err != nil || len(values) != 1 || str(values[0]["text"]) != rows {
		t.Fatalf("direct structured text lost: %+v %v", values, err)
	}
	values, err = resultValues(`{"audios":[{"storageKey":"resource:a"},{"storageKey":"resource:b"}],"audio":{"storageKey":"resource:a"}}`, "audio")
	if err != nil || len(values) != 2 {
		t.Fatalf("audio outputs dropped: %+v %v", values, err)
	}
}
func TestVideoPreviewAndProducedModelSurviveMaterialization(t *testing.T) {
	s, db, repo, cs, p, req, _ := taskFixture(t)
	snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	n := findNode(snap.Document, "target")
	n["type"] = "video"
	m := metadata(n)
	m["workflowProvider"] = "old"
	m["runningHubWorkflowId"] = "old"
	m["workflowParameters"] = map[string]any{"old": true}
	raw, _ := json.Marshal(snap.Document)
	db.Model(&model.CanvasProject{}).Where("id = ?", snap.CanvasID).Update("payload_json", string(raw))
	req.Task.Mode = "video"
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	db.Create(&model.Resource{ID: "video-shape", UserID: p.OwnerID, Kind: "video", Status: model.ResourceStatusReady, MimeType: "video/mp4", Size: 1000, Width: 1920, Height: 1080})
	db.Create(&model.Resource{ID: "preview-shape", UserID: p.OwnerID, Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Size: 100, Width: 640, Height: 360})
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"model": "fixture-video", "status": model.TaskStatusSucceeded, "result_json": `{"video":{"storageKey":"resource:video-shape","preview":{"storageKey":"resource:preview-shape"}}}`})
	repo.EnqueueCanvasMaterialization(h.TaskID)
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	n = findNode(snap.Document, "target")
	m = metadata(n)
	preview, _ := m["videoPreview"].(map[string]any)
	if preview["storageKey"] != "resource:preview-shape" || preview["content"] != "/api/resources/preview-shape/file" || numeric(preview["bytes"]) != 100 {
		t.Fatalf("preview lost: %+v", m)
	}
	if m["producedModel"] != "fixture-video" || m["workflowProvider"] != nil || m["runningHubWorkflowId"] != nil || m["workflowParameters"] != nil {
		t.Fatalf("result provenance differs: %+v", m)
	}
	if numeric(n["width"]) != 720 || numeric(n["height"]) != 405 {
		t.Fatalf("video fit differs: %+v", n)
	}
}
