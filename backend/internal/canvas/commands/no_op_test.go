package commands

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/identity"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func noOpRequest(snap Snapshot, operationID string, ops ...Operation) Command {
	req := command(ops...)
	req.CanvasID, req.DocumentEpoch, req.BaseRevision, req.OperationID = snap.CanvasID, snap.DocumentEpoch, snap.Revision, operationID
	return req
}

func noOpStoredCounts(t *testing.T, db *gorm.DB, canvasID string) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	for name, row := range map[string]any{
		"events": &model.CanvasCommandEvent{}, "history": &model.CanvasActorHistory{},
		"snapshots": &model.CanvasSnapshot{}, "versions": &model.CanvasEntityVersion{},
		"resource references": &model.CanvasCommandResource{},
	} {
		var count int64
		if err := db.Model(row).Where("canvas_id = ?", canvasID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		counts[name] = count
	}
	return counts
}

func TestNoOpCommandsPersistOnlyRecoverableReceipt(t *testing.T) {
	for _, kind := range []string{"rename", "empty custom appearance"} {
		t.Run(kind, func(t *testing.T) {
			svc, db, path, snap := commandFixture(t)
			ctx, p := context.Background(), ownerContext().Principal
			seed := noOpRequest(snap, "seed-appearance", Operation{Type: "edit_canvas_settings", ID: snap.CanvasID, Patch: map[string]any{"appearance": map[string]any{"mode": "custom", "custom": map[string]any{"baseTheme": "dark", "gridColor": "#123456"}}}})
			if _, err := svc.Execute(ctx, p, seed); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CanvasSnapshot{}).Where("canvas_id = ?", snap.CanvasID).Update("created_at", time.Now().Add(-10*time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			snap, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			title := text(snap.Document["title"])
			op := Operation{Type: "rename_canvas", ID: snap.CanvasID, Title: &title}
			if kind == "empty custom appearance" {
				op = Operation{Type: "edit_canvas_settings", ID: snap.CanvasID, Patch: map[string]any{"appearance": map[string]any{"custom": map[string]any{}}}}
			}
			req := noOpRequest(snap, "no-op", op)
			repo := repository.New(db)
			beforeRow, err := repo.CanvasProjectForUser(p.OwnerID, snap.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			beforeEpoch, err := repo.CommandEpoch(p.OwnerID, snap.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			counts := noOpStoredCounts(t, db, snap.CanvasID)
			committed := 0
			svc.options.AfterCommit = func(Receipt) { committed++ }
			first, err := svc.Execute(ctx, p, req)
			if err != nil {
				t.Fatal(err)
			}
			if first.Revision != snap.Revision || first.EventSeq != snap.EventSeq {
				t.Errorf("no-op advanced revision/sequence: got %d/%d, want %d/%d", first.Revision, first.EventSeq, snap.Revision, snap.EventSeq)
			}
			if len(first.Changes.Entities) != 0 || len(first.Changes.Fields) != 0 || first.Changes.NodeOrder != nil || first.Changes.ConnectionOrder != nil || len(first.Changes.RecreatedEntities) != 0 || len(first.EntityVersions) != 0 || len(first.EntityGenerations) != 0 {
				t.Errorf("no-op returned document writes: %+v", first)
			}
			for _, operationID := range []string{"no-op-2", "no-op-3", "no-op-4", "no-op-5"} {
				next := req
				next.OperationID = operationID
				receipt, err := svc.Execute(ctx, p, next)
				if err != nil || receipt.Revision != snap.Revision || receipt.EventSeq != snap.EventSeq {
					t.Errorf("distinct no-op advanced document: %+v %v", receipt, err)
				}
			}
			if committed != 0 {
				t.Errorf("no-op called AfterCommit %d times", committed)
			}
			after, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
			if err != nil || !reflect.DeepEqual(snap, after) {
				t.Errorf("no-op changed durable snapshot: %+v %v", after, err)
			}
			afterRow, err := repo.CanvasProjectForUser(p.OwnerID, snap.CanvasID)
			if err != nil || !reflect.DeepEqual(beforeRow, afterRow) {
				t.Errorf("no-op rewrote canvas row: %+v %v", afterRow, err)
			}
			afterEpoch, err := repo.CommandEpoch(p.OwnerID, snap.CanvasID)
			if err != nil || !reflect.DeepEqual(beforeEpoch, afterEpoch) {
				t.Errorf("no-op rewrote document epoch: %+v %v", afterEpoch, err)
			}
			if got := noOpStoredCounts(t, db, snap.CanvasID); !reflect.DeepEqual(counts, got) {
				t.Errorf("no-op created document/history rows: before=%v after=%v", counts, got)
			}
			var receiptCount int64
			if err := db.Model(&model.CanvasCommandReceipt{}).Where("canvas_id = ? AND operation_id = ?", snap.CanvasID, req.OperationID).Count(&receiptCount).Error; err != nil || receiptCount != 1 {
				t.Fatalf("no-op missing durable receipt: count=%d %v", receiptCount, err)
			}
			reopened := NewService(independentCommandRepository(t, path), ServiceOptions{})
			read, err := reopened.GetReceipt(ctx, p, req.CanvasID, req.DocumentEpoch, req.ClientID, req.OperationID)
			if err != nil || !reflect.DeepEqual(first, read) {
				t.Fatalf("receipt recovery differs from ACK: %+v %v", read, err)
			}
			replayed, err := reopened.Execute(ctx, p, req)
			if err != nil || !reflect.DeepEqual(first, replayed) {
				t.Fatalf("no-op retry differs from ACK: %+v %v", replayed, err)
			}
			_, err = reopened.GetReceipt(ctx, p, req.CanvasID, req.DocumentEpoch, "another-client", req.OperationID)
			assertReason(t, err, "receipt_missing")
			reused := req
			otherTitle := "different command"
			reused.Operations = []Operation{{Type: "rename_canvas", ID: snap.CanvasID, Title: &otherTitle}}
			_, err = reopened.Execute(ctx, p, reused)
			assertReason(t, err, "operation_id_reused")
			if got := noOpStoredCounts(t, db, snap.CanvasID); !reflect.DeepEqual(counts, got) {
				t.Errorf("no-op retries changed durable rows: before=%v after=%v", counts, got)
			}
			if _, err = svc.Execute(ctx, p, edit(after, "a", "later edit", "after-no-op")); err != nil {
				t.Fatal(err)
			}
			if committed != 1 {
				t.Errorf("real edit AfterCommit count=%d, want 1", committed)
			}
			old, err := reopened.Execute(ctx, p, req)
			if err != nil || !reflect.DeepEqual(first, old) {
				t.Fatalf("later edits changed original no-op receipt: %+v %v", old, err)
			}
		})
	}
}

func TestNoOpCommandPreservesRedo(t *testing.T) {
	svc, _, _, snap := commandFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	if _, err := svc.Execute(ctx, p, edit(snap, "a", "redo me", "real-edit")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", "undo-real-edit"); err != nil {
		t.Fatal(err)
	}
	snap, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	title := text(snap.Document["title"])
	if _, err = svc.Execute(ctx, p, noOpRequest(snap, "no-op-before-redo", Operation{Type: "rename_canvas", ID: snap.CanvasID, Title: &title})); err != nil {
		t.Fatal(err)
	}
	state, err := svc.ReadHistoryState(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client")
	if err != nil || !state.CanRedo {
		t.Fatalf("no-op abandoned redo: %+v %v", state, err)
	}
	if _, err = svc.Redo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", "redo-real-edit"); err != nil {
		t.Fatal(err)
	}
	after, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if err != nil || entity(after.Document, "node:a")["metadata"].(map[string]any)["content"] != "redo me" {
		t.Fatalf("redo did not restore real edit: %+v %v", after, err)
	}
}

func TestNoOpCommandRejectsUnobservedCursorWithCompleteEntityVersions(t *testing.T) {
	for _, kind := range []string{"rename", "empty custom appearance"} {
		t.Run(kind, func(t *testing.T) {
			svc, db, _, snap := commandFixture(t)
			ctx, p := context.Background(), ownerContext().Principal
			seed := noOpRequest(snap, "seed-appearance", Operation{Type: "edit_canvas_settings", ID: snap.CanvasID, Patch: map[string]any{"appearance": map[string]any{"mode": "custom", "custom": map[string]any{"baseTheme": "dark", "gridColor": "#123456"}}}})
			if _, err := svc.Execute(ctx, p, seed); err != nil {
				t.Fatal(err)
			}
			snap, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			title := text(snap.Document["title"])
			op := Operation{Type: "rename_canvas", ID: snap.CanvasID, Title: &title}
			if kind == "empty custom appearance" {
				op = Operation{Type: "edit_canvas_settings", ID: snap.CanvasID, Patch: map[string]any{"appearance": map[string]any{"custom": map[string]any{}}}}
			}
			req := noOpRequest(snap, "unseen-cursor-no-op", op)
			req.ExpectedEntityVersions = map[string]int64{"project:title": snap.EntityVersions["project:title"], "project:appearance": snap.EntityVersions["project:appearance"]}
			if _, err = svc.Execute(ctx, p, edit(snap, "b", "remote edit unseen by this client", "remote-edit")); err != nil {
				t.Fatal(err)
			}
			current, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
			if err != nil {
				t.Fatal(err)
			}
			counts := noOpStoredCounts(t, db, snap.CanvasID)
			committed := 0
			svc.options.AfterCommit = func(Receipt) { committed++ }
			_, err = svc.Execute(ctx, p, req)
			assertReason(t, err, "revision_conflict")
			after, err := svc.ReadSnapshot(ctx, p, snap.CanvasID)
			if err != nil || !reflect.DeepEqual(current, after) {
				t.Fatalf("rejected no-op changed current canvas: %+v %v", after, err)
			}
			if got := noOpStoredCounts(t, db, snap.CanvasID); !reflect.DeepEqual(counts, got) || committed != 0 {
				t.Fatalf("rejected no-op wrote history or emitted: before=%v after=%v emitted=%d", counts, got, committed)
			}
			_, err = svc.GetReceipt(ctx, p, req.CanvasID, req.DocumentEpoch, req.ClientID, req.OperationID)
			assertReason(t, err, "receipt_missing")
			req.BaseRevision = current.Revision
			receipt, err := svc.Execute(ctx, p, req)
			if err != nil || receipt.Revision != current.Revision || receipt.EventSeq != current.EventSeq || committed != 0 {
				t.Fatalf("observed current baseline did not settle without a new event: %+v %v", receipt, err)
			}
		})
	}
}

func TestNoOpCommandRetainsAuthorityAndPreconditions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		change func(*Command, *identity.Principal, Snapshot)
	}{
		{"viewer", "permission_denied", func(_ *Command, p *identity.Principal, _ Snapshot) { p.Role = "viewer" }},
		{"stale revision", "revision_conflict", func(req *Command, _ *identity.Principal, _ Snapshot) { req.BaseRevision-- }},
		{"stale entity", "entity_version_conflict", func(req *Command, _ *identity.Principal, _ Snapshot) {
			req.ExpectedEntityVersions = map[string]int64{"project:title": -1}
		}},
		{"old epoch", "document_epoch_mismatch", func(req *Command, _ *identity.Principal, _ Snapshot) { req.DocumentEpoch = "old-epoch" }},
		{"wrong generation", "entity_version_conflict", func(req *Command, _ *identity.Principal, snap Snapshot) {
			op := baselineOperation(t, "a", map[string]any{"content": ""}, `{"nodeType":"text","fields":{"content":{"exists":true,"value":""}}}`)
			generation := snap.EntityGenerations["node:a"] + 1
			op.FieldBaselines.Generation = &generation
			req.Operations = []Operation{op}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, db, _, snap := commandFixture(t)
			ctx, p := context.Background(), ownerContext().Principal
			title := text(snap.Document["title"])
			req := noOpRequest(snap, "rejected-no-op", Operation{Type: "rename_canvas", ID: snap.CanvasID, Title: &title})
			tc.change(&req, &p, snap)
			counts := noOpStoredCounts(t, db, snap.CanvasID)
			_, err := svc.Execute(ctx, p, req)
			assertReason(t, err, tc.reason)
			after, err := svc.ReadSnapshot(ctx, ownerContext().Principal, snap.CanvasID)
			if err != nil || !reflect.DeepEqual(snap, after) {
				t.Fatalf("rejected no-op changed snapshot: %+v %v", after, err)
			}
			if got := noOpStoredCounts(t, db, snap.CanvasID); !reflect.DeepEqual(counts, got) {
				t.Fatalf("rejected no-op wrote history: before=%v after=%v", counts, got)
			}
			var receipts int64
			if err := db.Model(&model.CanvasCommandReceipt{}).Where("canvas_id = ? AND operation_id = ?", snap.CanvasID, req.OperationID).Count(&receipts).Error; err != nil || receipts != 0 {
				t.Fatalf("rejected no-op saved receipt: %d %v", receipts, err)
			}
		})
	}
}

func TestNoOpOptimizationRetainsSameMediaSelection(t *testing.T) {
	svc, _, snap, _ := managedHistoryFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	before := mediaversions.ReadState(entity(snap.Document, "node:a"))
	for _, operationID := range []string{"select-current", "select-current-again"} {
		receipt, err := svc.Execute(ctx, p, noOpRequest(snap, operationID, Operation{Type: "use_media_version", ID: "a", VersionID: before.CurrentVersionID}))
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Revision != snap.Revision+1 || receipt.EventSeq != snap.EventSeq+1 || len(receipt.Changes.Entities) != 1 {
			t.Fatalf("same-media selection was swallowed: %+v", receipt)
		}
		snap, err = svc.ReadSnapshot(ctx, p, snap.CanvasID)
		if err != nil {
			t.Fatal(err)
		}
		after := mediaversions.ReadState(entity(snap.Document, "node:a"))
		if after.SelectionRevision != before.SelectionRevision+1 || after.CurrentVersionID != before.CurrentVersionID {
			t.Fatalf("same-media selection lost user intent: before=%+v after=%+v", before, after)
		}
		before = after
	}
}

func TestNoOpOptimizationLeavesHostEffectsAndHistoryIntact(t *testing.T) {
	svc, db, _, snap := commandFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	req := noOpRequest(snap, "host-side-effect")
	mutate := func(tx *repository.Repository, doc Document) (Document, map[string]bool, error) {
		return doc, nil, tx.Create(&model.Task{ID: "host-side-effect-task", UserID: p.OwnerID, Status: model.TaskStatusQueued})
	}
	first, fresh, err := svc.ExecuteHost(ctx, p, req, "task_result", nil, true, mutate)
	if err != nil || !fresh {
		t.Fatalf("host side effect failed: %+v fresh=%v %v", first, fresh, err)
	}
	if first.Revision != snap.Revision+1 || first.EventSeq != snap.EventSeq+1 {
		t.Fatalf("host transaction used ordinary no-op path: %+v", first)
	}
	if _, err := repository.New(db).Task("host-side-effect-task"); err != nil {
		t.Fatal("host side effect missing:", err)
	}
	history, err := repository.New(db).ActorHistoryForCommand(receiptKey(p, req), first.EventSeq)
	if err != nil || history.State != "applied" {
		t.Fatalf("host history unavailable for result replay: %+v %v", history, err)
	}
	replayed, fresh, err := svc.ExecuteHost(ctx, p, req, "task_result", nil, true, mutate)
	if err != nil || fresh || !reflect.DeepEqual(first, replayed) {
		t.Fatalf("host retry reran effect or changed receipt: %+v fresh=%v %v", replayed, fresh, err)
	}
	var taskCount int64
	if err := db.Model(&model.Task{}).Where("id = ?", "host-side-effect-task").Count(&taskCount).Error; err != nil || taskCount != 1 {
		t.Fatalf("host side effect count=%d %v", taskCount, err)
	}
}

func TestNoOpReceiptWriteFailureDoesNotAcknowledgeOrChangeCanvas(t *testing.T) {
	svc, db, _, snap := commandFixture(t)
	if err := db.Exec(`CREATE TRIGGER fail_no_op_receipt BEFORE INSERT ON canvas_command_receipts BEGIN SELECT RAISE(ABORT, 'receipt unavailable'); END;`).Error; err != nil {
		t.Fatal(err)
	}
	counts := noOpStoredCounts(t, db, snap.CanvasID)
	title := text(snap.Document["title"])
	req := noOpRequest(snap, "failed-no-op", Operation{Type: "rename_canvas", ID: snap.CanvasID, Title: &title})
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, req); err == nil {
		t.Fatal("failed no-op receipt was acknowledged")
	}
	after, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, snap.CanvasID)
	beforeJSON, _ := json.Marshal(snap)
	afterJSON, _ := json.Marshal(after)
	if err != nil || string(beforeJSON) != string(afterJSON) {
		t.Fatalf("failed no-op changed snapshot: %+v %v", after, err)
	}
	if got := noOpStoredCounts(t, db, snap.CanvasID); !reflect.DeepEqual(counts, got) {
		t.Fatalf("failed no-op changed durable rows: before=%v after=%v", counts, got)
	}
}
