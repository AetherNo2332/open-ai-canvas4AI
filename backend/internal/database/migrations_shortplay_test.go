package database

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

// W02：空库、含旧 drama 的库、PostgreSQL 三条路径都必须能把 shortplay 六张表建出来，
// 且不改动既有行。复合主键与未结束 job 的 dedupe 唯一索引由写入负例钉住。

var shortplayModelList = []any{
	&model.Shortplay{},
	&model.ShortplayEpisode{},
	&model.ShortplayAsset{},
	&model.ShortplayAssetImage{},
	&model.ShortplayAssetAudio{},
	&model.ShortplayJob{},
}

func TestMigrateShortplayWorkbenchOnEmptyDatabase(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "empty.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	status, err := ReadSchemaStatus(db)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Current != CurrentSchemaVersion {
		t.Fatalf("unexpected schema status: %+v", status)
	}
	// 只守住「shortplay 迁移已被包含」；最新版本号随后续迁移增长，不能钉死在这里。
	if CurrentSchemaVersion < shortplayWorkbenchMigrationVersion {
		t.Fatalf("latest schema %d is older than the shortplay workbench migration %d", CurrentSchemaVersion, shortplayWorkbenchMigrationVersion)
	}
	assertShortplayWorkbenchSchema(t, db)
	assertShortplayAssetIdentity(t, db)
}

func TestMigrateShortplayWorkbenchUpgradesOldDramaCopy(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "old-drama.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	if err := MigrateSchema(db); err != nil {
		t.Fatal(err)
	}
	// 造一个「升级前」的库：短剧表不存在、v112 未登记，但 drama 业务行已经在。
	for _, table := range shortplayModelList {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version = ?", shortplayWorkbenchMigrationVersion).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	project := model.Project{ID: "legacy-drama", UserID: "legacy-user", Name: "旧短剧制作台", Type: "short-drama", AspectRatio: "9:16", Status: model.ProjectStatusActive, Revision: 3}
	production := model.DramaProduction{ProjectID: project.ID, UserID: "legacy-user", Status: model.DramaStatusDraft, Revision: 2}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&production).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateSchema(db); err != nil {
		t.Fatalf("upgrade from v111 copy: %v", err)
	}
	assertShortplayWorkbenchSchema(t, db)
	var storedProject model.Project
	if err := db.First(&storedProject, "id = ?", project.ID).Error; err != nil || storedProject.Revision != 3 {
		t.Fatalf("existing drama project was not preserved: %+v, %v", storedProject, err)
	}
	var storedProduction model.DramaProduction
	if err := db.First(&storedProduction, "project_id = ?", project.ID).Error; err != nil || storedProduction.Revision != 2 {
		t.Fatalf("existing drama production was not preserved: %+v, %v", storedProduction, err)
	}
	now := time.Now()
	shortplay := model.Shortplay{ID: "sp-after-upgrade", ProjectID: project.ID, UserID: project.UserID, SettingsJSON: `{"aspectRatio":"9:16"}`, CreatedAt: now, UpdatedAt: now}
	episode := model.ShortplayEpisode{ID: "sp-after-upgrade-ep1", ShortplayID: shortplay.ID, Number: 1, Title: "第一集", CanvasID: "canvas-after-upgrade", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&shortplay).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&episode).Error; err != nil {
		t.Fatalf("shortplay episode round-trip after upgrade: %v", err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("migration should be idempotent: %v", err)
	}
}

// TestMigrateShortplayWorkbenchUpgradesV111DramaDatabase 复现真实升级路径：先按当前计划把库建到
// v111（含既有 drama 项目、制作台和画布行），再只执行 v112，验证旧数据不动、新表可用。
func TestMigrateShortplayWorkbenchUpgradesV111DramaDatabase(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "v111-drama.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	migrateSchemaToVersion(t, db, userSubmapPresetMigrationVersion)
	// 当前代码的 v1 基线 AutoMigrate 已知道 shortplay 模型，所以刚建出的「v111」会带上这六张表；
	// 真实旧库由旧代码建出、并无这些表，且 v1 记录已存在不会重跑。这里按旧代码状态删表再升级。
	for _, table := range shortplayModelList {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatal(err)
		}
	}
	if db.Migrator().HasTable(&model.Shortplay{}) {
		t.Fatal("v111 database already has shortplay tables")
	}
	var recorded int64
	if err := db.Model(&schemaMigration{}).Where("version = ?", shortplayWorkbenchMigrationVersion).Count(&recorded).Error; err != nil || recorded != 0 {
		t.Fatalf("v111 database has a v%d record: %d, %v", shortplayWorkbenchMigrationVersion, recorded, err)
	}

	user := model.User{ID: "v111-user", Username: "v111-user", Role: model.UserRoleUser, Status: model.UserStatusActive}
	project := model.Project{ID: "v111-drama", UserID: user.ID, Name: "v111 旧短剧制作台", Type: "short-drama", AspectRatio: "9:16", Status: model.ProjectStatusActive, Revision: 4}
	production := model.DramaProduction{ProjectID: project.ID, UserID: user.ID, Status: model.DramaStatusDraft, Revision: 2}
	canvas := model.CanvasProject{ID: "v111-canvas", UserID: user.ID, Title: "v111 旧画布", Revision: 7, PayloadJSON: `{"id":"v111-canvas","nodes":[],"connections":[]}`}
	for _, row := range []any{&user, &project, &production, &canvas} {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("seed v111 row: %v", err)
		}
	}

	if err := MigrateSchema(db); err != nil {
		t.Fatalf("upgrade from v111: %v", err)
	}
	status, err := ReadSchemaStatus(db)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Current != CurrentSchemaVersion {
		t.Fatalf("unexpected schema status after upgrade: %+v", status)
	}
	assertShortplayWorkbenchSchema(t, db)

	var storedProject model.Project
	if err := db.First(&storedProject, "id = ?", project.ID).Error; err != nil || storedProject.Revision != 4 {
		t.Fatalf("existing drama project was not preserved: %+v, %v", storedProject, err)
	}
	var storedProduction model.DramaProduction
	if err := db.First(&storedProduction, "project_id = ?", project.ID).Error; err != nil || storedProduction.Revision != 2 {
		t.Fatalf("existing drama production was not preserved: %+v, %v", storedProduction, err)
	}
	var storedCanvas model.CanvasProject
	if err := db.First(&storedCanvas, "id = ?", canvas.ID).Error; err != nil || storedCanvas.Revision != 7 {
		t.Fatalf("existing canvas was not preserved: %+v, %v", storedCanvas, err)
	}

	now := time.Now()
	shortplay := model.Shortplay{ID: "sp-v111-upgrade", ProjectID: project.ID, UserID: user.ID, SettingsJSON: `{"aspectRatio":"9:16"}`, CreatedAt: now, UpdatedAt: now}
	episode := model.ShortplayEpisode{ID: "sp-v111-upgrade-ep1", ShortplayID: shortplay.ID, Number: 1, Title: "第一集", CanvasID: "canvas-v111-upgrade", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&shortplay).Error; err != nil {
		t.Fatalf("create shortplay after upgrade: %v", err)
	}
	if err := db.Create(&episode).Error; err != nil {
		t.Fatalf("create episode after upgrade: %v", err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("migration should be idempotent: %v", err)
	}
}

// migrateSchemaToVersion 按当前迁移计划把空库建到指定版本，用来真实复现「升级前」的旧库。
// 它复刻 MigrateSchema 的登记/执行/校验循环，只截断版本范围，不改生产迁移代码。
func migrateSchemaToVersion(t *testing.T, db *gorm.DB, maxVersion int64) {
	t.Helper()
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range schemaMigrations {
		if item.version > maxVersion {
			continue
		}
		var applied schemaMigration
		err := db.First(&applied, "version = ?", item.version).Error
		if err == nil {
			if err := validateMigrationRecord(applied, item); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatal(err)
		}
		if err := item.apply(db); err != nil {
			t.Fatalf("apply migration %d (%s): %v", item.version, item.name, err)
		}
		record := schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum, AppliedAt: time.Now().UTC()}
		if err := db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	var current int64
	if err := db.Model(&schemaMigration{}).Select("COALESCE(MAX(version), 0)").Scan(&current).Error; err != nil {
		t.Fatal(err)
	}
	if current != maxVersion {
		t.Fatalf("schema built to %d, want %d", current, maxVersion)
	}
}

// TestMigrateShortplayWorkbenchPostgres 覆盖真实 PostgreSQL 的空库迁移、复合主键、
// 未结束 job 的 dedupe 唯一索引，以及含旧 drama 行的升级。没有 DSN 时跳过并如实记录。
func TestMigrateShortplayWorkbenchPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("CANVAS_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("CANVAS_TEST_POSTGRES_DSN is not configured")
	}
	base, err := Open(Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	baseSQL, _ := base.DB()
	defer baseSQL.Close()
	schemaName := fmt.Sprintf("shortplay_w02_%d", time.Now().UnixNano())
	if err := base.Exec(`CREATE SCHEMA "` + schemaName + `"`).Error; err != nil {
		t.Fatalf("create schema: %v", err)
	}
	defer func() {
		if err := base.Exec(`DROP SCHEMA IF EXISTS "` + schemaName + `" CASCADE`).Error; err != nil {
			t.Errorf("drop schema: %v", err)
		}
	}()
	testDSN, err := postgresDSNWithSearchPath(dsn, schemaName)
	if err != nil {
		t.Fatalf("test dsn: %v", err)
	}
	db, err := Open(Config{Driver: "postgres", DSN: testDSN})
	if err != nil {
		t.Fatalf("open test schema: %v", err)
	}
	dbSQL, _ := db.DB()
	defer dbSQL.Close()

	if err := MigrateSchema(db); err != nil {
		t.Fatalf("migrate empty postgres: %v", err)
	}
	assertShortplayWorkbenchSchema(t, db)
	assertShortplayAssetIdentity(t, db)
	assertShortplayJobDedupe(t, db)

	// 旧 drama 行 + 重跑 v112：只补短剧表，不动既有行。
	for _, table := range shortplayModelList {
		if err := db.Migrator().DropTable(table); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Where("version = ?", shortplayWorkbenchMigrationVersion).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	project := model.Project{ID: "pg-legacy-drama", UserID: "pg-user", Name: "PG 旧制作台", Type: "short-drama", AspectRatio: "16:9", Status: model.ProjectStatusActive, Revision: 5}
	if err := db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("upgrade postgres from v111: %v", err)
	}
	assertShortplayWorkbenchSchema(t, db)
	var stored model.Project
	if err := db.First(&stored, "id = ?", project.ID).Error; err != nil || stored.Revision != 5 {
		t.Fatalf("existing drama project was not preserved: %+v, %v", stored, err)
	}
}

func assertShortplayWorkbenchSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range shortplayModelList {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("table for %T missing after migration", table)
		}
	}
	var applied schemaMigration
	if err := db.First(&applied, "version = ?", shortplayWorkbenchMigrationVersion).Error; err != nil {
		t.Fatalf("shortplay migration record missing: %v", err)
	}
	if applied.Name != "shortplay_workbench_tables" || applied.Checksum != shortplayWorkbenchChecksum {
		t.Fatalf("unexpected migration record: %+v", applied)
	}
}

func assertShortplayAssetIdentity(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now()
	if err := db.Create(&model.Shortplay{ID: "sp-pg", ProjectID: "p-pg", UserID: "u-pg", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ShortplayAsset{ShortplayID: "sp-pg", ID: 1, Kind: "character", Name: "A", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ShortplayAsset{ShortplayID: "sp-pg", ID: 1, Kind: "character", Name: "B", CreatedAt: now, UpdatedAt: now}).Error; err == nil {
		t.Fatal("duplicate (shortplay_id, id) insert succeeded, want composite primary key failure")
	}
}

func assertShortplayJobDedupe(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now()
	first := model.ShortplayJob{ID: "job-1", ShortplayID: "sp-pg", Kind: "extract", Status: "queued", DedupeKey: "extract|sp-pg", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	second := model.ShortplayJob{ID: "job-2", ShortplayID: "sp-pg", Kind: "extract", Status: "queued", DedupeKey: "extract|sp-pg", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&second).Error; err == nil {
		t.Fatal("second unfinished job with same dedupe key succeeded, want unique index failure")
	}
	if err := db.Model(&model.ShortplayJob{}).Where("id = ?", first.ID).Update("status", "succeeded").Error; err != nil {
		t.Fatal(err)
	}
	third := model.ShortplayJob{ID: "job-3", ShortplayID: "sp-pg", Kind: "extract", Status: "queued", DedupeKey: "extract|sp-pg", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&third).Error; err != nil {
		t.Fatalf("re-enqueue after terminal state: %v", err)
	}
}
