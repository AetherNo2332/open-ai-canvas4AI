package commands

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestCommandEditLeaseProofIsEphemeralAndCommittedReplaySurvivesExpiry(t *testing.T) {
	svc, db, _, before := commandFixture(t)
	repo := repository.New(db)
	proof, err := repo.AcquireCanvasEditLease("owner", before.CanvasID, repository.CanvasEditProof{NodeID: "a", SessionID: "tab", Token: strings.Repeat("a", 48)})
	if err != nil {
		t.Fatal(err)
	}
	req := edit(before, "a", "confirmed", "edit-leased")
	if _, err = svc.Execute(context.Background(), ownerContext().Principal, req); !errors.Is(err, repository.ErrCanvasNodeLocked) {
		t.Fatalf("command bypass: %v", err)
	}
	ctx := repository.WithCanvasEditProofs(context.Background(), []repository.CanvasEditProof{proof.CanvasEditProof})
	saved, err := svc.Execute(ctx, ownerContext().Principal, req)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Model(&model.CanvasNodeEditLease{}).Where("canvas_id = ?", before.CanvasID).Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	replay, err := svc.Execute(ctx, ownerContext().Principal, req)
	if err != nil || !reflect.DeepEqual(saved, replay) {
		t.Fatalf("lost ACK expired replay: %#v %v", replay, err)
	}
	replacement, err := repo.AcquireCanvasEditLease("owner", before.CanvasID, repository.CanvasEditProof{NodeID: "a", SessionID: "new-tab", Token: strings.Repeat("b", 48)})
	if err != nil {
		t.Fatal(err)
	}
	newCtx := repository.WithCanvasEditProofs(context.Background(), []repository.CanvasEditProof{replacement.CanvasEditProof})
	replay, err = svc.Execute(newCtx, ownerContext().Principal, req)
	if err != nil || !reflect.DeepEqual(saved, replay) {
		t.Fatalf("renewed proof changed canonical identity: %v", err)
	}
	rows := []model.CanvasCommandReceipt{}
	if err = db.Where("operation_id = ?", req.OperationID).Find(&rows).Error; err != nil || len(rows) != 1 {
		t.Fatalf("duplicate receipt: %d %v", len(rows), err)
	}
	if strings.Contains(rows[0].CanonicalJSON, proof.Token) || strings.Contains(rows[0].ReceiptJSON, proof.Token) {
		t.Fatal("proof leaked into journal")
	}
	latest, err := svc.ReadSnapshot(context.Background(), ownerContext().Principal, before.CanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Execute(ctx, ownerContext().Principal, edit(latest, "a", "old proof draft", "uncommitted")); !errors.Is(err, repository.ErrCanvasEditProofInvalid) {
		t.Fatalf("old uncommitted proof accepted: %v", err)
	}
	if latest.Revision != saved.Revision {
		t.Fatal("replay wrote again")
	}
}

func TestCommandLeaseGuardsHostRestoreDeleteAndUndo(t *testing.T) {
	for _, action := range []string{"host-author", "host-progress", "restore", "delete", "undo"} {
		t.Run(action, func(t *testing.T) {
			svc, db, _, before := commandFixture(t)
			repo := repository.New(db)
			proof, err := repo.AcquireCanvasEditLease("owner", before.CanvasID, repository.CanvasEditProof{NodeID: "a", SessionID: "tab", Token: strings.Repeat("a", 48)})
			if err != nil {
				t.Fatal(err)
			}
			p := ownerContext().Principal
			ctx := repository.WithCanvasEditProofs(context.Background(), []repository.CanvasEditProof{proof.CanvasEditProof})
			run := func(runCtx context.Context) error {
				switch action {
				case "host-author", "host-progress":
					req := command()
					req.CanvasID, req.DocumentEpoch, req.BaseRevision, req.OperationID = before.CanvasID, before.DocumentEpoch, before.Revision, "host"
					_, _, err := svc.ExecuteHost(runCtx, p, req, "task_result", map[string]any{"task": "result"}, false, func(tx *repository.Repository, doc Document) (Document, map[string]bool, error) {
						node := maps(doc["nodes"])[0]
						meta, _ := node["metadata"].(map[string]any)
						if meta == nil {
							meta = map[string]any{}
							node["metadata"] = meta
						}
						if action == "host-author" {
							meta["content"] = "task must not bypass author"
						} else {
							meta["progress"] = 90
						}
						return doc, map[string]bool{"node:a": true}, nil
					})
					return err
				case "restore":
					doc, _ := cloneDocument(before.Document)
					maps(doc["nodes"])[0]["title"] = "restored"
					_, err := svc.RestoreVersion(runCtx, p, before.CanvasID, before.DocumentEpoch, "client", "restore", before.Revision, doc)
					return err
				case "delete":
					req := command(Operation{Type: "delete_node", ID: "a"})
					req.CanvasID, req.DocumentEpoch, req.BaseRevision, req.OperationID = before.CanvasID, before.DocumentEpoch, before.Revision, "delete"
					_, err := svc.Execute(runCtx, p, req)
					return err
				default:
					_, err := svc.Undo(runCtx, p, before.CanvasID, before.DocumentEpoch, "client", "undo")
					return err
				}
			}
			err = run(context.Background())
			if action == "host-progress" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, repository.ErrCanvasNodeLocked) {
				t.Fatalf("%s bypass: %v", action, err)
			}
			if err = run(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
