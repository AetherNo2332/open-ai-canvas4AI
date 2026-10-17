package commands

import (
	"context"
	"testing"
)

func TestRestoreRejectsUnseenEditsAndDeduplicatesAcrossEpoch(t *testing.T) {
	svc, _, _, snapshot := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	receipt, err := svc.Execute(ctx, p, edit(snapshot, "a", "newer", "newer"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.RestoreVersion(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client", "restore-stale", snapshot.Revision, snapshot.Document)
	assertReason(t, err, "revision_conflict")
	restored, err := svc.RestoreVersion(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client", "restore-fresh", receipt.Revision, snapshot.Document)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := svc.RestoreVersion(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client", "restore-fresh", receipt.Revision, snapshot.Document)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DocumentEpoch != replay.DocumentEpoch || restored.EventSeq != replay.EventSeq {
		t.Fatal("restore replay changed epoch")
	}
}

func TestHistoryMultipleUndoRedo(t *testing.T) {
	svc, _, _, snapshot := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	for i, value := range []string{"first", "second"} {
		current, _ := svc.ReadSnapshot(ctx, p, snapshot.CanvasID)
		req := edit(current, "a", value, []string{"edit1", "edit2"}[i])
		if _, err := svc.Execute(ctx, p, req); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range []string{"first", ""} {
		if _, err := svc.Undo(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client", []string{"undo1", "undo2"}[i]); err != nil {
			t.Fatal(err)
		}
		current, _ := svc.ReadSnapshot(ctx, p, snapshot.CanvasID)
		if text(entity(current.Document, "node:a")["metadata"].(map[string]any)["content"]) != want {
			t.Fatal("undo order incorrect")
		}
	}
	for i, want := range []string{"first", "second"} {
		if _, err := svc.Redo(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client", []string{"redo1", "redo2"}[i]); err != nil {
			t.Fatal(err)
		}
		current, _ := svc.ReadSnapshot(ctx, p, snapshot.CanvasID)
		if text(entity(current.Document, "node:a")["metadata"].(map[string]any)["content"]) != want {
			t.Fatal("redo order incorrect")
		}
	}
}
func TestHistoryStateIsBoundToActorClientAndEpoch(t *testing.T) {
	svc, _, _, snapshot := commandFixture(t)
	ctx := context.Background()
	p := ownerContext().Principal
	state, err := svc.ReadHistoryState(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client")
	if err != nil {
		t.Fatal(err)
	}
	if !state.CanUndo || state.CanRedo {
		t.Fatal("initial history state incorrect")
	}
	other, err := svc.ReadHistoryState(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "other")
	if err != nil {
		t.Fatal(err)
	}
	if other.CanUndo || other.CanRedo {
		t.Fatal("another client's history leaked")
	}
	if _, err := svc.Undo(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client", "undo-initial"); err != nil {
		t.Fatal(err)
	}
	state, err = svc.ReadHistoryState(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "client")
	if err != nil {
		t.Fatal(err)
	}
	if state.CanUndo || !state.CanRedo {
		t.Fatal("undo did not advance history state")
	}
	p.ClientID = "client"
	if _, err := svc.ReadHistoryState(ctx, p, snapshot.CanvasID, snapshot.DocumentEpoch, "other"); err == nil {
		t.Fatal("bound client accepted another history")
	}
	if _, err := svc.ReadHistoryState(ctx, p, snapshot.CanvasID, "old-epoch", "client"); err == nil {
		t.Fatal("old epoch accepted")
	}
}
