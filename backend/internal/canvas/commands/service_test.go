package commands

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"infinite-canvas/backend/internal/database"
	"infinite-canvas/backend/internal/model"
	"infinite-canvas/backend/internal/repository"
	"path/filepath"
	"reflect"
	"testing"
)

func commandFixture(t *testing.T) (*Service, *gorm.DB, string, Snapshot) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "commands.db")
	db, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_synchronous=FULL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err = db.AutoMigrate(&model.User{}, &model.CanvasProject{}, &model.Resource{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	// Semantic output binding rechecks durable task occupancy, so the accepted
	// binding tables have to exist rather than be swallowed as missing.
	if err = database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewService(repository.New(db), ServiceOptions{})
	snapshot, err := svc.Create(context.Background(), ownerContext().Principal, CreateCanvasRequest{OperationID: "create1", ClientID: "client", Title: "画布"})
	if err != nil {
		t.Fatal(err)
	}
	add := command(Operation{Type: "add_node", ID: "a", NodeType: "text", X: num(0), Y: num(0)}, Operation{Type: "add_node", ID: "b", NodeType: "text", X: num(1000), Y: num(0)})
	add.CanvasID, add.DocumentEpoch, add.BaseRevision = snapshot.CanvasID, snapshot.DocumentEpoch, snapshot.Revision
	if _, err = svc.Execute(context.Background(), ownerContext().Principal, add); err != nil {
		t.Fatal(err)
	}
	snapshot, err = svc.ReadSnapshot(context.Background(), ownerContext().Principal, snapshot.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	return svc, db, path, snapshot
}
func num(n float64) *float64 { return &n }
func edit(snapshot Snapshot, id, content, operationID string) Command {
	req := command(Operation{Type: "update_node", ID: id, Patch: map[string]any{"content": content}})
	req.CanvasID, req.DocumentEpoch, req.OperationID, req.BaseRevision = snapshot.CanvasID, snapshot.DocumentEpoch, operationID, snapshot.Revision
	return req
}
func TestCommitRollbackWhenReceiptWriteFails(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	if err := db.Exec(`CREATE TRIGGER fail_receipts BEFORE INSERT ON canvas_command_receipts BEGIN SELECT RAISE(ABORT, 'receipt unavailable'); END;`).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, edit(before, "a", "changed", "fail")); err == nil {
		t.Fatal("receipt failure acknowledged")
	}
	after, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatalf("partial commit: %s / %s", a, b)
	}
	var count int64
	db.Model(&model.CanvasActorHistory{}).Count(&count)
	if count != 1 {
		t.Fatalf("history committed separately: %d", count)
	}
}
func TestRetryReturnsOriginalReceipt(t *testing.T) {
	svc, db, _, snapshot := commandFixture(t)
	req := edit(snapshot, "a", "saved", "retry1")
	first, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, repeat) {
		t.Fatalf("receipt changed: %+v %+v", first, repeat)
	}
	var count int64
	db.Model(&model.CanvasCommandEvent{}).Where("operation_id = ?", "retry1").Count(&count)
	if count != 1 {
		t.Fatalf("duplicate events: %d", count)
	}
	req.Operations[0].Patch["content"] = "different"
	_, err = svc.Execute(context.Background(), ownerContext().Principal, req)
	assertReason(t, err, "operation_id_reused")
}
func TestRestartKeepsReceipt(t *testing.T) {
	svc, db, path, snapshot := commandFixture(t)
	req := edit(snapshot, "a", "saved", "restart1")
	first, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	if err = sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := gorm.Open(sqlite.Open(path+"?_busy_timeout=5000&_journal_mode=WAL&_synchronous=FULL"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := reopened.DB(); _ = sqlDB.Close() })
	repeat, err := NewService(repository.New(reopened), ServiceOptions{}).Execute(context.Background(), ownerContext().Principal, req)
	if err != nil || !reflect.DeepEqual(first, repeat) {
		t.Fatalf("restart lost receipt: %v %+v", err, repeat)
	}
}
func TestUnrelatedStaleEditSucceedsAndMissingVersionsUsesStrictCAS(t *testing.T) {
	svc, _, _, snapshot := commandFixture(t)
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, edit(snapshot, "a", "alice", "a1")); err != nil {
		t.Fatal(err)
	}
	stale := edit(snapshot, "b", "bob", "b1")
	stale.ExpectedEntityVersions = map[string]int64{"node:b": snapshot.EntityVersions["node:b"]}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, stale); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Execute(context.Background(), ownerContext().Principal, edit(snapshot, "b", "late", "b2"))
	assertReason(t, err, "revision_conflict")
}
func TestDeleteThenLateEditRejected(t *testing.T) {
	svc, _, _, snapshot := commandFixture(t)
	del := edit(snapshot, "a", "", "delete1")
	del.Operations = []Operation{{Type: "delete_node", ID: "a"}}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, del); err != nil {
		t.Fatal(err)
	}
	late := edit(snapshot, "a", "late", "late1")
	late.ExpectedEntityVersions = map[string]int64{"node:a": snapshot.EntityVersions["node:a"]}
	_, err := svc.Execute(context.Background(), ownerContext().Principal, late)
	assertReason(t, err, "entity_deleted")
}
func TestHistoryConflictAndUnrelatedEdit(t *testing.T) {
	for _, sameNode := range []bool{true, false} {
		t.Run(map[bool]string{true: "same", false: "unrelated"}[sameNode], func(t *testing.T) {
			svc, _, _, snapshot := commandFixture(t)
			a := edit(snapshot, "a", "alice", "alice1")
			a.ClientID = "alice"
			if _, err := svc.Execute(context.Background(), ownerContext().Principal, a); err != nil {
				t.Fatal(err)
			}
			current, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, snapshot.CanvasID)
			id := "b"
			if sameNode {
				id = "a"
			}
			b := edit(current, id, "bob", "bob1")
			b.ClientID = "bob"
			if _, err := svc.Execute(context.Background(), ownerContext().Principal, b); err != nil {
				t.Fatal(err)
			}
			_, err := svc.Undo(context.Background(), ownerContext().Principal, snapshot.CanvasID, snapshot.DocumentEpoch, "alice", "undo1")
			if sameNode {
				assertReason(t, err, "history_conflict")
			} else {
				if err != nil {
					t.Fatal(err)
				}
				current, _ = svc.ReadSnapshot(context.Background(), ownerContext().Principal, snapshot.CanvasID)
				if entity(current.Document, "node:b")["metadata"].(map[string]any)["content"] != "bob" {
					t.Fatal("undo clobbered other editor")
				}
			}
		})
	}
}
func TestRestoreChangesEpoch(t *testing.T) {
	svc, _, _, snapshot := commandFixture(t)
	restored, err := svc.RestoreVersion(context.Background(), ownerContext().Principal, snapshot.CanvasID, snapshot.DocumentEpoch, "client", "restore1", snapshot.Revision, snapshot.Document)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DocumentEpoch == snapshot.DocumentEpoch {
		t.Fatal("restore did not change epoch")
	}
	_, err = svc.Execute(context.Background(), ownerContext().Principal, edit(snapshot, "a", "old", "old1"))
	assertReason(t, err, "document_epoch_mismatch")
}

func TestCreateWithInitialCommandsIsAtomicAndRetryable(t *testing.T) {
	svc, db, _, _ := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	req := CreateCanvasRequest{ClientID: "client", OperationID: "create-seed", Title: "Seed", InitialOperations: []Operation{{Type: "add_node", ID: "seed", NodeType: "text", Content: ptr("seed text")}}}
	snap, err := svc.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	if text(entity(snap.Document, "node:seed")["metadata"].(map[string]any)["content"]) != "seed text" || snap.EntityVersions["node:seed"] != 1 {
		t.Fatal("initial command was not persisted", snap)
	}
	again, err := svc.Create(ctx, p, req)
	if err != nil || again.CanvasID != snap.CanvasID {
		t.Fatal("creation duplicated", err)
	}
	var before int64
	db.Model(&model.CanvasProject{}).Count(&before)
	req.OperationID = "bad-seed"
	req.InitialOperations = append(req.InitialOperations, Operation{Type: "set_parent", ID: "seed", ParentID: "missing"})
	if _, err := svc.Create(ctx, p, req); err == nil {
		t.Fatal("bad creation accepted")
	}
	var after int64
	db.Model(&model.CanvasProject{}).Count(&after)
	if before != after {
		t.Fatal("failed creation left a canvas")
	}
	req.InitialOperations = nil
	req.ProjectID = "foreign-project"
	if _, err := svc.Create(ctx, p, req); err == nil {
		t.Fatal("foreign project accepted")
	}
}
func TestCanvasLifecycleUsesCommandReceipts(t *testing.T) {
	svc, _, _, snapshot := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	req := edit(snapshot, snapshot.CanvasID, "", "rename1")
	req.Operations = []Operation{{Type: "rename_canvas", ID: snapshot.CanvasID, Title: ptr("新的名称")}}
	if _, err := svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	current, err := svc.ReadSnapshot(ctx, p, snapshot.CanvasID)
	if err != nil || current.Document["title"] != "新的名称" {
		t.Fatalf("rename failed: %v", err)
	}
	req = edit(current, snapshot.CanvasID, "", "delete1")
	req.Operations = []Operation{{Type: "delete_canvas", ID: snapshot.CanvasID}}
	if _, err = svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Execute(ctx, p, req); err != nil {
		t.Fatalf("delete retry lost receipt: %v", err)
	}
	_, err = svc.ReadSnapshot(ctx, p, snapshot.CanvasID)
	assertReason(t, err, "entity_deleted")
	restored, err := svc.RestoreVersion(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client", "restore-deleted", current.Revision+1, snapshot.Document)
	if err != nil || restored.DocumentEpoch == snapshot.DocumentEpoch {
		t.Fatalf("cannot restore deleted canvas: %v", err)
	}
}
func TestConcurrentRetriesCommitOneEvent(t *testing.T) {
	svc, db, _, snapshot := commandFixture(t)
	req := edit(snapshot, "a", "once", "concurrent")
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, err := svc.Execute(context.Background(), ownerContext().Principal, req); failures <- err }()
	}
	for i := 0; i < 8; i++ {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	db.Model(&model.CanvasCommandEvent{}).Where("operation_id = ?", "concurrent").Count(&count)
	if count != 1 {
		t.Fatalf("concurrent retries committed %d events", count)
	}
}
func TestCommandHistoryProtectsResourceAliases(t *testing.T) {
	_, db, _, snapshot := commandFixture(t)
	if err := db.AutoMigrate(&model.CanvasSnapshotResource{}); err != nil {
		t.Fatal(err)
	}
	resource := model.Resource{ID: "media1", UserID: "owner", Provider: "local", Status: model.ResourceStatusReady, ObjectKey: "media.png", Endpoint: "local", Bucket: "local"}
	if err := db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	alias := resource
	alias.ID = "media-alias"
	if err := db.Create(&alias).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.CanvasCommandResource{OwnerID: "owner", CanvasID: snapshot.CanvasID, Epoch: snapshot.DocumentEpoch, OperationID: "history1", Kind: "history", ResourceID: resource.ID}).Error; err != nil {
		t.Fatal(err)
	}
	protected, err := repository.New(db).CanvasHistoryReferencesObject(&alias)
	if err != nil || !protected {
		t.Fatalf("command history resource can be deleted through its alias: %v %v", protected, err)
	}
}
func TestAssetBindingRejectsMislabeledResource(t *testing.T) {
	svc, db, _, snapshot := commandFixture(t)
	if err := db.AutoMigrate(&model.Asset{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Resource{ID: "audio1", UserID: "owner", Kind: "audio", MimeType: "audio/wav", Status: model.ResourceStatusReady}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Asset{ID: "image-asset", UserID: "owner", Kind: "image", Status: model.AssetVersionStatusConfirmed, PayloadJSON: `{"data":{"storageKey":"resource:audio1"}}`}).Error; err != nil {
		t.Fatal(err)
	}
	req := edit(snapshot, "i", "", "add-image")
	req.Operations = []Operation{{Type: "add_node", ID: "i", NodeType: "image", X: num(2000), Y: num(0)}}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, req); err != nil {
		t.Fatal(err)
	}
	current, _ := svc.ReadSnapshot(context.Background(), ownerContext().Principal, snapshot.CanvasID)
	req = edit(current, "i", "", "bind")
	req.Operations = []Operation{{Type: "bind_asset", ID: "i", AssetID: "image-asset"}}
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, req); err == nil {
		t.Fatal("audio resource bound to image node")
	}
}
func assertReason(t *testing.T, err error, reason string) {
	t.Helper()
	var typed *Error
	if !errors.As(err, &typed) || typed.Reason != reason {
		t.Fatalf("want %s got %v", reason, err)
	}
}
