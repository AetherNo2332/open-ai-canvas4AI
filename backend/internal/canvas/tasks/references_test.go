package tasks

import (
	"context"
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestTaskReferenceOwnershipCheckedBeforePreparation(t *testing.T) {
	s, db, _, _, p, req, stub := taskFixture(t)
	db.Create(&model.Resource{ID: "foreign", UserID: "other", Status: model.ResourceStatusReady, MimeType: "image/png"})
	req.Task.Input = map[string]any{"referenceImages": []any{map[string]any{"storageKey": "resource:foreign", "dataUrl": "/api/resources/foreign/file"}}}
	if _, err := s.Submit(context.Background(), p, req); err == nil || stub.calls != 0 {
		t.Fatal("foreign reference queued")
	}
}
func TestTaskReferenceNodeVersionIncluded(t *testing.T) {
	s, _, _, _, p, req, stub := taskFixture(t)
	req.Task.Input = map[string]any{"metadata": map[string]any{"referenceNodeIds": []any{"missing"}}}
	if _, err := s.Submit(context.Background(), p, req); err == nil || stub.calls != 0 {
		t.Fatal("dangling node reference queued")
	}
}
