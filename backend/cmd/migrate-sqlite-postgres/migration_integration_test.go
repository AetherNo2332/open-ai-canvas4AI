package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func migrationPostgres(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	dsn := os.Getenv("CANVAS_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := database.Open(database.Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal("open PostgreSQL test administration connection:", err)
	}
	adminSQL, err := admin.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adminSQL.Close() })
	name := fmt.Sprintf("sdk_migration_%d", time.Now().UnixNano())
	if err := admin.Exec(`CREATE DATABASE "` + name + `"`).Error; err != nil {
		t.Fatal("test DSN must allow creating an isolated database:", err)
	}
	t.Cleanup(func() {
		if err := admin.Exec(`DROP DATABASE "` + name + `"`).Error; err != nil {
			t.Error("drop isolated database:", err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test DSN must be a postgres URL")
	}
	u.Path = "/" + name
	db, err := database.Open(database.Config{Driver: "postgres", DSN: u.String()})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}), u.String()
}

func migrationSource(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.db")
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.User{ID: "u", Username: "migration-user"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.SkillCurationSetting{}).Where("id = 1").Updates(map[string]any{"enabled": true, "revision": 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.SkillCurationRoot{}).Where("id = ?", "drama").Update("name", "Edited source root").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.SkillCurationCategory{ID: "category", RootTag: "drama", Name: "Source", NormalizedName: "source"},
		&model.SkillCurationAssignment{SkillID: "skill", CategoryID: "category"},
		&model.SkillCurationRootAssignment{SkillID: "skill", RootID: "drama"},
		&model.ModelChannel{ID: "CHANNEL_000003", Name: "source channel"},
		&model.Task{ID: "legacy-inflight", UserID: "u", Type: "canvas_image", Status: model.TaskStatusRunning, BillingOrderID: "legacy-order"},
		&model.BillingOrder{ID: "legacy-order", UserID: "u", TaskID: "legacy-inflight", IdempotencyKey: "legacy-order", Status: model.BillingStatusReserved, AmountMicrocredits: 200},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	stamp := time.Date(2025, 1, 2, 3, 4, 5, 123000000, time.UTC)
	if err := db.Model(&model.IDSequence{}).Where("name = ?", "id:CHANNEL").Updates(map[string]any{"value": 100, "updated_at": stamp}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PlatformEventCounter{ID: 1}).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		task, order, sub := fmt.Sprintf("task-%d", i), fmt.Sprintf("order-%d", i), fmt.Sprintf("sub-%d", i)
		for _, row := range []any{
			&model.Task{ID: task, UserID: "u", Type: "canvas_image", Status: model.TaskStatusQueued, BillingOrderID: order, InputJSON: "{}"},
			&model.BillingOrder{ID: order, UserID: "u", TaskID: task, IdempotencyKey: order, Status: model.BillingStatusReserved, AmountMicrocredits: 100},
			&model.PlatformSubmission{ID: sub, UserID: "u", Application: "test", TaskID: task, IdempotencyKey: sub, Fingerprint: sub, Capability: "image", ProjectRef: "project", BusinessJSON: `{"application":"test","projectId":"project"}`},
		} {
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
		}
		pos := int64(i*4 + 1)
		if err := db.Create(&model.PlatformTaskEvent{ID: fmt.Sprintf("z-submitted-%d", i), Position: &pos, TaskID: task, UserID: "u", Application: "test", ProjectRef: "project", Type: "task.submitted", TaskStatus: "queued", BillingStatus: "reserved", OccurredAtMs: pos}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.PlatformEventCounter{}).Where("id = 1").Update("value", pos).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.Task{}).Where("id = ?", task).Update("status", model.TaskStatusRunning).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.Task{}).Where("id = ?", task).Updates(map[string]any{"status": model.TaskStatusSucceeded, "media_stage": "completed", "result_json": `{"resourceId":"resource"}`}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.BillingOrder{}).Where("id = ?", order).Updates(map[string]any{"status": model.BillingStatusSettled, "actual_amount_microcredits": 100}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.Resource{ID: "resource", UserID: "u", Kind: "image", Status: model.ResourceStatusReady}).Error; err != nil {
		t.Fatal(err)
	}
	// The migrator must not modify even a fully current source.
	readonly, err := database.Open(database.Config{Driver: "sqlite", DSN: "file:" + filepath.ToSlash(path) + "?mode=ro"})
	if err != nil {
		t.Fatal(err)
	}
	roSQL, _ := readonly.DB()
	t.Cleanup(func() { _ = roSQL.Close() })
	return readonly.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}), path
}

func assertImportedEvents(t *testing.T, source, target *gorm.DB) {
	t.Helper()
	var want, got []model.PlatformTaskEvent
	if err := source.Order("position").Find(&want).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Order("position").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	if len(want) != 12 || !reflect.DeepEqual(want, got) {
		t.Fatalf("events after COMMIT differ: source=%+v target=%+v", want, got)
	}
	var counter model.PlatformEventCounter
	if err := target.First(&counter, "id = 1").Error; err != nil || counter.Value != 12 || counter.PrunedBefore != 0 {
		t.Fatalf("counter after COMMIT: %+v %v", counter, err)
	}
}

func TestMigrationFreshPostgresAndRepeat(t *testing.T) {
	target, _ := migrationPostgres(t)
	source, _ := migrationSource(t)
	for i := 0; i < 2; i++ {
		if err := runMigration(source, target); err != nil {
			t.Fatalf("migration run %d: %v", i+1, err)
		}
		assertImportedEvents(t, source, target)
		for _, migration := range migrations() {
			if _, err := migration.run(source, target, false); err != nil {
				t.Fatalf("after commit table %s: %v", migration.name, err)
			}
		}
	}
	id, err := database.AllocatePrefixedID(target, "CHANNEL")
	if err != nil || id != "CHANNEL_000101" {
		t.Fatalf("source high-water mark lost: %s %v", id, err)
	}
	// Verify continuation using the installed runtime trigger after the repeat.
	if err := target.Model(&model.Task{}).Where("id = ?", "task-0").Update("status", model.TaskStatusRunning).Error; err != nil {
		t.Fatal(err)
	}
	var counter model.PlatformEventCounter
	if err := target.First(&counter, "id = 1").Error; err != nil || counter.Value != 13 {
		t.Fatalf("new event did not continue cursor: %+v %v", counter, err)
	}
}

func TestMigrationRollbackAndNoPrematureSuccess(t *testing.T) {
	for _, failure := range []string{"copy", "reconcile", "commit"} {
		t.Run(failure, func(t *testing.T) {
			target, _ := migrationPostgres(t)
			source, sourcePath := migrationSource(t)
			installed := false
			if failure == "reconcile" {
				makeSourceSequenceLag(t, sourcePath)
				if err := target.Callback().Update().Before("gorm:update").Register("migration_test_reconcile_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "id_sequences" {
						installed = true
						tx.AddError(errors.New("injected reconciliation failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := target.Callback().Create().Before("gorm:create").Register("migration_test_failure", func(tx *gorm.DB) {
				if failure == "reconcile" || tx.Statement.Table != "platform_task_events" || installed {
					return
				}
				installed = true
				if failure == "copy" {
					tx.AddError(errors.New("injected copy failure"))
					return
				}
				tx.AddError(tx.Exec(`CREATE FUNCTION reject_import_commit() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'injected commit failure'; END; $$ LANGUAGE plpgsql`).Error)
				tx.AddError(tx.Exec(`CREATE CONSTRAINT TRIGGER reject_import_commit AFTER INSERT ON platform_task_events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_import_commit()`).Error)
			}); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			prior := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(prior)
			err := runMigration(source, target)
			if err == nil || !strings.Contains(err.Error(), "injected") || !installed {
				t.Fatalf("expected injected %s failure, got %v", failure, err)
			}
			if strings.Contains(output.String(), "全量迁移核对完成") {
				t.Fatal("reported success before rollback/failed commit")
			}
			names, err := publicTableNames(target)
			if err != nil || len(names) != 0 {
				t.Fatalf("failed migration left tables: %v %v", names, err)
			}
		})
	}
}

func makeSourceSequenceLag(t *testing.T, path string) {
	t.Helper()
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err := db.Model(&model.IDSequence{}).Where("name = ?", "id:CHANNEL").Update("value", 1).Error; err != nil {
		t.Fatal(err)
	}
}

func TestMigrationLaggingSequenceRepeatsAfterReconciliation(t *testing.T) {
	target, _ := migrationPostgres(t)
	source, path := migrationSource(t)
	makeSourceSequenceLag(t, path)
	for i := 0; i < 2; i++ {
		if err := runMigration(source, target); err != nil {
			t.Fatalf("lagging source migration run %d: %v", i+1, err)
		}
		assertImportedEvents(t, source, target)
	}
	id, err := database.AllocatePrefixedID(target, "CHANNEL")
	if err != nil || id != "CHANNEL_000004" {
		t.Fatalf("lagging sequence not reconciled: %s %v", id, err)
	}
	var original model.IDSequence
	if err := source.First(&original, "name = ?", "id:CHANNEL").Error; err != nil || original.Value != 1 {
		t.Fatalf("read-only source changed: %+v %v", original, err)
	}
}

func TestMigrationRejectsPartialOrExtraTargetTables(t *testing.T) {
	for _, kind := range []string{"partial", "extra"} {
		t.Run(kind, func(t *testing.T) {
			target, _ := migrationPostgres(t)
			source, _ := migrationSource(t)
			if kind == "extra" {
				if err := runMigration(source, target); err != nil {
					t.Fatal(err)
				}
			}
			if err := target.Exec("CREATE TABLE unrelated_data (id integer PRIMARY KEY)").Error; err != nil {
				t.Fatal(err)
			}
			before, err := publicTableNames(target)
			if err != nil {
				t.Fatal(err)
			}
			if err := runMigration(source, target); err == nil || !strings.Contains(err.Error(), "拒绝覆盖或补写") {
				t.Fatalf("unsafe existing schema accepted: %v", err)
			}
			after, err := publicTableNames(target)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("existing schema changed: before=%v after=%v %v", before, after, err)
			}
		})
	}
}

func TestMigrationExistingTargetIsReadOnly(t *testing.T) {
	target, _ := migrationPostgres(t)
	source, _ := migrationSource(t)
	if err := runMigration(source, target); err != nil {
		t.Fatal(err)
	}
	if err := target.Model(&model.IDSequence{}).Where("name = ?", "id:CHANNEL").Update("value", 101).Error; err != nil {
		t.Fatal(err)
	}
	if err := runMigration(source, target); err == nil {
		t.Fatal("different existing target accepted")
	}
	var sequence model.IDSequence
	if err := target.First(&sequence, "name = ?", "id:CHANNEL").Error; err != nil || sequence.Value != 101 {
		t.Fatalf("verification changed existing target: %+v %v", sequence, err)
	}
	assertImportedEvents(t, source, target)
}

func TestMigrationCLIReportsOnlyCommittedSuccess(t *testing.T) {
	_, dsn := migrationPostgres(t)
	_, sourcePath := migrationSource(t)
	binary := filepath.Join(t.TempDir(), "migrate.exe")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, output)
	}
	for i, message := range []string{"全量迁移核对完成", "目标库已有完整迁移结果，未重复写入"} {
		cmd := exec.Command(binary)
		cmd.Env = append(os.Environ(), "SQLITE_SOURCE_PATH="+sourcePath, "DATABASE_URL="+dsn)
		output, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(output), message) {
			t.Fatalf("CLI run %d: %v %s", i+1, err, output)
		}
	}
}
