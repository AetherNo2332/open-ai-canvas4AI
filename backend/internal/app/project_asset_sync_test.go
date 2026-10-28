package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func syncFixture(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	s, db := newProjectAssetLinkTestService(t)
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.User{ID: "owner", Username: "owner", Email: "owner@example.test", Phone: "owner"}, &model.User{ID: "other", Username: "other", Email: "other@example.test", Phone: "other"},
		&model.Project{ID: "project", UserID: "owner", Revision: 4},
		&model.Project{ID: "foreign", UserID: "other"},
		&model.ProjectAssetFolder{ID: "folder", ProjectID: "project", NameKey: "folder"},
		&model.ProjectAssetFolder{ID: "foreign-folder", ProjectID: "foreign", NameKey: "foreign"},
		&model.Asset{ID: "asset", UserID: "owner", Kind: "text", Title: "remote title", Category: model.AssetCategoryMaterial, PayloadJSON: `{"data":{"content":"remote body","source":"canvas"}}`},
		&model.Asset{ID: "foreign-asset", UserID: "other", Category: model.AssetCategoryOther},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return s, db
}

func postgresSyncFixture(t *testing.T) (*Service, *Service, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set CANVAS_TEST_POSTGRES_DSN to an isolated local database")
	}
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("asset_sync_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
		raw, _ := admin.DB()
		_ = raw.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	open := func() *gorm.DB {
		db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { raw, _ := db.DB(); _ = raw.Close() })
		return db
	}
	db, other := open(), open()
	if err := db.AutoMigrate(&model.User{}, &model.Project{}, &model.Asset{}, &model.AssetVersion{}, &model.ProjectAssetLink{}, &model.ProjectAssetFolder{}, &model.AssetRepresentation{}, &model.Shot{}, &model.ShotAssetReference{}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{&model.User{ID: "owner", Username: "owner"}, &model.Project{ID: "project", UserID: "owner", Revision: 4}, &model.Asset{ID: "asset", UserID: "owner", Category: model.AssetCategoryMaterial, Kind: "text", PayloadJSON: `{}`}, &model.ProjectAssetLink{ID: "link", ProjectID: "project", AssetID: "asset"}, &model.ProjectAssetFolder{ID: "new", ProjectID: "project", NameKey: "new"}, &model.ProjectAssetFolder{ID: "old", ProjectID: "project", NameKey: "old"}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return &Service{repo: repository.New(db)}, &Service{repo: repository.New(other)}, db
}

func TestPostgresProjectAssetSyncLateRequestAndSnapshot(t *testing.T) {
	reader, writer, db := postgresSyncFixture(t)
	oldBaseline, err := reader.GetProjectAssetSync("owner", "project", "asset")
	if err != nil {
		t.Fatal(err)
	}
	// Pause GET after reading the project. Another connection commits a folder
	// change before GET reads the relationship: both fields must remain old.
	started, committed := make(chan struct{}), make(chan struct{})
	if err := db.Callback().Query().After("gorm:query").Register("test:pause-sync-snapshot", func(tx *gorm.DB) {
		if tx.Statement.Table == "projects" {
			close(started)
			<-committed
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:pause-sync-snapshot") })
	type result struct {
		state ProjectAssetSyncState
		err   error
	}
	snapshot := make(chan result, 1)
	go func() {
		state, err := reader.GetProjectAssetSync("owner", "project", "asset")
		snapshot <- result{state, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("GET did not reach project read")
	}
	folder := "new"
	newState, writeErr := writer.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: oldBaseline.ProjectRevision, FolderID: &folder})
	close(committed)
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	read := <-snapshot
	if read.err != nil || read.state.ProjectRevision != 4 || read.state.Asset.FolderID != "" {
		t.Fatalf("torn snapshot: %+v %v", read.state, read.err)
	}
	if err := db.Callback().Query().Remove("test:pause-sync-snapshot"); err != nil {
		t.Fatal(err)
	}
	folder = "old"
	_, err = reader.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: oldBaseline.ProjectRevision, FolderID: &folder})
	var conflict *AppError
	if !errors.As(err, &conflict) || conflict.Status != 409 {
		t.Fatalf("late request accepted: %v", err)
	}
	final, err := writer.GetProjectAssetSync("owner", "project", "asset")
	if err != nil || final.ProjectRevision != newState.ProjectRevision || final.Asset.FolderID != "new" {
		t.Fatalf("late request changed new state: %+v %v", final, err)
	}
}

func TestPostgresProjectAssetSyncConcurrentCAS(t *testing.T) {
	a, b, _ := postgresSyncFixture(t)
	start, results := make(chan struct{}), make(chan error, 2)
	for i, svc := range []*Service{a, b} {
		go func(i int, svc *Service) {
			<-start
			folder := []string{"old", "new"}[i]
			_, err := svc.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: 4, FolderID: &folder})
			results <- err
		}(i, svc)
	}
	close(start)
	success, conflicts := 0, 0
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				success++
			} else {
				var appErr *AppError
				if !errors.As(err, &appErr) || appErr.Status != 409 {
					t.Fatal(err)
				}
				conflicts++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent sync deadlocked")
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflict=%d", success, conflicts)
	}
}

func TestProjectAssetSyncAtomicCASAndPreservation(t *testing.T) {
	s, db := syncFixture(t)
	initial, err := s.GetProjectAssetSync("owner", "project", "asset")
	if err != nil || initial.ProjectRevision != 4 || initial.Asset != nil {
		t.Fatalf("unlinked baseline: %+v %v", initial, err)
	}
	category, folder := "prop", "folder"
	first, err := s.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: 4, Category: &category, FolderID: &folder})
	if err != nil {
		t.Fatal(err)
	}
	if first.ProjectRevision <= 4 || first.Asset == nil || first.Asset.Category != model.AssetCategoryProp || first.Asset.FolderID != "folder" || first.Asset.VersionCount != 1 || first.Asset.Source != "canvas" {
		t.Fatalf("sync: %+v", first)
	}
	root, oldCategory := "", "other"
	_, err = s.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: 4, Category: &oldCategory, FolderID: &root})
	var conflict *AppError
	if !errors.As(err, &conflict) || conflict.Status != 409 || conflict.Reason != "project_asset_sync_conflict" || !conflict.Retryable {
		t.Fatalf("stale: %v", err)
	}
	current, err := s.GetProjectAssetSync("owner", "project", "asset")
	if err != nil || current.ProjectRevision != first.ProjectRevision || current.Asset.Category != model.AssetCategoryProp || current.Asset.FolderID != "folder" {
		t.Fatalf("stale modified persisted state: %+v %v", current, err)
	}
	moved, err := s.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: current.ProjectRevision, FolderID: &root})
	if err != nil || moved.Asset.FolderID != "" || moved.Asset.Category != model.AssetCategoryProp {
		t.Fatalf("root/preserve: %+v %v", moved, err)
	}
	var asset model.Asset
	if err := db.First(&asset, "id = ?", "asset").Error; err != nil {
		t.Fatal(err)
	}
	if asset.Title != "remote title" || asset.PayloadJSON != `{"data":{"content":"remote body","source":"canvas"}}` {
		t.Fatalf("body overwritten: %+v", asset)
	}
}

func TestProjectAssetSyncValidationRollsBackReservation(t *testing.T) {
	for _, field := range []string{"folder", "category"} {
		t.Run(field, func(t *testing.T) {
			s, _ := syncFixture(t)
			category, folder := "prop", "foreign-folder"
			if field == "category" {
				category, folder = "invalid", "folder"
			}
			if _, err := s.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: 4, Category: &category, FolderID: &folder}); err == nil {
				t.Fatal("invalid target accepted")
			}
			state, err := s.GetProjectAssetSync("owner", "project", "asset")
			if err != nil || state.ProjectRevision != 4 || state.Asset != nil {
				t.Fatalf("failed transaction leaked: %+v %v", state, err)
			}
			asset, err := s.repo.AssetForUser("owner", "asset")
			if err != nil || asset.Category != model.AssetCategoryMaterial || asset.PrimaryVersionID != "" {
				t.Fatalf("failed transaction modified asset: %+v %v", asset, err)
			}
		})
	}
}

func TestProjectAssetSyncOwnership(t *testing.T) {
	s, _ := syncFixture(t)
	for _, target := range [][3]string{{"other", "project", "asset"}, {"owner", "foreign", "asset"}, {"owner", "project", "foreign-asset"}, {"owner", "project", "missing"}} {
		_, getErr := s.GetProjectAssetSync(target[0], target[1], target[2])
		_, putErr := s.SyncProjectAsset(target[0], target[1], target[2], SyncProjectAssetRequest{ExpectedProjectRevision: 4})
		for _, err := range []error{getErr, putErr} {
			var appErr *AppError
			if !errors.As(err, &appErr) || appErr.Status != 404 {
				t.Fatalf("target %v: %v", target, err)
			}
		}
	}
}

func TestProjectAssetSyncStorageFailureRollsBackWholeBatch(t *testing.T) {
	s, db := syncFixture(t)
	initial, err := s.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER reject_sync_move BEFORE UPDATE ON project_asset_links BEGIN SELECT RAISE(ABORT, 'forced move failure'); END;").Error; err != nil {
		t.Fatal(err)
	}
	category, folder := "prop", "folder"
	if _, err := s.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: initial.ProjectRevision, Category: &category, FolderID: &folder}); err == nil {
		t.Fatal("storage failure ignored")
	}
	state, err := s.GetProjectAssetSync("owner", "project", "asset")
	if err != nil || state.ProjectRevision != initial.ProjectRevision || state.Asset.Category != model.AssetCategoryMaterial || state.Asset.FolderID != "" || state.Asset.VersionCount != 1 {
		t.Fatalf("partial batch persisted: %+v %v", state, err)
	}
}

func TestProjectAssetSyncLegacyUpdatesInvalidateBaseline(t *testing.T) {
	for _, field := range []string{"category", "folder"} {
		t.Run(field, func(t *testing.T) {
			s, _ := syncFixture(t)
			initial, err := s.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: 4})
			if err != nil {
				t.Fatal(err)
			}
			category, folder := "prop", "folder"
			request := UpdateProjectAssetRequest{Category: &category}
			if field == "folder" {
				request = UpdateProjectAssetRequest{FolderID: &folder}
			}
			if _, err := s.UpdateProjectAsset("owner", "project", "asset", request); err != nil {
				t.Fatal(err)
			}
			_, err = s.SyncProjectAsset("owner", "project", "asset", SyncProjectAssetRequest{ExpectedProjectRevision: initial.ProjectRevision})
			var conflict *AppError
			if !errors.As(err, &conflict) || conflict.Status != 409 {
				t.Fatalf("legacy change did not invalidate CAS: %v", err)
			}
		})
	}
}
