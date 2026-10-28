package repository

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func canvasLifecycleFixture(t *testing.T, pg bool) (*Repository, *gorm.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lifecycle.db") + "?_busy_timeout=5000&_journal_mode=WAL"
	var dialect gorm.Dialector = sqlite.Open(path)
	if pg {
		dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set CANVAS_TEST_POSTGRES_DSN to an isolated test database")
		}
		admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		schema := fmt.Sprintf("canvas_lifecycle_%d", time.Now().UnixNano())
		if err = admin.Exec("CREATE SCHEMA " + schema).Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := admin.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
				t.Error(err)
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
		path, dialect = parsed.String(), postgres.Open(parsed.String())
	}
	db, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection, _ := db.DB(); _ = connection.Close() })
	if err = db.AutoMigrate(&model.User{}, &model.CanvasProject{}, &model.CanvasNodeEditLease{}, &model.Project{}, &model.Asset{}, &model.Resource{}, &model.CanvasSnapshot{}, &model.CanvasSnapshotResource{}, &model.CanvasShare{}, &model.CanvasUnitLink{}, &model.Task{}); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasCommands(db); err != nil {
		t.Fatal(err)
	}
	if err = database.MigrateCanvasTasks(db); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&model.User{ID: "owner", Status: model.UserStatusActive}).Error; err != nil {
		t.Fatal(err)
	}
	return New(db), db, path
}

func TestNestedCanvasWritersLockAccountBeforeDomainRows(t *testing.T) {
	for _, name := range []string{"completion", "agent", "agent-control", "agent-family", "creation", "dynamic-admission"} {
		t.Run(name, func(t *testing.T) {
			repo, db, _ := canvasLifecycleFixture(t, false)
			stop := errors.New("stop at first domain update")
			var order []string
			if err := db.Callback().Update().Before("gorm:update").Register("test:account-before-domain", func(tx *gorm.DB) {
				order = append(order, tx.Statement.Table)
				if tx.Statement.Table != "users" {
					tx.AddError(stop)
				}
			}); err != nil {
				t.Fatal(err)
			}
			var err error
			switch name {
			case "completion":
				err = repo.SaveTaskCompletionWithRegistration(&model.Task{ID: "task", UserID: "owner"}, model.TaskStatusRunning, nil, nil)
			case "agent":
				err = repo.MutateCloudAgent("owner", "run", 0, nil)
			case "agent-control":
				err = repo.MutateCloudAgentControl("owner", "run", 0, nil)
			case "agent-family":
				err = repo.WithAgentSubagentFamily("owner", "parent", "child", nil)
			case "creation":
				err = repo.MutateCreationRun("owner", "creation", nil)
			case "dynamic-admission":
				err = repo.CreateDynamicSubagentAdmission(CloudAgentAdmission{}, &model.AgentSubagentLink{UserID: "owner", ParentRunID: "parent"}, 10, 10, 10)
			}
			if !errors.Is(err, stop) || len(order) < 2 || order[0] != "users" {
				t.Fatalf("root lock order %v: %v", order, err)
			}
		})
	}
}

func TestPostgresTaskCompletionAndCanvasDeletionShareRootAccountLock(t *testing.T) {
	repo, db, path := canvasLifecycleFixture(t, true)
	other, err := gorm.Open(postgres.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, _ := other.DB()
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = connection.Close() })
	var deletionPID int
	if err = other.Raw("SELECT pg_backend_pid()").Scan(&deletionPID).Error; err != nil {
		t.Fatal(err)
	}
	row := &model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: `{"nodes":[],"connections":[]}`}
	if err = repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	task := &model.Task{ID: "task", UserID: "owner", ProjectID: row.ID, Status: model.TaskStatusRunning}
	if err = db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	registered, release := make(chan struct{}), make(chan struct{})
	completed, deleted := make(chan error, 1), make(chan error, 1)
	go func() {
		task.Status = model.TaskStatusSucceeded
		completed <- repo.WithContext(ctx).SaveTaskCompletionWithRegistration(task, model.TaskStatusRunning, nil, func(tx *Repository) error {
			close(registered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return tx.UpsertAsset(&model.Asset{ID: "output", UserID: "owner", PayloadJSON: "{}"})
		})
	}()
	select {
	case <-registered:
	case err := <-completed:
		t.Fatalf("completion did not reach registration: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() { deleted <- New(other).WithContext(ctx).DeleteCanvasProject("owner", row.ID) }()
	// Wait for a real database lock wait. Before the fix this is Task; after
	// the fix it is User, before deletion has obtained any domain row lock.
	blocked := false
	for !blocked && ctx.Err() == nil {
		if err = db.WithContext(ctx).Raw("SELECT cardinality(pg_blocking_pids(?)) > 0", deletionPID).Scan(&blocked).Error; err != nil {
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
	completionErr, deletionErr := <-completed, <-deleted
	if err != nil || !blocked || completionErr != nil || deletionErr != nil {
		t.Fatalf("actual root transactions: blocked=%v completion=%v deletion=%v probe=%v", blocked, completionErr, deletionErr, err)
	}
	stored, err := repo.Task(task.ID)
	if err != nil || stored.Status != model.TaskStatusSucceeded || stored.ProjectID != "" {
		t.Fatalf("completion/deletion state: %+v %v", stored, err)
	}
	if _, err = repo.AssetForUser("owner", "output"); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyResourcesFollowCurrentSnapshotAndDeleteRetention(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "sqlite"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			repo, db, _ := canvasLifecycleFixture(t, pg)
			image := &model.Resource{ID: "image", UserID: "owner", Status: model.ResourceStatusReady, Endpoint: "store", Bucket: "bucket", ObjectKey: "image"}
			alias := &model.Resource{ID: "alias", UserID: "owner", Status: model.ResourceStatusReady, Endpoint: image.Endpoint, Bucket: image.Bucket, ObjectKey: image.ObjectKey}
			if err := db.Create(image).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(alias).Error; err != nil {
				t.Fatal(err)
			}
			payload := `{"nodes":[{"id":"image","type":"image","metadata":{"storageKey":"resource:image"}}],"connections":[]}`
			row := &model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: payload}
			for i := 0; i < 3; i++ {
				if err := repo.UpsertCanvasProject(row); err != nil {
					t.Fatal(err)
				}
			}
			var pins int64
			if err := db.Model(&model.CanvasCommandResource{}).Where("canvas_id = ?", row.ID).Count(&pins).Error; err != nil || pins != 1 {
				t.Fatalf("autosave accumulated pins: %d %v", pins, err)
			}
			assertProtected := func(expected bool) {
				t.Helper()
				actual, err := repo.CanvasHistoryReferencesObject(alias)
				if err != nil || actual != expected {
					t.Fatalf("alias protected=%v want=%v: %v", actual, expected, err)
				}
			}
			assertProtected(true)
			first := &model.CanvasSnapshot{ID: "first", CanvasID: row.ID, UserID: row.UserID, Revision: row.Revision, PayloadJSON: payload, CreatedAt: time.Now()}
			row.PayloadJSON = `{"nodes":[],"connections":[]}`
			if err := repo.SaveCanvasWithSnapshot(row, first, []string{image.ID}, nil, time.Now(), 1, true); err != nil {
				t.Fatal(err)
			}
			assertProtected(true)
			second := &model.CanvasSnapshot{ID: "second", CanvasID: row.ID, UserID: row.UserID, Revision: row.Revision, PayloadJSON: row.PayloadJSON, CreatedAt: time.Now()}
			if err := repo.SaveCanvasWithSnapshot(row, second, nil, nil, time.Now(), 1, true); err != nil {
				t.Fatal(err)
			}
			assertProtected(false)
			row.PayloadJSON = payload
			if err := repo.UpsertCanvasProject(row); err != nil {
				t.Fatal(err)
			}
			epoch, err := repo.CommandEpoch("owner", row.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate an orphan emitted by the prior legacy projection.
			if err = db.Create(&model.CanvasCommandResource{OwnerID: "owner", CanvasID: row.ID, Epoch: epoch.Epoch, OperationID: "legacy:old:pin", Kind: "history", ResourceID: image.ID}).Error; err != nil {
				t.Fatal(err)
			}
			if err = repo.DeleteCanvasProject("owner", row.ID); err != nil {
				t.Fatal(err)
			}
			assertProtected(false)
			if err = db.Model(&model.CanvasCommandResource{}).Where("canvas_id = ?", row.ID).Count(&pins).Error; err != nil || pins != 0 {
				t.Fatalf("terminal deletion left orphan pins: %d %v", pins, err)
			}
			// Confirm the released object can enter the ordinary cleanup transaction.
			if err = db.AutoMigrate(&model.Announcement{}, &model.AnnouncementImageDraft{}, &model.AssetRepresentation{}, &model.VoiceProfile{}, &model.ShotArtifact{}, &model.ArkPrivateAssetBinding{}, &model.ResourceDeletionJob{}); err != nil {
				t.Fatal(err)
			}
			if err = repo.DeleteDetachedResources([]model.Resource{*image}, nil); err != nil {
				t.Fatalf("released resource cleanup: %v", err)
			}
		})
	}
}

func TestLegacyDeletionPreservesActualCommandHistoryAliasesAcrossEpochs(t *testing.T) {
	repo, db, _ := canvasLifecycleFixture(t, false)
	resource := &model.Resource{ID: "image", UserID: "owner", Endpoint: "store", Bucket: "bucket", ObjectKey: "image"}
	if err := db.Create(resource).Error; err != nil {
		t.Fatal(err)
	}
	row := &model.CanvasProject{ID: "canvas", UserID: "owner", PayloadJSON: `{"nodes":[],"connections":[]}`}
	if err := repo.UpsertCanvasProject(row); err != nil {
		t.Fatal(err)
	}
	for _, epoch := range []string{"old-epoch", "new-epoch"} {
		op := "legacy:actual:" + epoch
		if err := repo.SaveActorHistory(&model.CanvasActorHistory{ID: epoch, OwnerID: "owner", CanvasID: row.ID, Epoch: epoch, OperationID: op, State: "archived", ForwardJSON: "{}", InverseJSON: "{}", ExpectedVersionsJSON: "{}"}); err != nil {
			t.Fatal(err)
		}
		if err := repo.SaveCommandResources("owner", row.ID, epoch, op, nil, []string{resource.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.DeleteCanvasProject("owner", row.ID); err != nil {
		t.Fatal(err)
	}
	alias := *resource
	alias.ID = "alias"
	protected, err := repo.CanvasHistoryReferencesObject(&alias)
	var pins int64
	countErr := db.Model(&model.CanvasCommandResource{}).Where("kind = ?", "history").Count(&pins).Error
	if err != nil || countErr != nil || !protected || pins != 2 {
		t.Fatalf("valid archived aliases lost: protected=%v pins=%d err=%v %v", protected, pins, err, countErr)
	}
}

func TestUpdateProjectRevisionNeverRewindsFromStaleServiceValue(t *testing.T) {
	repo, db, _ := canvasLifecycleFixture(t, false)
	project := &model.Project{ID: "project", UserID: "owner", Name: "initial", Revision: 8}
	if err := db.Create(project).Error; err != nil {
		t.Fatal(err)
	}
	stale := *project
	stale.Revision = 2
	stale.Name = "stale service value"
	if err := repo.UpdateProject(&stale); err != nil {
		t.Fatal(err)
	}
	if stale.Revision != 9 {
		t.Fatalf("returned revision=%d want=9", stale.Revision)
	}
	var stored model.Project
	if err := db.First(&stored, "id = ?", project.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stale.Revision, stored.Revision) || stored.Name != stale.Name {
		t.Fatalf("project state %+v returned %+v", stored, stale)
	}
}
