package tasks

import (
	"context"
	"infinite-canvas/backend/internal/model"
	"testing"
)

func TestOwnerClosesUncertaintyWithoutResubmitting(t *testing.T) {
	s, db, repo, cs, p, req, stub := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Update("status", model.TaskStatusSubmissionUncertain)
	repo.EnqueueCanvasMaterialization(h.TaskID)
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	header := req.Command
	header.OperationID = "owner-close"
	header.BaseRevision = snap.Revision
	viewer := p
	viewer.Role = "viewer"
	if _, err = s.ResolveUncertainty(context.Background(), viewer, h.TaskID, header); err == nil {
		t.Fatal("viewer closed uncertainty")
	}
	receipt, err := s.ResolveUncertainty(context.Background(), p, h.TaskID, header)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := s.ResolveUncertainty(context.Background(), p, h.TaskID, header)
	if err != nil || receipt.EventSeq != repeat.EventSeq {
		t.Fatal("resolution retry differed")
	}
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if metadata(findNode(snap.Document, "target"))["taskStatus"] != "failed" {
		t.Fatal("target remains blocked")
	}
	count, _ := repo.ActiveTaskCountForUser(p.OwnerID)
	if count != 0 || stub.calls != 1 || stub.wakes != 1 {
		t.Fatal("owner closure contacted provider")
	}
	task, _ := repo.Task(h.TaskID)
	if task.Status != model.TaskStatusFailed || task.Error != "submission_uncertainty_acknowledged" {
		t.Fatal("closure destroyed provenance")
	}
}
