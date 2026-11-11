package main

import (
	"path/filepath"
	"testing"
	"time"

	"yingce/backend/internal/database"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

func sequenceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open(database.Config{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "source.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.IDSequence{}, &model.ModelChannel{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func idSequenceMigration(t *testing.T) tableMigration {
	t.Helper()
	for _, migration := range migrations() {
		if migration.name == "id_sequences" {
			return migration
		}
	}
	t.Fatal("id_sequences missing from migration list")
	return tableMigration{}
}

func TestIDSequenceMigrationPreservesSourceAndVerifiesRepeat(t *testing.T) {
	source, target := sequenceTestDB(t), sequenceTestDB(t)
	stamp := time.Date(2025, 1, 2, 3, 4, 5, 123000000, time.UTC)
	rows := []model.IDSequence{{Name: "id:CHANNEL", Value: 100, UpdatedAt: stamp}, {Name: "id:CUSTOM", Value: 15, UpdatedAt: stamp}}
	if err := source.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if err := target.Create(&model.IDSequence{Name: "id:CHANNEL", UpdatedAt: stamp.Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	migration := idSequenceMigration(t)
	if count, err := migration.run(source, target, true); err != nil || count != 2 {
		t.Fatalf("copy source sequences over initialization rows: count=%d err=%v", count, err)
	}
	if count, err := migration.run(source, target, false); err != nil || count != 2 {
		t.Fatalf("verify repeat: count=%d err=%v", count, err)
	}
	if err := target.Model(&model.IDSequence{}).Where("name = ?", "id:CHANNEL").Update("value", 101).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := migration.run(source, target, false); err == nil {
		t.Fatal("changed target accepted")
	}
	var got model.IDSequence
	if err := target.First(&got, "name = ?", "id:CHANNEL").Error; err != nil || got.Value != 101 {
		t.Fatalf("verification wrote target: %+v %v", got, err)
	}
}

func TestIDSequenceMigrationRepeatAllowsOnlyDerivedFloor(t *testing.T) {
	source, target := sequenceTestDB(t), sequenceTestDB(t)
	for _, db := range []*gorm.DB{source, target} {
		if err := db.Create(&model.ModelChannel{ID: "CHANNEL_000003", Name: "test"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := source.Create(&model.IDSequence{Name: "id:CHANNEL", Value: 1, UpdatedAt: time.Now().Add(-time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	migration := idSequenceMigration(t)
	if _, err := migration.run(source, target, true); err != nil {
		t.Fatal(err)
	}
	if err := database.ReconcilePrefixedIDSequences(target); err != nil {
		t.Fatal(err)
	}
	if _, err := migration.run(source, target, false); err != nil {
		t.Fatalf("legitimate sequence reconciliation rejected: %v", err)
	}
	if err := target.Model(&model.IDSequence{}).Where("name = ?", "id:CHANNEL").Update("value", 4).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := migration.run(source, target, false); err == nil {
		t.Fatal("sequence beyond source/business floor accepted")
	}
}
