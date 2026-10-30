package tasks

import (
	"context"
	"strings"
	"testing"
	"yingce/backend/internal/model"
)

func TestMaterializationFailureBacksOffAndRetryNeverRegenerates(t *testing.T) {
	s, db, repo, _, p, req, stub := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"ready"}`})
	repo.EnqueueCanvasMaterialization(h.TaskID)
	db.Exec(`CREATE TRIGGER fail_apply BEFORE INSERT ON canvas_command_receipts WHEN NEW.operation_id LIKE 'task-result:%' BEGIN SELECT RAISE(ABORT,'PRIVATE-DATABASE-ERROR'); END;`)
	if err = s.DrainMaterializations(context.Background()); err == nil {
		t.Fatal("failure missing")
	}
	view, err := s.ReadMaterialization(context.Background(), p, h.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Attempts != 1 || view.NextAttemptAt == nil || view.Message == "" || strings.Contains(view.Message, "PRIVATE") {
		t.Fatalf("no safe persistent diagnostic: %+v", view)
	}
	s.DrainMaterializations(context.Background())
	view, _ = s.ReadMaterialization(context.Background(), p, h.TaskID)
	if view.Attempts != 1 {
		t.Fatal("retry ignored backoff")
	}
	db.Exec(`DROP TRIGGER fail_apply`)
	if err = s.RetryMaterialization(context.Background(), p, h.TaskID); err != nil {
		t.Fatal(err)
	}
	view, _ = s.ReadMaterialization(context.Background(), p, h.TaskID)
	if view.State != "done" || stub.calls != 1 || stub.wakes != 1 {
		t.Fatalf("retry regenerated: %+v calls %d", view, stub.calls)
	}
	denied := p
	denied.Role = "viewer"
	if err = s.RetryMaterialization(context.Background(), denied, h.TaskID); err == nil {
		t.Fatal("viewer could write back")
	}
}
