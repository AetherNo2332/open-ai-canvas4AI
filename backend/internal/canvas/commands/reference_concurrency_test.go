package commands

import (
	"context"
	"testing"

	"infinite-canvas/backend/internal/model"
)

// referenceCanvas seeds a board with one row plus the media node the row may reference.
func referenceCanvas(t *testing.T, svc *Service, snap Snapshot, operationID string) Snapshot {
	t.Helper()
	req := command(
		Operation{Type: "add_node", ID: "asset", NodeType: "image", X: num(0), Y: num(0)},
		Operation{Type: "add_node", ID: "board", NodeType: "script", X: num(500), Y: num(0)},
		Operation{Type: "edit_storyboard", ID: "board", Action: "append", RowID: "row", Patch: map[string]any{"durationSeconds": float64(5), "plotDescription": "test"}},
	)
	req.CanvasID, req.DocumentEpoch, req.BaseRevision, req.OperationID = snap.CanvasID, snap.DocumentEpoch, snap.Revision, operationID
	if _, err := svc.Execute(context.Background(), ownerContext().Principal, req); err != nil {
		t.Fatal(err)
	}
	current, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func referenceRow(t *testing.T, doc Document) map[string]any {
	t.Helper()
	board := entity(doc, "node:board")
	if board == nil {
		t.Fatal("board missing")
	}
	rows := maps(board["metadata"].(map[string]any)["storyboard"].(map[string]any)["rows"])
	if len(rows) != 1 {
		t.Fatalf("want one row got %d", len(rows))
	}
	return rows[0]
}

// A row whose accepted task is still running must not be re-owned, even though the
// row metadata that history can revert says nothing about it.
func TestReferenceSemanticOutputRejectsDurableActiveTask(t *testing.T) {
	svc, db, _, snap := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	current := referenceCanvas(t, svc, snap, "setup-occupancy")
	if err := db.Create(&model.Task{ID: "task-active", UserID: "owner", Type: "canvas_image", Status: model.TaskStatusRunning}).Error; err != nil {
		t.Fatal(err)
	}
	binding := model.CanvasTaskBinding{TaskID: "task-active", OwnerID: "owner", CanvasID: current.CanvasID, Epoch: current.DocumentEpoch, ActorID: "owner", ClientID: "client", OperationID: "task-op", TargetNodeID: "board", Mode: "image", ConsumerJSON: `{"kind":"storyboard","rowId":"row","field":"imageNodeId","outputNodeId":"asset"}`}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	bind := command(Operation{Type: "bind_storyboard_output", ID: "board", RowID: "row", OutputMode: "image", SourceNodeID: "asset"})
	bind.CanvasID, bind.DocumentEpoch, bind.BaseRevision, bind.OperationID = current.CanvasID, current.DocumentEpoch, current.Revision, "bind-busy"
	if _, err := svc.Execute(ctx, p, bind); err == nil {
		t.Fatal("an occupied row accepted a new output owner")
	} else {
		assertReason(t, err, "reference_busy")
	}
	after, err := svc.ReadSnapshot(ctx, p, current.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if row := referenceRow(t, after.Document); row["imageNodeId"] != nil {
		t.Fatalf("rejected bind still wrote the row: %+v", row)
	}
	if len(maps(after.Document["connections"])) != 0 {
		t.Fatal("rejected bind still wrote derived connections")
	}
}

// Two clients editing the same row from the same revision: the loser must fail rather
// than silently discard the committed reference (lost update).
func TestReferenceConcurrentStaleWriterKeepsDeclaredInputs(t *testing.T) {
	svc, _, _, snap := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	base := referenceCanvas(t, svc, snap, "setup-concurrent")

	attach := command(Operation{Type: "edit_storyboard", ID: "board", RowID: "row", Action: "update", Patch: map[string]any{"assetBindings": []any{map[string]any{"nodeId": "asset", "role": "style", "priority": float64(80)}}}})
	attach.CanvasID, attach.DocumentEpoch, attach.BaseRevision, attach.OperationID = base.CanvasID, base.DocumentEpoch, base.Revision, "attach-asset"
	if _, err := svc.Execute(ctx, p, attach); err != nil {
		t.Fatal(err)
	}

	stale := command(Operation{Type: "edit_storyboard", ID: "board", RowID: "row", Action: "update", Patch: map[string]any{"assetBindings": []any{}}})
	stale.CanvasID, stale.DocumentEpoch, stale.BaseRevision, stale.OperationID = base.CanvasID, base.DocumentEpoch, base.Revision, "stale-clear"
	if _, err := svc.Execute(ctx, p, stale); err == nil {
		t.Fatal("stale writer overwrote a committed reference edit")
	} else {
		assertReason(t, err, "revision_conflict")
	}

	after, err := svc.ReadSnapshot(ctx, p, base.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	bindings, _ := referenceRow(t, after.Document)["assetBindings"].([]any)
	if len(bindings) != 1 {
		t.Fatalf("stale writer changed declared inputs: %+v", referenceRow(t, after.Document))
	}
	edges := maps(after.Document["connections"])
	if len(edges) != 1 || edges[0]["managedBy"] != "reference-fields-v1" || edges[0]["fromNodeId"] != "asset" {
		t.Fatalf("stale writer changed derived connections: %+v", edges)
	}
}

// A deleted source cannot be reintroduced by a writer that still holds the revision
// from before the delete.
func TestReferenceStaleWriterCannotReviveDeletedSource(t *testing.T) {
	svc, _, _, snap := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	base := referenceCanvas(t, svc, snap, "setup-delete")

	remove := command(Operation{Type: "delete_node", ID: "asset"})
	remove.CanvasID, remove.DocumentEpoch, remove.BaseRevision, remove.OperationID = base.CanvasID, base.DocumentEpoch, base.Revision, "delete-asset"
	if _, err := svc.Execute(ctx, p, remove); err != nil {
		t.Fatal(err)
	}

	stale := command(Operation{Type: "edit_storyboard", ID: "board", RowID: "row", Action: "update", Patch: map[string]any{"assetBindings": []any{map[string]any{"nodeId": "asset", "role": "style", "priority": float64(80)}}}})
	stale.CanvasID, stale.DocumentEpoch, stale.BaseRevision, stale.OperationID = base.CanvasID, base.DocumentEpoch, base.Revision, "stale-reference"
	if _, err := svc.Execute(ctx, p, stale); err == nil {
		t.Fatal("stale writer referenced a deleted source")
	}

	after, err := svc.ReadSnapshot(ctx, p, base.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if entity(after.Document, "node:asset") != nil {
		t.Fatal("stale writer revived the deleted node")
	}
	if bindings, _ := referenceRow(t, after.Document)["assetBindings"].([]any); len(bindings) != 0 {
		t.Fatalf("stale writer left a dangling binding: %+v", bindings)
	}
	if len(maps(after.Document["connections"])) != 0 {
		t.Fatal("stale writer left a dangling connection")
	}
}
