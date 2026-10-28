package commands

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"yingce/backend/internal/canvas/mediaversions"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func managedHistoryFixture(t *testing.T) (*Service, *repository.Repository, Snapshot, string) {
	return managedHistoryFixtureWithInitial(t, false)
}
func managedHistoryFixtureWithInitial(t *testing.T, first bool) (*Service, *repository.Repository, Snapshot, string) {
	t.Helper()
	svc, db, _, snap := commandFixture(t)
	if first {
		var err error
		snap, err = svc.Create(context.Background(), ownerContext().Principal, CreateCanvasRequest{ClientID: "client", OperationID: "create-first-media"})
		if err != nil {
			t.Fatal(err)
		}
		add := command(Operation{Type: "add_node", ID: "a", NodeType: "image", X: num(0), Y: num(0)})
		add.CanvasID, add.DocumentEpoch, add.BaseRevision, add.OperationID = snap.CanvasID, snap.DocumentEpoch, snap.Revision, "add-first-media"
		if _, err = svc.Execute(context.Background(), ownerContext().Principal, add); err != nil {
			t.Fatal(err)
		}
		snap, err = svc.ReadSnapshot(context.Background(), ownerContext().Principal, snap.CanvasID)
		if err != nil {
			t.Fatal(err)
		}
	}
	repo := repository.New(db)
	for _, id := range []string{"old", "new"} {
		if err := db.Create(&model.Resource{ID: id, UserID: "owner", MimeType: "image/png", Status: model.ResourceStatusReady}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.Task{ID: "old-task", UserID: "owner", Status: model.TaskStatusSucceeded}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, p := context.Background(), ownerContext().Principal
	req := command()
	req.CanvasID, req.DocumentEpoch, req.BaseRevision, req.OperationID = snap.CanvasID, snap.DocumentEpoch, snap.Revision, "version-pending"
	var oldVersion string
	_, _, err := svc.ExecuteHost(ctx, p, req, "task_submit", nil, false, func(tx *repository.Repository, doc Document) (Document, map[string]bool, error) {
		n := entity(doc, "node:a")
		if !first {
			n["type"] = "image"
			n["metadata"] = map[string]any{"content": "/api/resources/old/file", "storageKey": "resource:old", "status": "success", "taskId": "old-task", "composerContent": "draft"}
		}
		scope, e := mediaversions.ScopeForNode(tx, "owner", snap.CanvasID, "a")
		if e != nil {
			return nil, nil, e
		}
		intent := "new_version"
		if first {
			intent = "create"
		}
		_, e = mediaversions.Begin(tx, scope, n, model.Task{ID: "new-task", UserID: "owner", Status: model.TaskStatusQueued}, intent)
		oldVersion = mediaversions.ReadState(n).CurrentVersionID
		return doc, nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	req.BaseRevision, req.OperationID = snap.Revision, "version-finish"
	_, _, err = svc.ExecuteHost(ctx, p, req, "task_result", nil, true, func(tx *repository.Repository, doc Document) (Document, map[string]bool, error) {
		scope, e := mediaversions.ScopeForNode(tx, "owner", snap.CanvasID, "a")
		if e != nil {
			return nil, nil, e
		}
		_, _, e = mediaversions.Finish(tx, scope, entity(doc, "node:a"), model.Task{ID: "new-task", UserID: "owner", Status: model.TaskStatusSucceeded}, map[string]any{"content": "/api/resources/new/file", "storageKey": "resource:new"}, nil)
		return doc, nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	return svc, repo, snap, oldVersion
}

func TestManagedVersionSelectionUndoRedoPreservesReceiptsAndEarlierEdits(t *testing.T) {
	svc, _, snap, old := managedHistoryFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	current := mediaversions.ReadState(entity(snap.Document, "node:a")).CurrentVersionID
	req := edit(snap, "a", "", "author")
	req.Operations = []Operation{{Type: "update_node", ID: "a", Patch: map[string]any{"title": "later title"}}}
	if _, err := svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	req = edit(snap, "a", "", "choose-old")
	req.Operations = []Operation{{Type: "use_media_version", ID: "a", VersionID: old}}
	if _, err := svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"undo-selection", "undo-author"} {
		if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", operation); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	n := entity(snap.Document, "node:a")
	m := n["metadata"].(map[string]any)
	if n["title"] == "later title" || m["composerContent"] != "draft" || mediaversions.ReadState(n).CurrentVersionID != current || mediaversions.ReadState(n).SelectionRevision < 3 {
		t.Fatalf("undo lost projection or draft: %+v", m)
	}
	for _, operation := range []string{"redo-author", "redo-selection"} {
		if _, err := svc.Redo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", operation); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if mediaversions.ReadState(entity(snap.Document, "node:a")).CurrentVersionID != old {
		t.Fatal("redo did not select history")
	}
}

func TestManagedTaskResultUndoDoesNotResurrectPending(t *testing.T) {
	svc, repo, snap, old := managedHistoryFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", "undo-result"); err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	state := mediaversions.ReadState(entity(snap.Document, "node:a"))
	if state.ActiveTaskID != "" || state.PendingVersionID != "" || state.CurrentVersionID != old {
		t.Fatalf("task result undo resurrected pending: %+v", state)
	}
	version, err := repo.CanvasMediaVersionForTask("owner", "new-task")
	if err != nil || version == nil || version.Status != "ready" {
		t.Fatal("undo erased real result", err)
	}
}

func TestManagedDeleteUndoAndDocumentRestoreRebaseAvailableMedia(t *testing.T) {
	for _, action := range []string{"delete-undo", "restore"} {
		t.Run(action, func(t *testing.T) {
			svc, repo, snap, _ := managedHistoryFixture(t)
			ctx, p := context.Background(), ownerContext().Principal
			oldState := mediaversions.ReadState(entity(snap.Document, "node:a"))
			if action == "restore" {
				var err error
				snap, err = svc.RestoreVersion(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", "restore-media", snap.Revision, snap.Document)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				req := edit(snap, "a", "", "delete-media")
				req.Operations = []Operation{{Type: "delete_node", ID: "a"}}
				if _, err := svc.Execute(ctx, p, req); err != nil {
					t.Fatal(err)
				}
				if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", "restore-deleted"); err != nil {
					t.Fatal(err)
				}
				snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
			}
			n := entity(snap.Document, "node:a")
			state := mediaversions.ReadState(n)
			if state.CurrentVersionID == "" || state.CurrentVersionID == oldState.CurrentVersionID || state.ActiveTaskID != "" {
				t.Fatalf("not rebased: %+v", state)
			}
			scope, err := mediaversions.ScopeForNode(repo, "owner", snap.CanvasID, "a")
			if err != nil {
				t.Fatal(err)
			}
			version, err := repo.CanvasMediaVersion(scope, state.CurrentVersionID)
			if err != nil || version.SourceVersionID != oldState.CurrentVersionID {
				t.Fatalf("source not retained: %+v %v", version, err)
			}
		})
	}
}

func TestLegacyPutCannotInjectManagedIdentityIntoExistingNode(t *testing.T) {
	svc, db, _, snap := commandFixture(t)
	_ = svc
	repo := repository.New(db)
	entity(snap.Document, "node:a")["metadata"].(map[string]any)["mediaVersion"] = map[string]any{"epoch": snap.DocumentEpoch, "generation": snap.EntityGenerations["node:a"], "pendingVersionId": "fake"}
	raw, _ := json.Marshal(snap.Document)
	row, err := repo.CanvasProjectForUser("owner", snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	row.PayloadJSON = string(raw)
	if err = repo.UpsertCanvasProject(row); !errors.Is(err, repository.ErrCanvasMediaVersionManaged) {
		t.Fatalf("untrusted managed identity accepted: %v", err)
	}
}

func TestManagedHistoryTraversesSelectionsAcrossRestoredNodeGenerations(t *testing.T) {
	svc, repo, snap, old := managedHistoryFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	newVersion := mediaversions.ReadState(entity(snap.Document, "node:a")).CurrentVersionID
	req := edit(snap, "a", "", "choose-before-delete")
	req.Operations = []Operation{{Type: "use_media_version", ID: "a", VersionID: old}}
	if _, err := svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	req = edit(snap, "a", "", "delete-chosen")
	req.Operations = []Operation{{Type: "delete_node", ID: "a"}}
	if _, err := svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"undo-deletion", "undo-choice", "undo-task-result"} {
		if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", op); err != nil {
			t.Fatal(op, err)
		}
	}
	for _, op := range []string{"redo-task-result", "redo-choice"} {
		if _, err := svc.Redo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", op); err != nil {
			t.Fatal(op, err)
		}
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	scope, err := mediaversions.ScopeForNode(repo, "owner", snap.CanvasID, "a")
	if err != nil {
		t.Fatal(err)
	}
	versions, err := repo.CanvasMediaVersions(scope, 0, 20)
	if err != nil || len(versions) != 2 {
		t.Fatalf("repeated history duplicated restored versions: %+v %v", versions, err)
	}
	if versions[0].SourceVersionID != newVersion || versions[1].SourceVersionID != old {
		t.Fatalf("cross-generation source mapping: %+v", versions)
	}
}

func TestFirstManagedGenerationSupportsOriginalAddUndoRedo(t *testing.T) {
	svc, repo, snap, _ := managedHistoryFixtureWithInitial(t, true)
	ctx, p := context.Background(), ownerContext().Principal
	for _, op := range []string{"undo-first-result", "undo-original-add"} {
		if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", op); err != nil {
			t.Fatal(op, err)
		}
	}
	for _, op := range []string{"redo-original-add", "redo-first-result"} {
		if _, err := svc.Redo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", op); err != nil {
			t.Fatal(op, err)
		}
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	node := entity(snap.Document, "node:a")
	scope, err := mediaversions.ScopeForNode(repo, "owner", snap.CanvasID, "a")
	if err != nil {
		t.Fatal(err)
	}
	state := mediaversions.ReadState(node)
	if state.ActiveTaskID != "" || state.Generation != scope.Generation || node["metadata"].(map[string]any)["storageKey"] != "resource:new" {
		t.Fatalf("first lifecycle: %+v", node)
	}
}
