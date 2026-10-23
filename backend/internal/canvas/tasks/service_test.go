package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/canvas/commands"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/identity"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"path/filepath"
	"reflect"
	"testing"
)

type stubSubmitter struct{ calls, wakes int }

func (s *stubSubmitter) Prepare(_ context.Context, p identity.Principal, req TaskRequest) (PreparedTask, error) {
	s.calls++
	return PreparedTask{Task: model.Task{ID: fmt.Sprintf("task-%d", s.calls), UserID: p.OwnerID, Type: "canvas_" + req.Mode, Prompt: req.Prompt, Status: model.TaskStatusQueued, InputJSON: `{}`}}, nil
}
func (s *stubSubmitter) Commit(_ context.Context, tx *repository.Repository, p PreparedTask) error {
	return tx.InsertPreparedCanvasTask(&p.Task)
}
func (s *stubSubmitter) Wake() { s.wakes++ }
func taskFixture(t *testing.T) (*Service, *gorm.DB, *repository.Repository, *commands.Service, identity.Principal, SubmitRequest, *stubSubmitter) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "tasks.db")+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d, _ := db.DB(); _ = d.Close() })
	if err = db.AutoMigrate(&model.User{}, &model.Task{}, &model.CanvasProject{}, &model.CanvasNodeEditLease{}, &model.Resource{}, &model.Asset{}); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	db.Create(&model.User{ID: "owner", Status: model.UserStatusActive})
	repo := repository.New(db)
	cs := commands.NewService(repo, commands.ServiceOptions{})
	p := identity.Principal{ID: "owner", OwnerID: "owner", Role: "owner", ClientID: "client", Scopes: []string{"canvas.read", "canvas.write", "generation.submit"}}
	snap, err := cs.Create(context.Background(), p, commands.CreateCanvasRequest{ClientID: "client", OperationID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	x, y := 0.0, 0.0
	_, err = cs.Execute(context.Background(), p, commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "node", BaseRevision: snap.Revision, Operations: []commands.Operation{{Type: "add_node", ID: "target", NodeType: "text", X: &x, Y: &y}}})
	if err != nil {
		t.Fatal(err)
	}
	snap, err = cs.ReadSnapshot(context.Background(), p, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	submitter := &stubSubmitter{}
	svc := NewService(repo, cs, submitter)
	req := SubmitRequest{Command: commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "submit", BaseRevision: snap.Revision}, TargetNodeID: "target", Task: TaskRequest{Mode: "text", Prompt: "hello"}}
	return svc, db, repo, cs, p, req, submitter
}
func TestRetryCreatesOneTaskAndOneWake(t *testing.T) {
	s, db, _, _, p, req, stub := taskFixture(t)
	a, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || stub.calls != 1 || stub.wakes != 1 {
		t.Fatalf("duplicate submission: %+v %+v prepare=%d wake=%d", a, b, stub.calls, stub.wakes)
	}
	var count int64
	db.Model(&model.Task{}).Count(&count)
	if count != 1 {
		t.Fatal(count)
	}
	req.Task.Prompt = "changed"
	if _, err = s.Submit(context.Background(), p, req); err == nil {
		t.Fatal("reused operation accepted")
	}
}
func TestAdmissionUsesDurableBindingRatherThanRevertedNodeTaskMetadata(t *testing.T) {
	s, db, _, cs, p, req, stub := taskFixture(t)
	first, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	req.Command.BaseRevision = snap.Revision
	req.Command.OperationID = "next"
	// A terminal task may retain queued metadata after a historical undo.
	db.Model(&model.Task{}).Where("id = ?", first.TaskID).Update("status", model.TaskStatusFailed)
	second, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatalf("stale presentation blocks new task: %v", err)
	}
	if first.TaskID == second.TaskID || stub.calls != 2 {
		t.Fatal("new task identity missing")
	}
	// Conversely, losing the visible task id must not permit a second active task.
	snap, err = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	nodes := objectList(snap.Document["nodes"])
	delete(metadata(nodes[0]), "taskId")
	delete(metadata(nodes[0]), "taskStatus")
	raw, _ := json.Marshal(snap.Document)
	db.Model(&model.CanvasProject{}).Where("id = ?", req.Command.CanvasID).Update("payload_json", string(raw))
	req.Command.BaseRevision = snap.Revision
	req.Command.OperationID = "third"
	if _, err = s.Submit(context.Background(), p, req); err == nil {
		t.Fatal("active immutable binding ignored")
	}
	if stub.calls != 2 {
		t.Fatal("active target reached provider preparation")
	}
}
func TestTaskAndPendingRollbackOnReceiptFailure(t *testing.T) {
	s, db, _, cs, p, req, stub := taskFixture(t)
	before, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	db.Exec(`CREATE TRIGGER receipt_failure BEFORE INSERT ON canvas_command_receipts BEGIN SELECT RAISE(ABORT,'failed'); END;`)
	if _, err := s.Submit(context.Background(), p, req); err == nil {
		t.Fatal("receipt failure acknowledged")
	}
	after, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if !reflect.DeepEqual(before, after) || stub.wakes != 0 {
		t.Fatal("partial document or wake")
	}
	var count int64
	db.Model(&model.Task{}).Count(&count)
	if count != 0 {
		t.Fatal("orphan task")
	}
}
func TestMaterializerWithoutWindowAndRetry(t *testing.T) {
	s, db, repo, cs, p, req, _ := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"durable answer"}`})
	if err = repo.EnqueueCanvasMaterialization(h.TaskID); err != nil {
		t.Fatal(err)
	}
	// Simulate a new host instance: no browser, no original service cache.
	resumed := NewService(repo, cs, &stubSubmitter{})
	if err = resumed.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	first, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if err = resumed.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	second, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	raw, _ := json.Marshal(first.Document)
	if !reflect.DeepEqual(first, second) || !contains(string(raw), "durable answer") {
		t.Fatalf("missing or duplicate result: %s", raw)
	}
}
func TestDeletedTargetKeepsOutputWithoutRecreatingNode(t *testing.T) {
	s, db, repo, cs, p, req, _ := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	_, err = cs.Execute(context.Background(), p, commands.Command{Protocol: 1, CanvasID: snap.CanvasID, DocumentEpoch: snap.DocumentEpoch, ClientID: "client", OperationID: "delete", BaseRevision: snap.Revision, Operations: []commands.Operation{{Type: "delete_node", ID: "target"}}})
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"retained output"}`})
	if err = repo.EnqueueCanvasMaterialization(h.TaskID); err != nil {
		t.Fatal(err)
	}
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	snap, _ = cs.ReadSnapshot(context.Background(), p, req.Command.CanvasID)
	if len(snap.Document["nodes"].([]any)) != 0 {
		t.Fatal("deleted node recreated")
	}
	var assetCount int64
	db.Model(&model.Asset{}).Count(&assetCount)
	if assetCount != 1 {
		t.Fatalf("output lost: %d", assetCount)
	}
}
func TestMaterializationJobRemainsPendingOnApplyFailure(t *testing.T) {
	s, db, repo, _, p, req, _ := taskFixture(t)
	h, err := s.Submit(context.Background(), p, req)
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&model.Task{}).Where("id = ?", h.TaskID).Updates(map[string]any{"status": model.TaskStatusSucceeded, "result_json": `{"text":"saved"}`})
	repo.EnqueueCanvasMaterialization(h.TaskID)
	db.Exec(`CREATE TRIGGER result_failure BEFORE INSERT ON canvas_command_receipts BEGIN SELECT RAISE(ABORT,'failed'); END;`)
	if err = s.Materialize(context.Background(), h.TaskID); err == nil {
		t.Fatal("apply failure ignored")
	}
	jobs, err := repo.PendingCanvasMaterializations(10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("job lost: %v %+v", err, jobs)
	}
	db.Exec(`DROP TRIGGER result_failure`)
	if err = s.Materialize(context.Background(), h.TaskID); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.Asset{}).Count(&count)
	if count != 1 {
		t.Fatal("duplicate output")
	}
}
func TestViewerCannotSubmit(t *testing.T) {
	s, _, _, _, p, req, stub := taskFixture(t)
	p.Role = "viewer"
	_, err := s.Submit(context.Background(), p, req)
	if err == nil || stub.calls != 0 {
		t.Fatal(errors.New("viewer submitted"))
	}
}
