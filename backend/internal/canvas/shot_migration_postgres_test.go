package canvas

import (
	"encoding/json"
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

func newCanvasShotMigrationPostgresService(t *testing.T) (*Service, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set CANVAS_TEST_POSTGRES_DSN to an isolated test database")
	}
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(postgres.Open(dsn), config)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("canvas_shot_migration_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
		connection, _ := admin.DB()
		_ = connection.Close()
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		connection, _ := db.DB()
		_ = connection.Close()
	})
	if err := db.AutoMigrate(
		&model.CanvasEditLease{},
		&model.CanvasProject{},
		&model.CanvasSnapshot{},
		&model.CanvasSnapshotResource{},
		&model.CanvasMutationReceipt{},
		&model.CanvasShare{},
		&model.CanvasUnitLink{},
		&model.Task{},
		&model.CloudAgentExecution{},
		&model.Resource{},
		&model.Asset{},
	); err != nil {
		t.Fatal(err)
	}
	t.Log("using isolated PostgreSQL schema", schema)
	return New(repository.New(db), nil), db
}

func TestShotMigrationPostgresPreviewAndRestore(t *testing.T) {
	svc, _ := newCanvasShotMigrationPostgresService(t)
	shotMigrationSeed(t, svc, "postgres-legacy", 3)
	preview, err := svc.PreviewCanvasShotMigration("user", "postgres-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Required || preview.Rollback != nil || preview.Rows != 2 || preview.ShotSubmaps != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	result, err := svc.MigrateCanvasShots("user", "postgres-legacy", CanvasShotMigrationRequest{Revision: &preview.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if result.Project.Revision != preview.Revision+1 {
		t.Fatalf("migration revision = %d, want %d", result.Project.Revision, preview.Revision+1)
	}
	rollback, err := svc.PreviewCanvasShotMigration("user", "postgres-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Required || rollback.Rollback == nil || !rollback.Rollback.CanRestore || rollback.Rollback.CanCopy {
		t.Fatalf("rollback preview = %+v", rollback)
	}
	restored, err := svc.RollbackCanvasShots("user", "postgres-legacy", CanvasShotMigrationRollbackRequest{Revision: &result.Project.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Restored || restored.SnapshotID == "" || restored.Revision != result.Project.Revision+1 {
		t.Fatalf("restore = %+v", restored)
	}
	afterRestore, err := svc.PreviewCanvasShotMigration("user", "postgres-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if !afterRestore.Required || afterRestore.Rollback != nil {
		t.Fatalf("restored preview = %+v", afterRestore)
	}
}

func TestShotMigrationPostgresActiveRollbackAndCopy(t *testing.T) {
	svc, _ := newCanvasShotMigrationPostgresService(t)
	shotMigrationSeed(t, svc, "postgres-copy", 2)
	preview, err := svc.PreviewCanvasShotMigration("user", "postgres-copy")
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.MigrateCanvasShots("user", "postgres-copy", CanvasShotMigrationRequest{Revision: &preview.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.repo.Create(&model.Task{ID: "postgres-active-rollback", UserID: "user", ProjectID: "postgres-copy", Status: model.TaskStatusRunning}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RollbackCanvasShots("user", "postgres-copy", CanvasShotMigrationRollbackRequest{Revision: &result.Project.Revision}); referenceMigrationReason(err) != "shot_migration_tasks_active" {
		t.Fatalf("active task rollback error = %v", err)
	}
	if err := svc.repo.Delete(&model.Task{ID: "postgres-active-rollback"}, "id = ?", "postgres-active-rollback"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyUserCanvasOperations("user", "postgres-copy", CanvasOperationsRequest{
		MutationID:         "postgres-newer-edit",
		ClientCapabilities: []string{CanvasClientCapabilityReferenceFields},
		Operations: []CanvasOperation{{
			Type: "patch_node", ID: "submap-1",
			Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"镜头 1 新标题"`)},
			Before: map[string]json.RawMessage{"title": json.RawMessage(`"镜头 1"`)},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.repo.CanvasProjectForUser("user", "postgres-copy")
	if err != nil {
		t.Fatal(err)
	}
	copied, err := svc.RollbackCanvasShots("user", "postgres-copy", CanvasShotMigrationRollbackRequest{
		Revision: &current.Revision, Strategy: "new_canvas",
	})
	if err != nil {
		t.Fatal(err)
	}
	if copied.NewCanvasID == "" {
		t.Fatalf("copy result = %+v", copied)
	}
	kept, err := svc.repo.CanvasProjectForUser("user", "postgres-copy")
	if err != nil || kept.Revision != current.Revision {
		t.Fatalf("current canvas changed: revision=%d err=%v", kept.Revision, err)
	}
	copyProject, err := svc.repo.CanvasProjectForUser("user", copied.NewCanvasID)
	if err != nil {
		t.Fatal(err)
	}
	if copyProject.Revision != 1 {
		t.Fatalf("copied preimage revision = %d", copyProject.Revision)
	}
}

func TestShotMigrationPostgresCapturesPreimageAfterRecentAutomaticSnapshot(t *testing.T) {
	svc, _ := newCanvasShotMigrationPostgresService(t)
	shotMigrationSeed(t, svc, "postgres-recent-history", 3)
	if _, err := svc.ApplyUserCanvasOperations("user", "postgres-recent-history", CanvasOperationsRequest{
		MutationID: "postgres-recent-edit",
		Operations: []CanvasOperation{{
			Type: "patch_node", ID: "old-image",
			Patch:  map[string]json.RawMessage{"title": json.RawMessage(`"已出图 2"`)},
			Before: map[string]json.RawMessage{"title": json.RawMessage(`"已出图"`)},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewCanvasShotMigration("user", "postgres-recent-history")
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Required || preview.Revision != 4 {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := svc.MigrateCanvasShots("user", "postgres-recent-history", CanvasShotMigrationRequest{Revision: &preview.Revision}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := svc.repo.LatestCanvasSnapshotWithReason("user", "postgres-recent-history", shotMigrationSnapshotReason)
	if err != nil || snapshot.Revision != preview.Revision {
		t.Fatalf("migration preimage = %+v, %v; want revision %d", snapshot, err, preview.Revision)
	}
	rollback, err := svc.PreviewCanvasShotMigration("user", "postgres-recent-history")
	if err != nil {
		t.Fatal(err)
	}
	if rollback.Rollback == nil || !rollback.Rollback.CanRestore {
		t.Fatalf("rollback preview = %+v", rollback)
	}
}
