package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/model"
)

func leaseFixture(t *testing.T, pg bool) (*Repository, string) {
	t.Helper()
	repo, db, path := canvasLifecycleFixture(t, pg)
	if err := db.AutoMigrate(&model.CanvasNodeEditLease{}); err != nil {
		t.Fatal(err)
	}
	row := &model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: `{"nodes":[{"id":"text","type":"text","title":"Title","metadata":{"content":"draft","richText":{"text":"draft"},"prompt":"prompt"}},{"id":"image","type":"image","metadata":{"content":"output.png","prompt":"image prompt"}}],"connections":[]}`}
	if err := repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	return repo, path
}

func TestCanvasEditLeaseLifecycle(t *testing.T) {
	repo, _ := leaseFixture(t, false)
	first, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab-a", Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab-a", Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil || first.Token != again.Token {
		t.Fatalf("same-session retry: %#v %v", again, err)
	}
	if _, err = repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab-a", Token: strings.Repeat("x", 48)}); !errors.Is(err, ErrCanvasEditProofInvalid) {
		t.Fatalf("public session id recovered token: %v", err)
	}
	if _, err = repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab-b", Token: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}); !errors.Is(err, ErrCanvasNodeLocked) {
		t.Fatalf("other tab acquired: %v", err)
	}
	wrong := CanvasEditProof{NodeID: "text", SessionID: "tab-a", Token: "wrongwrongwrongwrongwrongwrongxx"}
	if _, err = repo.RenewCanvasEditLease("owner", "canvas", wrong); !errors.Is(err, ErrCanvasEditProofInvalid) {
		t.Fatalf("wrong token renewed: %v", err)
	}
	if err = repo.ReleaseCanvasEditLease("owner", "canvas", wrong); !errors.Is(err, ErrCanvasEditProofInvalid) {
		t.Fatalf("wrong token released: %v", err)
	}
	if _, err = repo.RenewCanvasEditLease("owner", "canvas", first.CanvasEditProof); err != nil {
		t.Fatal(err)
	}
	if err = repo.ReleaseCanvasEditLease("owner", "canvas", first.CanvasEditProof); err != nil {
		t.Fatal(err)
	}
	next, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab-b", Token: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
	if err != nil || first.Token == next.Token {
		t.Fatalf("takeover: %v", err)
	}
	if err = repo.db.Model(&model.CanvasNodeEditLease{}).Where("canvas_id = ?", "canvas").Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = repo.RenewCanvasEditLease("owner", "canvas", next.CanvasEditProof); !errors.Is(err, ErrCanvasEditProofInvalid) {
		t.Fatalf("expired proof renewed: %v", err)
	}
	if _, err = repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab-c", Token: "cccccccccccccccccccccccccccccccc"}); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.AcquireCanvasEditLease("other", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab", Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err == nil {
		t.Fatal("wrong owner acquired")
	}
	if _, err = repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "missing", SessionID: "tab", Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err == nil {
		t.Fatal("missing node acquired")
	}
}

func TestCanvasLeaseRestoreOfRemovedNodeAndCrossCanvasProof(t *testing.T) {
	repo, _ := leaseFixture(t, false)
	proof, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)})
	if err != nil {
		t.Fatal(err)
	}
	original, _ := repo.CanvasProjectForUser("owner", "canvas")
	row := *original
	row.PayloadJSON = `{"nodes":[],"connections":[]}`
	ctx := WithCanvasEditProofs(context.Background(), []CanvasEditProof{proof.CanvasEditProof})
	if err = repo.WithContext(ctx).UpsertCanvasProject(&row); err != nil {
		t.Fatal(err)
	}
	row.PayloadJSON = original.PayloadJSON
	if err = repo.UpsertCanvasProject(&row); !errors.Is(err, ErrCanvasNodeLocked) {
		t.Fatalf("restore re-added leased node without proof: %v", err)
	}
	other := *original
	other.ID = "other-canvas"
	other.Revision = 0
	if err = repo.UpsertCanvasProject(&other); err != nil {
		t.Fatal(err)
	}
	otherProof, err := repo.AcquireCanvasEditLease("owner", other.ID, CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("b", 48)})
	if err != nil {
		t.Fatal(err)
	}
	wrongCtx := WithCanvasEditProofs(context.Background(), []CanvasEditProof{otherProof.CanvasEditProof})
	if err = repo.WithContext(wrongCtx).UpsertCanvasProject(&row); !errors.Is(err, ErrCanvasNodeLocked) {
		t.Fatalf("cross canvas proof unlocked: %v", err)
	}
	if err = repo.WithContext(ctx).UpsertCanvasProject(&row); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCanvasLeaseAcquireSerializesAndRollbackReleasesClaim(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "rollback"}[rollback], func(t *testing.T) {
			repo, path := leaseFixture(t, true)
			other, err := gorm.Open(postgres.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			conn, _ := other.DB()
			conn.SetMaxOpenConns(1)
			t.Cleanup(func() { _ = conn.Close() })
			var pid int
			if err = other.Raw("SELECT pg_backend_pid()").Scan(&pid).Error; err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			claimed, release := make(chan struct{}), make(chan struct{})
			doneA, doneB := make(chan error, 1), make(chan error, 1)
			rolledBack := errors.New("rollback test transaction")
			go func() {
				doneA <- repo.CommandTransaction(ctx, func(tx *Repository) error {
					if _, err := tx.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "a", Token: strings.Repeat("a", 48)}); err != nil {
						return err
					}
					close(claimed)
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					if rollback {
						return rolledBack
					}
					return nil
				})
			}()
			select {
			case <-claimed:
			case err := <-doneA:
				t.Fatalf("acquire did not reach lock: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			go func() {
				_, err := New(other).WithContext(ctx).AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "b", Token: strings.Repeat("b", 48)})
				doneB <- err
			}()
			blocked := false
			for !blocked && ctx.Err() == nil {
				if err = repo.db.WithContext(ctx).Raw("SELECT cardinality(pg_blocking_pids(?)) > 0", pid).Scan(&blocked).Error; err != nil {
					break
				}
				if !blocked {
					select {
					case <-time.After(10 * time.Millisecond):
					case <-ctx.Done():
					}
				}
			}
			close(release)
			firstErr, secondErr := <-doneA, <-doneB
			if err != nil || !blocked {
				t.Fatalf("no database serialization evidence: %v %v", blocked, err)
			}
			if rollback {
				if !errors.Is(firstErr, rolledBack) || secondErr != nil {
					t.Fatalf("rollback blocked next owner: %v %v", firstErr, secondErr)
				}
			} else if firstErr != nil || !errors.Is(secondErr, ErrCanvasNodeLocked) {
				t.Fatalf("double-acquire: %v %v", firstErr, secondErr)
			}
		})
	}
}

func TestCanvasAuthorLeaseGuardProtectsNestedAuthorFields(t *testing.T) {
	for _, kind := range []string{"title", "content", "richText", "prompt", "composerContent", "generationSpec", "delete", "delete-canvas", "restore"} {
		t.Run(kind, func(t *testing.T) {
			repo, _ := leaseFixture(t, false)
			proof, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab", Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
			if err != nil {
				t.Fatal(err)
			}
			row, _ := repo.CanvasProjectForUser("owner", "canvas")
			var doc map[string]any
			_ = json.Unmarshal([]byte(row.PayloadJSON), &doc)
			node := doc["nodes"].([]any)[0].(map[string]any)
			metadata := node["metadata"].(map[string]any)
			switch kind {
			case "title":
				node["title"] = "changed"
			case "delete":
				doc["nodes"] = doc["nodes"].([]any)[1:]
			case "delete-canvas":
				doc["_deleted"] = true
			case "restore":
				metadata["content"] = "restored"
			case "generationSpec":
				metadata[kind] = map[string]any{"prompt": "changed"}
			default:
				metadata[kind] = "changed"
			}
			raw, _ := json.Marshal(doc)
			row.PayloadJSON = string(raw)
			save := func(writer *Repository) error {
				if kind == "delete-canvas" {
					return writer.DeleteCanvasProject("owner", "canvas")
				}
				if kind == "restore" {
					return writer.SaveCanvasWithSnapshot(row, nil, nil, nil, time.Now(), 10, true)
				}
				return writer.UpsertCanvasProject(row)
			}
			if err = save(repo); !errors.Is(err, ErrCanvasNodeLocked) {
				t.Fatalf("unproved author write: %v", err)
			}
			writer := repo.WithContext(WithCanvasEditProofs(context.Background(), []CanvasEditProof{proof.CanvasEditProof}))
			if err = save(writer); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCanvasAuthorLeaseAllowsTaskProgressAndMediaOutput(t *testing.T) {
	repo, _ := leaseFixture(t, false)
	for _, id := range []string{"text", "image"} {
		if _, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: id, SessionID: "tab", Token: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
			t.Fatal(err)
		}
	}
	row, _ := repo.CanvasProjectForUser("owner", "canvas")
	var doc map[string]any
	_ = json.Unmarshal([]byte(row.PayloadJSON), &doc)
	nodes := doc["nodes"].([]any)
	nodes[0].(map[string]any)["metadata"].(map[string]any)["progress"] = 90
	nodes[1].(map[string]any)["metadata"].(map[string]any)["content"] = "new-output.png"
	raw, _ := json.Marshal(doc)
	row.PayloadJSON = string(raw)
	if err := repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
}

func TestCanvasEditProofSurvivesNewTransactionRepositoryAndMissingTableFailsClosed(t *testing.T) {
	repo, _ := leaseFixture(t, false)
	proof, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)})
	if err != nil {
		t.Fatal(err)
	}
	row, _ := repo.CanvasProjectForUser("owner", "canvas")
	row.PayloadJSON = strings.Replace(row.PayloadJSON, "draft", "changed", 1)
	ctx := WithCanvasEditProofs(context.Background(), []CanvasEditProof{proof.CanvasEditProof})
	if err = repo.CommandTransaction(ctx, func(tx *Repository) error { return New(tx.db).UpsertCanvasProject(row) }); err != nil {
		t.Fatalf("New(tx) lost proof: %v", err)
	}
	prior, _ := repo.CanvasProjectForUser("owner", "canvas")
	if err = repo.db.Migrator().DropTable(&model.CanvasNodeEditLease{}); err != nil {
		t.Fatal(err)
	}
	row.PayloadJSON = strings.Replace(row.PayloadJSON, "changed", "must not save", 1)
	if err = repo.UpsertCanvasProject(row); err == nil {
		t.Fatal("missing lease table silently bypassed protection")
	}
	after, _ := repo.CanvasProjectForUser("owner", "canvas")
	if after.Revision != prior.Revision || after.PayloadJSON != prior.PayloadJSON {
		t.Fatal("missing lease schema partially wrote document")
	}
}
