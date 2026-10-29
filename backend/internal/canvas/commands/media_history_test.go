package commands

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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
	var base mediaversions.Base
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
		if e = mediaversions.CheckAdmission(tx, scope, n, intent); e != nil {
			return nil, nil, e
		}
		base, e = mediaversions.Begin(n, model.Task{ID: "new-task", UserID: "owner", Status: model.TaskStatusQueued}, maps(doc["nodes"]))
		return doc, nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	req.BaseRevision, req.OperationID = snap.Revision, "version-finish"
	_, _, err = svc.ExecuteHost(ctx, p, req, "task_result", nil, true, func(tx *repository.Repository, doc Document) (Document, map[string]bool, error) {
		node := entity(doc, "node:a")
		result, _ := cloneDocument(Document(node))
		m := result["metadata"].(map[string]any)
		m["content"], m["storageKey"], m["taskStatus"], m["status"] = "/api/resources/new/file", "resource:new", "succeeded", "success"
		next, outcome := mediaversions.Finish(node, map[string]any(result), base, model.Task{ID: "new-task", UserID: "owner", Status: model.TaskStatusSucceeded}, "", time.Now())
		if !outcome.Adopted {
			return nil, nil, errors.New("result not adopted")
		}
		nodes, _ := doc["nodes"].([]any)
		for i, value := range nodes {
			if node, ok := value.(map[string]any); ok && text(node["id"]) == "a" {
				nodes[i] = next
			}
		}
		if versions := mediaversions.List(next); len(versions) > 1 {
			oldVersion = text(versions[0]["id"])
		}
		return doc, nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	return svc, repo, snap, oldVersion
}

func currentVersion(node map[string]any) string {
	return text(mediaversions.Current(node)["id"])
}

func TestManagedVersionSelectionUndoRedoPreservesReceiptsAndEarlierEdits(t *testing.T) {
	svc, _, snap, old := managedHistoryFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	current := currentVersion(entity(snap.Document, "node:a"))
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
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if currentVersion(entity(snap.Document, "node:a")) != old || entity(snap.Document, "node:a")["metadata"].(map[string]any)["storageKey"] != "resource:old" {
		t.Fatal("use_media_version did not adopt the old media")
	}
	for _, operation := range []string{"undo-selection", "undo-author"} {
		if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", operation); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	n := entity(snap.Document, "node:a")
	m := n["metadata"].(map[string]any)
	if n["title"] == "later title" || m["composerContent"] != "draft" || currentVersion(n) != current || len(mediaversions.List(n)) != 2 {
		t.Fatalf("undo lost selection or draft: %+v", m)
	}
	for _, operation := range []string{"redo-author", "redo-selection"} {
		if _, err := svc.Redo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", operation); err != nil {
			t.Fatal(err)
		}
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	if currentVersion(entity(snap.Document, "node:a")) != old {
		t.Fatal("redo did not select history")
	}
}

func TestManagedTaskResultUndoDoesNotResurrectPending(t *testing.T) {
	svc, _, snap, _ := managedHistoryFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	if _, err := svc.Undo(ctx, p, snap.CanvasID, snap.DocumentEpoch, "client", "undo-result"); err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	m := entity(snap.Document, "node:a")["metadata"].(map[string]any)
	if m["status"] == "loading" || m["taskStatus"] != "succeeded" {
		t.Fatalf("task result undo resurrected pending: %+v", m)
	}
}

func TestManagedDeleteUndoAndDocumentRestoreKeepVersionHistory(t *testing.T) {
	for _, action := range []string{"delete-undo", "restore"} {
		t.Run(action, func(t *testing.T) {
			svc, _, snap, _ := managedHistoryFixture(t)
			ctx, p := context.Background(), ownerContext().Principal
			versions := mediaversions.List(entity(snap.Document, "node:a"))
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
			if len(mediaversions.List(n)) != len(versions) || currentVersion(n) == "" || n["metadata"].(map[string]any)["status"] == "loading" {
				t.Fatalf("restored node lost history: %+v", n["metadata"])
			}
		})
	}
}

func TestLegacyPutCannotRewriteExistingVersionRecord(t *testing.T) {
	_, repo, snap, old := managedHistoryFixture(t)
	for _, version := range mediaversions.List(entity(snap.Document, "node:a")) {
		if text(version["id"]) == old {
			version["media"].(map[string]any)["storageKey"] = "resource:new"
		}
	}
	raw, _ := json.Marshal(snap.Document)
	row, err := repo.CanvasProjectForUser("owner", snap.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	row.PayloadJSON = string(raw)
	if err = repo.UpsertCanvasProject(row); !errors.Is(err, repository.ErrCanvasMediaVersionManaged) {
		t.Fatalf("version record rewrite accepted: %v", err)
	}
}

func TestAgentGenericEditCannotTouchVersionHistory(t *testing.T) {
	svc, _, snap, _ := managedHistoryFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	req := edit(snap, "a", "", "bind-over-history")
	req.Operations = []Operation{{Type: "bind_resource", ID: "a", ResourceID: "old"}}
	if _, err := svc.Execute(ctx, p, req); err == nil {
		t.Fatal("generic edit replaced versioned media")
	}
}

func TestExtractMediaVersionCreatesStandaloneNode(t *testing.T) {
	svc, _, snap, old := managedHistoryFixture(t)
	ctx, p := context.Background(), ownerContext().Principal
	req := edit(snap, "a", "", "extract")
	req.Operations = []Operation{{Type: "extract_media_version", ID: "a", VersionID: old, SourceNodeID: "a-v1"}}
	if _, err := svc.Execute(ctx, p, req); err != nil {
		t.Fatal(err)
	}
	snap, _ = svc.ReadSnapshot(ctx, p, snap.CanvasID)
	copyNode := entity(snap.Document, "node:a-v1")
	if copyNode == nil {
		t.Fatal("extract did not create a node")
	}
	m, _ := copyNode["metadata"].(map[string]any)
	if m["storageKey"] != "resource:old" || m["mediaVersions"] != nil || m["taskId"] != nil || m["copiedFromNodeId"] != "a" {
		t.Fatalf("extract: %+v", copyNode)
	}
}

func TestFirstManagedGenerationSupportsOriginalAddUndoRedo(t *testing.T) {
	svc, _, snap, _ := managedHistoryFixtureWithInitial(t, true)
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
	m := node["metadata"].(map[string]any)
	if m["status"] == "loading" || m["storageKey"] != "resource:new" || len(mediaversions.List(node)) != 1 {
		t.Fatalf("first lifecycle: %+v", node)
	}
}
