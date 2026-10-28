package tasks

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"yingce/backend/internal/canvas/commands"
	"yingce/backend/internal/model"
)

func TestTerminalSuccessAndJobCommitTogether(t *testing.T) {
	s, db, repo, _, p, req, _ := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	task, _ := repo.Task(h.TaskID)
	task.Status = model.TaskStatusSucceeded
	task.ResultJSON = `{"text":"saved"}`
	if err = repo.SaveTaskCompletionWithRegistration(task, model.TaskStatusQueued, nil, nil); err != nil {
		t.Fatal(err)
	}
	jobs, err := repo.PendingCanvasMaterializations(10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("terminal result without durable job: %v %v", jobs, err)
	}
	// Failure writing job must prevent terminal success in the same transaction.
	db.Exec(`DELETE FROM canvas_materialization_jobs`)
	db.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusQueued)
	db.Exec(`CREATE TRIGGER job_failure BEFORE INSERT ON canvas_materialization_jobs BEGIN SELECT RAISE(ABORT,'failed'); END;`)
	if err = repo.SaveTaskCompletionWithRegistration(task, model.TaskStatusQueued, nil, nil); err == nil {
		t.Fatal("job failure acknowledged")
	}
	stored, _ := repo.Task(task.ID)
	if stored.Status != model.TaskStatusQueued {
		t.Fatal("task committed without job")
	}
}
func TestUnknownSubmissionCannotBeClaimedOrResubmitted(t *testing.T) {
	s, db, repo, _, p, req, stub := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Update("status", model.TaskStatusRunning)
	task, _ := repo.Task(h.TaskID)
	task.Status = model.TaskStatusFailed
	task.ExecutionDiagnosticJSON = `{"submissionOutcome":"unknown","retryClass":"reconcile_submission"}`
	if ok, err := repo.UpdateTaskTerminalDiagnostic(task, time.Now()); err != nil || !ok {
		t.Fatalf("terminal: %v %v", ok, err)
	}
	stored, _ := repo.Task(task.ID)
	if stored.Status != model.TaskStatusSubmissionUncertain {
		t.Fatalf("unsafe status: %s", stored.Status)
	}
	if count, err := repo.ActiveTaskCountForUser(p.OwnerID); err != nil || count != 1 {
		t.Fatal("uncertain task lost from occupancy count", count, err)
	}
	if items, err := repo.Tasks(p.OwnerID, 20, "", true); err != nil || len(items) != 1 {
		t.Fatal("uncertain task missing from pending list", items, err)
	}
	req.Command.OperationID = "second"
	_, err = s.Submit(context.Background(), p, req)
	if err == nil || stub.calls != 1 {
		t.Fatal("uncertain request resubmitted")
	}
	var queued int64
	db.Model(&model.Task{}).Where("status = ?", model.TaskStatusQueued).Count(&queued)
	if queued != 0 {
		t.Fatal("uncertain task queued")
	}
}
func TestOldEpochResultRetainsAssetButDoesNotChangeRestoredCanvas(t *testing.T) {
	s, db, repo, cs, p, req, _ := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	_, err = cs.RestoreVersion(context.Background(), p, snap.CanvasID, snap.DocumentEpoch, "client", "restore", snap.Revision, snap.Document)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"old epoch output"}`})
	repo.EnqueueCanvasMaterialization(h.TaskID)
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	after, _ := cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("old result changed restored document")
	}
	outputs, err := repo.CanvasTaskOutputs(h.TaskID)
	if err != nil || len(outputs) != 1 {
		t.Fatal("old output lost")
	}
}
func TestTextResultDoesNotOverwriteNewerManualText(t *testing.T) {
	s, db, repo, cs, p, req, _ := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	_, err = cs.Execute(context.Background(), p, commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "manual", BaseRevision: snap.Revision, Operations: []commands.Operation{{Type: "update_node", ID: "target", Patch: map[string]any{"content": "manual draft"}}}})
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"provider answer"}`})
	repo.EnqueueCanvasMaterialization(h.TaskID)
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	raw, _ := json.Marshal(snap.Document)
	if !contains(string(raw), "manual draft") || contains(string(raw), "provider answer") {
		t.Fatal(string(raw))
	}
}
