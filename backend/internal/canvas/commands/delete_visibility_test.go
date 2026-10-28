package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	canvasdomain "yingce/backend/internal/canvas"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"

	"gorm.io/gorm"
)

func TestCommandDeleteHidesLiveReadsAndRestoreMakesCanvasVisible(t *testing.T) {
	svc, db, _, before := parityCommandFixture(t)
	if err := db.AutoMigrate(&model.CloudAgentExecution{}); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	domain := canvasdomain.New(repo, nil)
	ctx, principal := context.Background(), ownerContext().Principal
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", before.CanvasID).Update("project_id", "project").Error; err != nil {
		t.Fatal(err)
	}
	legacy := model.CanvasProject{ID: "legacy-without-epoch", UserID: "owner", Title: "Legacy visible", Revision: 1, PayloadJSON: `{"nodes":[],"connections":[]}`, UpdatedAt: time.Now().Add(-time.Hour)}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	foreign := legacy
	foreign.ID, foreign.UserID = "foreign", "other-owner"
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	page, err := domain.UserCanvasProjectsPage("owner", 1, 1, "all", "", "updated")
	if err != nil || page.Total != 2 || !page.HasMore {
		t.Fatalf("initial live page: %+v %v", page, err)
	}
	proof, err := repo.AcquireCanvasEditLease("owner", before.CanvasID, repository.CanvasEditProof{NodeID: "a", SessionID: "delete-tab", Token: strings.Repeat("a", 48)})
	if err != nil {
		t.Fatal(err)
	}
	deleteCtx := repository.WithCanvasEditProofs(ctx, []repository.CanvasEditProof{proof.CanvasEditProof})
	req := edit(before, before.CanvasID, "", "delete-library-visibility")
	req.Operations = []Operation{{Type: "delete_canvas", ID: before.CanvasID}}
	deleted, err := svc.Execute(deleteCtx, principal, req)
	if err != nil {
		t.Fatal(err)
	}
	assertNotFound := func(err error) {
		t.Helper()
		var app *kernel.AppError
		if !errors.As(err, &app) || app.Status != 404 {
			t.Errorf("live entry should be 404: %v", err)
		}
	}
	page, err = domain.UserCanvasProjectsPage("owner", 1, 1, "all", "", "nodes")
	if err != nil || page.Total != 1 || page.HasMore || len(page.Projects) != 1 || page.Projects[0].ID != legacy.ID {
		t.Errorf("deleted canvas in live page/count: %+v %v", page, err)
	}
	last, err := domain.UserCanvasProjectsPage("owner", 2, 1, "all", "", "name")
	if err != nil || last.Total != 1 || last.HasMore || len(last.Projects) != 0 {
		t.Errorf("last live page inconsistent: %+v %v", last, err)
	}
	searched, err := domain.UserCanvasProjectsPage("owner", 1, 10, "project", before.Document["title"].(string), "updated")
	if err != nil || searched.Total != 0 || len(searched.Projects) != 0 {
		t.Errorf("project/search live filter: %+v %v", searched, err)
	}
	aggregate, err := repo.CanvasProjects("owner")
	if err != nil || len(aggregate) != 1 || aggregate[0].ID != legacy.ID {
		t.Errorf("live aggregate: %+v %v", aggregate, err)
	}
	summaries, err := repo.CanvasProjectSummaries("owner")
	if err != nil || len(summaries) != 1 || summaries[0].ID != legacy.ID {
		t.Errorf("live summaries: %+v %v", summaries, err)
	}
	projectRows, err := repo.ProjectCanvasSummaries("owner", "project")
	if err != nil || len(projectRows) != 0 {
		t.Errorf("project live summaries: %+v %v", projectRows, err)
	}
	projectRows, total, err := repo.ProjectCanvasSummariesPage("owner", "project", 1, 10)
	if err != nil || total != 0 || len(projectRows) != 0 {
		t.Errorf("project live page/count: %+v total=%d %v", projectRows, total, err)
	}
	_, err = domain.UserCanvasProject("owner", before.CanvasID)
	assertNotFound(err)
	_, err = domain.UserCanvasProjectMetadata("owner", before.CanvasID)
	assertNotFound(err)
	_, err = domain.CanvasSyncStatus(ctx, "owner", before.CanvasID)
	assertNotFound(err)
	_, err = domain.AcquireCanvasEditLease(ctx, "owner", before.CanvasID, proof.CanvasEditProof)
	assertNotFound(err)
	_, err = domain.RenewCanvasEditLease(ctx, "owner", before.CanvasID, proof.CanvasEditProof)
	assertNotFound(err)
	if err = domain.ReleaseCanvasEditLease(ctx, "owner", before.CanvasID, proof.CanvasEditProof); err != nil {
		t.Errorf("deleted lease release: %v", err)
	}
	if _, err = domain.UserCanvasProject("owner", legacy.ID); err != nil {
		t.Errorf("legacy no-epoch hidden: %v", err)
	}
	if _, err = domain.CanvasSyncStatus(ctx, "owner", legacy.ID); err != nil {
		t.Errorf("legacy status hidden: %v", err)
	}
	// Internal restore, event and receipt reads retain the tombstone.
	if _, err = repo.CanvasProjectForUser("owner", before.CanvasID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.CanvasProjectMetadata("owner", before.CanvasID); err != nil {
		t.Fatal(err)
	}
	epoch, err := repo.CommandEpoch("owner", before.CanvasID)
	if err != nil || !epoch.Deleted {
		t.Fatalf("raw epoch lost tombstone: %+v %v", epoch, err)
	}
	if _, err = svc.GetReceipt(ctx, principal, before.CanvasID, before.DocumentEpoch, req.ClientID, req.OperationID); err != nil {
		t.Fatal(err)
	}
	restored, err := svc.RestoreVersion(ctx, principal, before.CanvasID, before.DocumentEpoch, "client", "restore-library-visibility", deleted.Revision, before.Document)
	if err != nil || restored.DocumentEpoch == before.DocumentEpoch {
		t.Fatalf("restore: %+v %v", restored, err)
	}
	page, err = domain.UserCanvasProjectsPage("owner", 1, 1, "all", "", "updated")
	if err != nil || page.Total != 2 || !page.HasMore {
		t.Fatalf("restored live page: %+v %v", page, err)
	}
	if _, err = domain.UserCanvasProject("owner", before.CanvasID); err != nil {
		t.Fatal(err)
	}
	if metadata, err := domain.UserCanvasProjectMetadata("owner", before.CanvasID); err != nil || metadata.PayloadJSON != "" {
		t.Fatalf("restored live metadata: %+v %v", metadata, err)
	}
	if _, err = domain.CanvasSyncStatus(ctx, "owner", before.CanvasID); err != nil {
		t.Fatal(err)
	}
	grant, err := repo.AcquireCanvasEditLease("owner", before.CanvasID, proof.CanvasEditProof)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.ReleaseCanvasEditLease("owner", before.CanvasID, grant.CanvasEditProof); err != nil {
		t.Fatal(err)
	}
	rename := edit(restored, before.CanvasID, "", "rename-after-restore")
	rename.Operations = []Operation{{Type: "rename_canvas", ID: before.CanvasID, Title: ptr("visible rename")}}
	if _, err = svc.Execute(ctx, principal, rename); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Undo(ctx, principal, before.CanvasID, restored.DocumentEpoch, "client", "undo-live-rename"); err != nil {
		t.Fatal(err)
	}
	if _, err = domain.UserCanvasProject("owner", before.CanvasID); err != nil {
		t.Fatal(err)
	}
	// Polling live status must not acquire account/domain write locks.
	if err = db.Callback().Update().Before("gorm:update").Register("test:status-is-read-only", func(tx *gorm.DB) { tx.AddError(errors.New("status issued UPDATE")) }); err != nil {
		t.Fatal(err)
	}
	if _, err = domain.CanvasSyncStatus(ctx, "owner", before.CanvasID); err != nil {
		t.Errorf("status should be read-only: %v", err)
	}
}

func TestCommandDeleteOverviewCountsTrackRestore(t *testing.T) {
	svc, db, _, before := parityCommandFixture(t)
	if err := db.AutoMigrate(&model.ProjectUnit{}, &model.Shot{}, &model.ProjectAssetLink{}, &model.ProjectAssetCandidate{}, &model.ShotArtifact{}, &model.CanvasUnitLink{}); err != nil {
		t.Fatal(err)
	}
	repo := repository.New(db)
	if err := db.Model(&model.CanvasProject{}).Where("id = ?", before.CanvasID).Update("project_id", "project").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.ProjectUnit{ID: "unit", ProjectID: "project"},
		&model.CanvasProject{ID: "legacy-overview", UserID: "owner", ProjectID: "project", Revision: 1, PayloadJSON: `{"nodes":[],"connections":[]}`},
		&model.CanvasUnitLink{ID: "command-link", ProjectID: "project", CanvasID: before.CanvasID, UnitID: "unit"},
		&model.CanvasUnitLink{ID: "legacy-link", ProjectID: "project", CanvasID: "legacy-overview", UnitID: "unit"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	assertCounts := func(expected int64) {
		t.Helper()
		metrics, err := repo.ProjectOverviewMetrics("project")
		if err != nil || metrics.CanvasCount != expected {
			t.Errorf("overview canvas count=%d want=%d err=%v", metrics.CanvasCount, expected, err)
		}
		counts, err := repo.ProjectUnitCanvasCounts("project")
		if err != nil || counts["unit"] != expected {
			t.Errorf("unit canvas counts=%v want=%d err=%v", counts, expected, err)
		}
		units, err := repo.ProjectOverviewUnits("project", 8)
		if err != nil || len(units) != 1 || units[0].CanvasCount != expected {
			t.Errorf("overview units=%+v want=%d err=%v", units, expected, err)
		}
	}
	assertCounts(2)
	req := edit(before, before.CanvasID, "", "delete-overview-counts")
	req.Operations = []Operation{{Type: "delete_canvas", ID: before.CanvasID}}
	deleted, err := svc.Execute(context.Background(), ownerContext().Principal, req)
	if err != nil {
		t.Fatal(err)
	}
	assertCounts(1)
	if raw, err := repo.ProjectCanvasDocuments("owner", "project"); err != nil || len(raw) != 2 {
		t.Errorf("internal documents filtered: %d %v", len(raw), err)
	}
	if _, err = svc.RestoreVersion(context.Background(), ownerContext().Principal, before.CanvasID, before.DocumentEpoch, "client", "restore-overview-counts", deleted.Revision, before.Document); err != nil {
		t.Fatal(err)
	}
	assertCounts(2)
}
