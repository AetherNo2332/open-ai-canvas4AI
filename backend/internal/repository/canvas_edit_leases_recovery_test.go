package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestCanvasLeasePhysicalDeletionCannotRecreateSameAuthorWithoutProof(t *testing.T) {
	for _, entry := range []string{"legacy", "snapshot", "command"} {
		t.Run(entry, func(t *testing.T) {
			repo, _ := leaseFixture(t, false)
			grant, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)})
			if err != nil {
				t.Fatal(err)
			}
			original, _ := repo.CanvasProjectForUser("owner", "canvas")
			ctx := WithCanvasEditProofs(context.Background(), []CanvasEditProof{grant.CanvasEditProof})
			if err = repo.WithContext(ctx).DeleteCanvasProject("owner", "canvas"); err != nil {
				t.Fatal(err)
			}
			save := func(writeCtx context.Context, id string) error {
				next := *original
				next.ID = id
				next.Revision = 0
				return repo.CommandTransaction(writeCtx, func(tx *Repository) error {
					switch entry {
					case "snapshot":
						return tx.SaveCanvasWithSnapshot(&next, nil, nil, nil, time.Now(), 20, true)
					case "command":
						next.Revision = 1
						return tx.CreateCommandCanvas(&next)
					default:
						return tx.UpsertCanvasProject(&next)
					}
				})
			}
			if err = save(context.Background(), "canvas"); !errors.Is(err, ErrCanvasNodeLocked) {
				t.Fatalf("recreate without proof: %v", err)
			}
			if _, err = repo.CanvasProjectForUser("owner", "canvas"); !repo.IsMissing(err) {
				t.Fatal("failed recreate persisted canvas")
			}
			wrong := grant.CanvasEditProof
			wrong.Token = strings.Repeat("b", 48)
			if err = save(WithCanvasEditProofs(context.Background(), []CanvasEditProof{wrong}), "canvas"); !errors.Is(err, ErrCanvasEditProofInvalid) {
				t.Fatalf("recreate wrong proof: %v", err)
			}
			if err = save(context.Background(), "brand-new"); err != nil {
				t.Fatalf("fresh canvas blocked: %v", err)
			}
			if err = save(ctx, "canvas"); err != nil {
				t.Fatalf("proved recreate: %v", err)
			}
		})
	}
}

func TestSQLiteCanvasLeaseStatusAndGuardAgreeAcrossTimezones(t *testing.T) {
	originalLocal := time.Local
	t.Cleanup(func() { time.Local = originalLocal })
	utc8 := time.FixedZone("UTC+8", 8*60*60)
	for _, writerZone := range []*time.Location{time.UTC, utc8} {
		t.Run(writerZone.String(), func(t *testing.T) {
			repo, _ := leaseFixture(t, false)
			time.Local = writerZone
			proof, err := repo.AcquireCanvasEditLease("owner", "canvas", CanvasEditProof{NodeID: "text", SessionID: "tab", Token: strings.Repeat("a", 48)})
			if err != nil {
				t.Fatal(err)
			}
			if writerZone == time.UTC {
				time.Local = utc8
			} else {
				time.Local = time.UTC
			}
			leases, err := repo.ActiveCanvasEditLeases("owner", "canvas")
			if err != nil || len(leases) != 1 {
				t.Fatalf("active lease hidden cross-zone: %d %v", len(leases), err)
			}
			row, _ := repo.CanvasProjectForUser("owner", "canvas")
			row.PayloadJSON = strings.Replace(row.PayloadJSON, "draft", "changed", 1)
			if err = repo.UpsertCanvasProject(row); !errors.Is(err, ErrCanvasNodeLocked) {
				t.Fatalf("active guard mismatch: %v", err)
			}
			// Existing short-lived rows may have been written with a non-UTC offset.
			if err = repo.db.Model(&model.CanvasNodeEditLease{}).Where("canvas_id = ?", "canvas").Update("expires_at", time.Now().Add(time.Minute).In(writerZone)).Error; err != nil {
				t.Fatal(err)
			}
			leases, err = repo.ActiveCanvasEditLeases("owner", "canvas")
			if err != nil || len(leases) != 1 {
				t.Fatalf("legacy active offset hidden: %d %v", len(leases), err)
			}
			renewed, err := repo.RenewCanvasEditLease("owner", "canvas", proof.CanvasEditProof)
			if err != nil {
				t.Fatal(err)
			}
			if _, offset := renewed.ExpiresAt.Zone(); offset != 0 {
				t.Fatal("renew must persist UTC expiry")
			}
			if err = repo.db.Model(&model.CanvasNodeEditLease{}).Where("canvas_id = ?", "canvas").Update("expires_at", time.Now().Add(-time.Minute).In(writerZone)).Error; err != nil {
				t.Fatal(err)
			}
			leases, err = repo.ActiveCanvasEditLeases("owner", "canvas")
			if err != nil || len(leases) != 0 {
				t.Fatalf("expired offset still visible: %d %v", len(leases), err)
			}
			if _, err = repo.RenewCanvasEditLease("owner", "canvas", proof.CanvasEditProof); !errors.Is(err, ErrCanvasEditProofInvalid) {
				t.Fatalf("expired renew: %v", err)
			}
			if err = repo.UpsertCanvasProject(row); err != nil {
				t.Fatalf("expired unproved writer still locked: %v", err)
			}
		})
	}
}
