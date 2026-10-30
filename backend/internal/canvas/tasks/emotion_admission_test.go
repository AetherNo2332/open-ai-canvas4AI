package tasks

import (
	"context"
	"encoding/json"
	"testing"
	"yingce/backend/internal/model"
)

func TestEmotionAdmissionChecksAndGuardsFrozenSource(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "owned"}[owned], func(t *testing.T) {
			s, db, _, cs, p, req, stub := taskFixture(t)
			snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
			node := findNode(snap.Document, "target")
			node["type"] = "image"
			region := map[string]any{"x": 1.0, "y": 1.0, "width": 10.0, "height": 10.0}
			metadata(node)["emotionEdit"] = map[string]any{"sourceNodeId": "source", "editRegion": region, "faceBox": region}
			source := map[string]any{"id": "source", "type": "image", "metadata": map[string]any{"storageKey": "resource:emotion-input"}}
			snap.Document["nodes"] = append(snap.Document["nodes"].([]any), source)
			raw, _ := json.Marshal(snap.Document)
			db.Model(&model.CanvasProject{}).Where("id = ?", snap.CanvasID).Update("payload_json", string(raw))
			owner := "other"
			if owned {
				owner = p.OwnerID
			}
			db.Create(&model.Resource{ID: "emotion-input", UserID: owner, Kind: "image", Status: model.ResourceStatusReady, MimeType: "image/png", Size: 100, Width: 32, Height: 32})
			req.Task.Mode = "image"
			h, err := s.Submit(context.Background(), p, req)
			if !owned {
				if err == nil || stub.calls != 0 {
					t.Fatal("foreign emotion input reached preparation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var count int64
			db.Model(&model.CanvasTaskResource{}).Where("task_id = ? AND resource_id = ?", h.TaskID, "emotion-input").Count(&count)
			if count != 1 {
				t.Fatal("frozen emotion input unprotected")
			}
		})
	}
}
