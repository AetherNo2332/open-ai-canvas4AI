package database

import (
	"path/filepath"
	"testing"

	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func TestMigrateUserSubmapPresetOnEmptyDatabase(t *testing.T) {
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
	assertUserSubmapPresetSchema(t, db)
	status, err := ReadSchemaStatus(db)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Ready || status.Current != CurrentSchemaVersion {
		t.Fatalf("unexpected schema status: %+v", status)
	}
	// 这里只守住「预设迁移已被包含」；最新版本号由各迁移自己的专项测试钉住，
	// 否则每加一个迁移都要改这条无关断言。
	if CurrentSchemaVersion < userSubmapPresetMigrationVersion {
		t.Fatalf("latest schema %d is older than the user submap preset migration %d", CurrentSchemaVersion, userSubmapPresetMigrationVersion)
	}
}

func TestMigrateUserSubmapPresetUpgradesOldCopy(t *testing.T) {
	db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "old-copy.db")})
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
	if err := db.Migrator().DropTable(&model.UserSubmapPreset{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("version = ?", userSubmapPresetMigrationVersion).Delete(&schemaMigration{}).Error; err != nil {
		t.Fatal(err)
	}
	user := model.User{ID: "upgrade-user", Username: "upgrade-user", Role: model.UserRoleUser, Status: model.UserStatusActive}
	canvas := model.CanvasProject{ID: "upgrade-canvas", UserID: user.ID, Title: "旧档画布", Revision: 7, PayloadJSON: `{"id":"upgrade-canvas","nodes":[],"connections":[]}`}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&canvas).Error; err != nil {
		t.Fatal(err)
	}

	if err := MigrateSchema(db); err != nil {
		t.Fatalf("upgrade from v110 copy: %v", err)
	}
	assertUserSubmapPresetSchema(t, db)
	var storedUser model.User
	if err := db.First(&storedUser, "id = ?", user.ID).Error; err != nil || storedUser.Username != user.Username {
		t.Fatalf("existing user was not preserved: %+v, %v", storedUser, err)
	}
	var storedCanvas model.CanvasProject
	if err := db.First(&storedCanvas, "id = ?", canvas.ID).Error; err != nil || storedCanvas.Revision != 7 {
		t.Fatalf("existing canvas was not preserved: %+v, %v", storedCanvas, err)
	}
	preset := model.UserSubmapPreset{ID: "upgrade-preset", UserID: user.ID, Name: "升级后模板", ManifestJSON: `{"version":1,"title":"升级后模板","nodes":[{"localId":"text-1","type":"text","title":"文本"}]}`, Revision: 1}
	if err := db.Create(&preset).Error; err != nil {
		t.Fatalf("insert preset after upgrade: %v", err)
	}
	var storedPreset model.UserSubmapPreset
	if err := db.First(&storedPreset, "id = ?", preset.ID).Error; err != nil || storedPreset.UserID != user.ID {
		t.Fatalf("preset round-trip after upgrade: %+v, %v", storedPreset, err)
	}
	if err := MigrateSchema(db); err != nil {
		t.Fatalf("migration should be idempotent: %v", err)
	}
}

func assertUserSubmapPresetSchema(t *testing.T, db *gorm.DB) {
	t.Helper()
	if !db.Migrator().HasTable(&model.UserSubmapPreset{}) {
		t.Fatal("user_submap_presets table missing")
	}
	if !db.Migrator().HasIndex(&model.UserSubmapPreset{}, "idx_user_submap_presets_user_updated") {
		t.Fatal("user_submap_presets user/updated index missing")
	}
	var applied schemaMigration
	if err := db.First(&applied, "version = ?", userSubmapPresetMigrationVersion).Error; err != nil {
		t.Fatal(err)
	}
	if applied.Name != "user_submap_preset" || applied.Checksum != userSubmapPresetChecksum {
		t.Fatalf("unexpected migration record: %+v", applied)
	}
}
