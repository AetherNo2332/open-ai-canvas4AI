package database

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestEcommerceRoundSchemaUpgradePreservesHistory(t *testing.T) {
	for _, version := range []int64{85, 100} {
		t.Run(fmt.Sprintf("from%d", version), func(t *testing.T) {
			testEcommerceRoundSchemaUpgrade(t, version)
		})
	}
}

func testEcommerceRoundSchemaUpgrade(t *testing.T, version int64) {
	t.Helper()
	db, err := Open(Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "upgrade.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&schemaMigration{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range schemaMigrations {
		if item.version > version {
			break
		}
		if err := item.apply(db); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&schemaMigration{Version: item.version, Name: item.name, Checksum: item.checksum, AppliedAt: time.Now()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Baseline migration uses current models. Restore the pre-round schema.
	if err := db.Migrator().DropTable(&model.EcommerceRound{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropIndex(&model.EcommerceProject{}, "ActiveRoundID"); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&model.EcommerceProject{}, "ActiveRoundID"); err != nil {
		t.Fatal(err)
	}
	want := model.EcommerceProject{ID: "old-project", UserID: "owner", Name: "Existing", Status: "draft", DraftJSON: `{"product":"existing"}`, PlanJSON: `{"taskId":"existing-task"}`, Revision: 7}
	if err := db.Omit("ActiveRoundID").Create(&want).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Omit("ActiveRoundID").First(&want, "id = ?", want.ID).Error; err != nil {
		t.Fatal(err)
	}
	var before []schemaMigration
	if err := db.Order("version").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := MigrateSchema(db); err != nil {
			t.Fatal(err)
		}
	}
	var after []schemaMigration
	if err := db.Where("version <= ?", version).Order("version").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("existing migration ledger changed")
	}
	var stored model.EcommerceProject
	if err := db.First(&stored, "id = ?", want.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, stored) {
		t.Fatalf("existing project changed: want=%+v got=%+v", want, stored)
	}
	var count int64
	if err := db.Model(&model.EcommerceRound{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("migration must defer round conversion to save: count=%d err=%v", count, err)
	}
	for _, index := range []string{"idx_ecommerce_round_ordinal", "idx_ecommerce_round_dedup", "idx_ecommerce_round_list"} {
		if !db.Migrator().HasIndex(&model.EcommerceRound{}, index) {
			t.Fatalf("missing round index %s", index)
		}
	}
	var added schemaMigration
	if err := db.First(&added, "version = ?", 101).Error; err != nil {
		t.Fatal(err)
	}
	if added.Name != "ecommerce_round_history" || added.Checksum != "sha256:ecommerce-round-history-v93-20261109" {
		t.Fatalf("unexpected v101 migration identity: %+v", added)
	}
	if err := RequireSchemaVersion(db); err != nil {
		t.Fatal(err)
	}
}
